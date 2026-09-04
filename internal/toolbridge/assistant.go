package toolbridge

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/svpchain/svpchain-perps-agent/internal/assistant"
	"github.com/svpchain/svpchain-perps-agent/internal/mcpclient"
)

// SkillAssistant is the model-driven skill: a question in English, answered by
// planning tool calls over the MCP catalog.
//
// It is registered conditionally, unlike every other family here — it needs an
// Anthropic API key and an MCP endpoint, and an operator that configures
// neither gets an agent whose card does not mention it. That is why it is not
// in New() and not in wire.PerpsProfile: those compose the surface this binary
// always serves, and this one it serves only when configured.
const SkillAssistant = "svpchain-assistant"

// AskInput is the assistant's argument object.
type AskInput struct {
	Question string `json:"question" jsonschema:"the question to answer, in plain English, e.g. \"how exposed am I to BTC right now?\""`
}

// RegisterAssistant adds the planning surface.
func (r *Registry) RegisterAssistant(a *assistant.Assistant) {
	r.add(SkillAssistant, "ask", Bound{
		InputSchema: schemaFor[AskInput](),
		Call: func(ctx context.Context, raw json.RawMessage) (any, error) {
			var in AskInput
			if len(raw) > 0 {
				if err := json.Unmarshal(raw, &in); err != nil {
					return nil, fmt.Errorf("decode args: %w", err)
				}
			}
			// The planner acts as the caller, never as the agent. An
			// unauthenticated request gets the zero identity, and every tool
			// the plan reaches then refuses on its own — which is the same
			// answer the caller would get calling those tools directly.
			id, _ := mcpclient.CallerFrom(ctx)
			return a.Ask(ctx, assistant.Request{Question: in.Question, Identity: id})
		},
	})
}
