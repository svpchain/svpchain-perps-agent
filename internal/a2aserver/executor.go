package a2aserver

import (
	"context"
	"encoding/json"
	"fmt"
	"iter"
	"strings"

	"github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/a2aproject/a2a-go/v2/a2asrv"

	"github.com/svpchain/svpchain-perps-agent/internal/toolbridge"
)

// Executor answers A2A tasks by dispatching them into the operation registry.
//
// Dispatch is a lookup, not a model call. A request names a skill, a tool and
// its arguments as JSON, and the executor dispatches it — pricing an order or
// building a tx payload has one right answer, and putting a model in front of
// that would add cost, latency and a failure mode for no gain.
//
// The one exception is a message that is not an envelope at all. Free text is
// routed to the assistant skill where a binary serves one, because a question
// in English is the natural thing for another agent to send and answering it
// is exactly what that skill is for. A caller that names its tool never
// reaches a model.
type Executor struct {
	registry *toolbridge.Registry
	authr    *AuthResolver
}

// assistantTool is the operation free text is routed to.
const assistantTool = "ask"

var _ a2asrv.AgentExecutor = (*Executor)(nil)

// NewFullExecutor returns an executor serving the whole operation registry.
func NewFullExecutor(registry *toolbridge.Registry, authr *AuthResolver) *Executor {
	return &Executor{registry: registry, authr: authr}
}

func (e *Executor) Execute(ctx context.Context, execCtx *a2asrv.ExecutorContext) iter.Seq2[a2a.Event, error] {
	return func(yield func(a2a.Event, error) bool) {
		if execCtx.Message == nil {
			yield(nil, fmt.Errorf("empty message"))
			return
		}

		if execCtx.StoredTask == nil {
			if !yield(a2a.NewSubmittedTask(execCtx, execCtx.Message), nil) {
				return
			}
		}
		if !yield(a2a.NewStatusUpdateEvent(execCtx, a2a.TaskStateWorking, nil), nil) {
			return
		}

		result, err := e.handle(ctx, execCtx)
		if err != nil {
			// A refused or malformed request completes the task with the
			// reason as its answer, not a transport error or a failed state:
			// the task ran and produced an answer, and that answer is "no".
			// A caller distinguishes this from a crash.
			result = fmt.Sprintf("error: %v", err)
		}

		// The answer rides the terminal status update — once the submitted
		// task exists, the protocol forbids bare Message events.
		reply := a2a.NewMessageForTask(a2a.MessageRoleAgent, execCtx, a2a.NewTextPart(result))
		yield(a2a.NewStatusUpdateEvent(execCtx, a2a.TaskStateCompleted, reply), nil)
	}
}

// Cancel is a no-op: operations are synchronous request/response calls with
// nothing to unwind.
func (e *Executor) Cancel(ctx context.Context, execCtx *a2asrv.ExecutorContext) iter.Seq2[a2a.Event, error] {
	return func(yield func(a2a.Event, error) bool) {
		yield(a2a.NewStatusUpdateEvent(execCtx, a2a.TaskStateCanceled, nil), nil)
	}
}

// handle dispatches one request and returns its JSON result.
func (e *Executor) handle(ctx context.Context, execCtx *a2asrv.ExecutorContext) (string, error) {
	raw := messageText(execCtx.Message)

	var req Request
	if err := json.Unmarshal([]byte(raw), &req); err != nil {
		// ★ Plain text is not a malformed envelope, it is the most natural
		// thing one agent sends another — and this agent grew a skill whose
		// entire job is answering it. Refusing it while advertising that skill
		// made us the one agent in the fleet that cannot take a question.
		//
		// So an unparseable message becomes a question for the assistant, when
		// one is configured. It costs a model call, which the operator opted
		// into by configuring it, and the planner's own budgets bound what one
		// question can spend.
		if q, ok := e.asAssistantQuestion(raw); ok {
			req = q
		} else {
			return "", fmt.Errorf("%s: %w", e.envelopeHelp(), err)
		}
	}
	if req.Skill == "" {
		return "", fmt.Errorf("no skill named — %s", e.envelopeHelp())
	}

	// ★ The {"skill":…,"query":…} form is gone. It was served from a direct
	// indexer client with no credential, which is the one thing that could not
	// move onto the MCP server: every tool there is bearer-gated. A caller on
	// that form gets this, which names what replaced each query.
	if req.Query != "" {
		return "", fmt.Errorf(
			"the %q form was removed; name a tool instead — markets/market/orderbook/funding are list_markets, get_market, get_orderbook and get_historical_funding, and estimate is %s. All need a bearer from svpchain-auth",
			"query", toolbridge.EstimateClearingPrice)
	}

	if req.Tool == "" {
		return "", fmt.Errorf("no tool named for %s", req.Skill)
	}
	op, ok := e.registry.Lookup(req.Tool)
	if !ok {
		return "", fmt.Errorf("unknown tool %q", req.Tool)
	}
	if op.Skill != req.Skill {
		return "", fmt.Errorf("tool %q belongs to skill %q, not %q", req.Tool, op.Skill, req.Skill)
	}

	if e.authr != nil {
		ctx = e.authr.Attach(ctx, execCtx, &req)
	}

	resp := Response{Skill: req.Skill, Tool: req.Tool}
	result, err := op.Call(ctx, req.Args)
	if err != nil {
		resp.Error = err.Error()
	} else {
		resp.OK = true
		resp.Result = result
	}
	b, err := json.Marshal(resp)
	if err != nil {
		return "", fmt.Errorf("encode result: %w", err)
	}
	return string(b), nil
}

// asAssistantQuestion turns free text into a call on the assistant skill, when
// this binary serves one. Empty text is not a question.
func (e *Executor) asAssistantQuestion(raw string) (Request, bool) {
	if strings.TrimSpace(raw) == "" {
		return Request{}, false
	}
	op, ok := e.registry.Lookup(assistantTool)
	if !ok || op.Skill != toolbridge.SkillAssistant {
		return Request{}, false
	}
	args, err := json.Marshal(toolbridge.AskInput{Question: raw})
	if err != nil {
		return Request{}, false
	}
	return Request{Skill: op.Skill, Tool: op.Tool, Args: args}, true
}

// envelopeHelp tells a caller how to phrase a request, and says whether plain
// English is an option here. A refusal that does not say what would have
// worked leaves the caller with nothing to try.
func (e *Executor) envelopeHelp() string {
	help := `a request must be JSON naming a skill and tool, e.g. ` +
		`{"skill":"svpchain-market-data","tool":"list_markets"}. ` +
		`Call {"skill":"svpchain-meta","tool":"list_tools"} to discover every tool and its arguments`
	if _, ok := e.registry.Lookup(assistantTool); ok {
		return help + `. This agent also answers plain-English questions: send the question as the message text, with a bearer from svpchain-auth for anything account-scoped`
	}
	return help
}

// messageText concatenates the text parts of a message. Mirrors the helper the
// wallet agent uses, so both read A2A messages the same way.
func messageText(msg *a2a.Message) string {
	if msg == nil {
		return ""
	}
	var out strings.Builder
	for _, part := range msg.Parts {
		if part == nil {
			continue
		}
		if text := part.Text(); text != "" {
			out.WriteString(text)
		}
	}
	return strings.TrimSpace(out.String())
}
