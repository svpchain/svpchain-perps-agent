package assistant

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/svpchain/svpchain-perps-agent/internal/mcpclient"
)

// Defaults for the bounds an operator rarely sets. See the package doc: a
// planning loop that will not stop is a bill.
// Provider names, as an operator writes them in the config.
const (
	// ProviderAnthropic is the native Messages API. The default, because it
	// is the one that caches the tool catalog.
	ProviderAnthropic = "anthropic"

	// ProviderOpenAI is the chat-completions format, which OpenAI, DeepSeek
	// and most local runtimes serve.
	ProviderOpenAI = "openai"
)

const (
	DefaultModel         = "claude-opus-5"
	DefaultMaxIterations = 8
	DefaultMaxToolCalls  = 24
	DefaultTimeout       = 90 * time.Second
	DefaultMaxTokens     = 8000
)

// Config tunes the planner.
type Config struct {
	Model string

	// MaxIterations bounds trips through the model loop; MaxToolCalls bounds
	// tool calls across the whole request. Both exist because one model turn
	// can request several tools at once, so neither bounds the other.
	MaxIterations int
	MaxToolCalls  int

	// Timeout is the wall clock for the whole request, planning included.
	Timeout time.Duration

	// MaxTokens caps one model response.
	MaxTokens int64
}

func (c *Config) applyDefaults() {
	if c.Model == "" {
		c.Model = DefaultModel
	}
	if c.MaxIterations <= 0 {
		c.MaxIterations = DefaultMaxIterations
	}
	if c.MaxToolCalls <= 0 {
		c.MaxToolCalls = DefaultMaxToolCalls
	}
	if c.Timeout <= 0 {
		c.Timeout = DefaultTimeout
	}
	if c.MaxTokens <= 0 {
		c.MaxTokens = DefaultMaxTokens
	}
}

// Assistant plans MCP tool calls to answer a question.
type Assistant struct {
	provider Provider
	mcp      *mcpclient.Client
	cfg      Config
	system   string

	// The catalog is read from the MCP server, so it needs the server to be
	// up. Loading it lazily rather than at boot keeps a server that is down
	// from stopping the agent starting, which matters because every other
	// skill still answers without it. The first question after the server
	// comes back picks it up.
	catalogMu  sync.Mutex
	catalog    *Catalog
	catalogErr error
}

// New returns an assistant. It reaches the MCP server on the first question,
// not here.
func New(provider Provider, mcp *mcpclient.Client, cfg Config) *Assistant {
	cfg.applyDefaults()
	return &Assistant{provider: provider, mcp: mcp, cfg: cfg, system: systemPrompt}
}

// Provider is the model API in use, for logs and diagnostics.
func (a *Assistant) Provider() Provider { return a.provider }

// ensureCatalog loads the tool catalog once and caches it. A failure is not
// cached: the server being down for one question should not disable the skill
// for the life of the process.
func (a *Assistant) ensureCatalog(ctx context.Context) (*Catalog, error) {
	a.catalogMu.Lock()
	defer a.catalogMu.Unlock()
	if a.catalog != nil {
		return a.catalog, nil
	}
	cat, err := BuildCatalog(ctx, a.mcp)
	if err != nil {
		return nil, err
	}
	a.catalog = cat
	return cat, nil
}

// Request is one natural-language question, asked as somebody.
type Request struct {
	Question string

	// Identity is the A2A caller. Every tool call the planner makes carries
	// it, so the planner can only read what its caller could already read.
	Identity mcpclient.Identity
}

// ToolCall records one dispatch, so an answer can be audited back to the data
// it came from. A model-written answer that cannot be traced to its reads is
// not something to act on.
type ToolCall struct {
	Tool     string          `json:"tool"`
	Args     json.RawMessage `json:"args,omitempty"`
	Error    string          `json:"error,omitempty"`
	Duration string          `json:"duration"`
}

// Answer is the planner's reply.
type Answer struct {
	Text      string     `json:"text"`
	ToolCalls []ToolCall `json:"tool_calls"`

	// Provider and Model say who produced this answer. A caller comparing
	// answers across deployments needs to know which model wrote one, and an
	// operator switching providers needs to see the switch took effect.
	Provider string `json:"provider"`
	Model    string `json:"model"`

	// Truncated reports that a bound stopped the loop before the model was
	// finished. The text is then the best answer available, not a complete
	// one, and it says so.
	Truncated bool `json:"truncated,omitempty"`

	InputTokens  int64 `json:"input_tokens"`
	OutputTokens int64 `json:"output_tokens"`
}

