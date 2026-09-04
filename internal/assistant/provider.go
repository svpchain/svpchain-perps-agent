package assistant

import (
	"context"
	"encoding/json"
)

// A Provider is a model API the planner can run on.
//
// Two exist: the native Anthropic Messages API, and the OpenAI
// chat-completions format, which OpenAI and DeepSeek both serve. The seam is
// here rather than at a wire-format adapter because the two APIs disagree
// about more than field names — what an assistant turn even contains, and what
// has to be echoed back to continue a conversation.
//
// ★ That last point is why a Provider hands out a Session that owns its own
// transcript, instead of the loop keeping one neutral history and translating
// it per request. Anthropic replays reasoning blocks that belong to the
// producing model and must come back unchanged; rebuilding an assistant turn
// from a lowest-common-denominator struct would quietly drop them. Letting
// each provider keep the history in its own shape means neither has to be
// lossy to satisfy the other.
type Provider interface {
	// Name identifies the provider in logs and in whoami-style replies.
	Name() string

	// Model is the model id in use.
	Model() string

	// Start opens a conversation with a fixed system prompt and tool set.
	// Both stay constant for its life, which is what makes them cacheable on
	// providers that cache.
	Start(system string, tools []ToolDef) Session
}

// Session is one conversation with a model. Not safe for concurrent use; the
// planner drives it one turn at a time.
type Session interface {
	// Send appends the input to the transcript and returns the model's reply.
	Send(ctx context.Context, in Input) (*Reply, error)
}

// Input is what the planner adds to the transcript before asking for a reply:
// the question on the first turn, tool results on every turn after.
type Input struct {
	// Text is a user message. Set on the first turn.
	Text string

	// ToolResults answer the tool calls from the previous reply. They go back
	// in ONE turn, which is what keeps a model willing to ask for several
	// tools at once.
	ToolResults []ToolResult

	// FinalTurn withholds the tool definitions, so the model has to answer
	// from what it has. The planner sets it when a budget is spent.
	FinalTurn bool
}

// ToolResult is one tool's output, answering one call.
type ToolResult struct {
	ID      string
	Content string
	IsError bool
}

// ToolRequest is the model asking for a tool. Distinct from ToolCall, which
// is the record of one having run.
type ToolRequest struct {
	ID   string
	Name string

	// Args is the arguments object. Providers differ on whether they send it
	// as JSON or as a string holding JSON; both normalize to raw JSON here.
	Args json.RawMessage
}

// StopReason is why the model stopped, normalized across providers.
type StopReason string

const (
	// StopEnd means the model finished its answer.
	StopEnd StopReason = "end"
	// StopToolUse means it wants tools run before it continues.
	StopToolUse StopReason = "tool_use"
	// StopRefusal means it declined the request outright.
	StopRefusal StopReason = "refusal"
	// StopLength means it hit the output cap mid-answer.
	StopLength StopReason = "length"
)

// Reply is one model turn.
type Reply struct {
	Text      string
	ToolCalls []ToolRequest
	Stop      StopReason

	InputTokens  int64
	OutputTokens int64
}

// ToolDef is one tool offered to the model, in provider-neutral form.
type ToolDef struct {
	Name        string
	Description string

	// Schema is the tool's FULL JSON Schema object — type, properties and
	// required — as the MCP server published it. Providers take different
	// slices: the Anthropic tool definition splits properties from required,
	// the OpenAI one passes the whole object as `parameters`. Keeping the
	// whole thing here is what stops `required` being dropped on the way
	// through, which is what tells a model an argument is not optional.
	Schema map[string]any
}

// Properties is the schema's properties object, or an empty one.
func (t ToolDef) Properties() map[string]any {
	if p, ok := t.Schema["properties"].(map[string]any); ok {
		return p
	}
	return map[string]any{}
}

// Required is the schema's list of required argument names.
func (t ToolDef) Required() []string {
	raw, ok := t.Schema["required"].([]any)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(raw))
	for _, v := range raw {
		if s, ok := v.(string); ok {
			out = append(out, s)
		}
	}
	return out
}