const systemPrompt = `You answer questions about a svpchain perpetuals DEX account by calling tools.

Rules:
- Answer only from tool results. Never invent a number, price, size or address.
- Call tools in parallel when they do not depend on each other.
- Tools that build a transaction return an UNSIGNED payload. You cannot sign or
  submit anything, and neither can this agent. When you build one, say plainly
  that the caller must review and sign it themselves.
- If a tool reports that authentication is required, stop and say so. Do not
  retry it and do not try to authenticate.
- If you cannot answer from the tools available, say what is missing.
- Be brief. Report figures as the tools gave them; do not round or reformat.`

// Ask runs the planning loop and returns the answer.
func (a *Assistant) Ask(ctx context.Context, req Request) (*Answer, error) {
	if strings.TrimSpace(req.Question) == "" {
		return nil, fmt.Errorf("question is empty")
	}

	ctx, cancel := context.WithTimeout(ctx, a.cfg.Timeout)
	defer cancel()

	catalog, err := a.ensureCatalog(ctx)
	if err != nil {
		return nil, err
	}

	ctx = withMaxTokens(ctx, a.cfg.MaxTokens)
	session := a.provider.Start(a.system, catalog.Tools())
	next := Input{Text: req.Question}
	ans := &Answer{ToolCalls: []ToolCall{}, Provider: a.provider.Name(), Model: a.provider.Model()}

	for iter := 0; ; iter++ {
		// The bound is checked before the call that would exceed it, so the
		// final turn is one where the model is told to answer with what it has
		// rather than one that is cut off mid-plan.
		last := iter >= a.cfg.MaxIterations-1 || len(ans.ToolCalls) >= a.cfg.MaxToolCalls
		if last {
			ans.Truncated = true
			next.FinalTurn = true
			next.Text = "You have reached this request's tool budget. Answer now with what you have, and say which part you could not check."
		}

		reply, err := session.Send(ctx, next)
		if err != nil {
			return nil, fmt.Errorf("plan: %w", err)
		}
		ans.InputTokens += reply.InputTokens
		ans.OutputTokens += reply.OutputTokens

		if reply.Stop == StopRefusal {
			return nil, fmt.Errorf("the model declined this request")
		}
		if reply.Stop != StopToolUse || last {
			ans.Text = reply.Text
			if ans.Text == "" {
				return nil, fmt.Errorf("the model returned no answer")
			}
			return ans, nil
		}

		results, records := a.runTools(ctx, catalog, reply.ToolCalls, req.Identity)
		ans.ToolCalls = append(ans.ToolCalls, records...)
		next = Input{ToolResults: results}
	}
}

// runTools dispatches one turn's tool calls concurrently and returns their
// results in the order the model asked for them.
func (a *Assistant) runTools(ctx context.Context, catalog *Catalog, calls []ToolRequest, id mcpclient.Identity) ([]ToolResult, []ToolCall) {
	results := make([]ToolResult, len(calls))
	records := make([]ToolCall, len(calls))

	var wg sync.WaitGroup
	for i, c := range calls {
		wg.Add(1)
		go func(i int, c ToolRequest) {
			defer wg.Done()
			started := time.Now()
			rec := ToolCall{Tool: c.Name, Args: c.Args}

			text, isErr := a.dispatch(ctx, catalog, c.Name, c.Args, id)
			if isErr {
				rec.Error = text
			}
			rec.Duration = time.Since(started).Round(time.Millisecond).String()

			records[i] = rec
			results[i] = ToolResult{ID: c.ID, Content: text, IsError: isErr}
		}(i, c)
	}
	wg.Wait()
	return results, records
}

// dispatch runs one tool and renders its result as text for the model.
//
// A failure comes back as a tool error rather than aborting the request: the
// model can then say what it could not check, which is a better answer than
// nothing. The one thing it must not do is retry, and the system prompt says
// so for the auth case, where retrying cannot help.
func (a *Assistant) dispatch(ctx context.Context, catalog *Catalog, name string, args json.RawMessage, id mcpclient.Identity) (string, bool) {
	// A model can name a tool it was not given. That is a tool error, not a
	// dispatch: the withheld set is a boundary, so it has to hold here too and
	// not only in what the catalog advertises.
	if !catalog.Allows(name) {
		if why, withheld := WithheldTools[name]; withheld {
			return fmt.Sprintf("%s is not available to you: %s", name, why), true
		}
		return fmt.Sprintf("no such tool: %s", name), true
	}

	res, err := a.mcp.CallTool(ctx, mcpclient.Call{
		Tool:       name,
		Args:       args,
		Identity:   id,
		Idempotent: readOnlyTools[name],
	})
	if err != nil {
		return fmt.Sprintf("%s failed: %v", name, err), true
	}
	if len(res.Structured) > 0 {
		return string(res.Structured), res.IsError
	}
	if res.Text != "" {
		return res.Text, res.IsError
	}
	return "(the tool returned nothing)", res.IsError
}
