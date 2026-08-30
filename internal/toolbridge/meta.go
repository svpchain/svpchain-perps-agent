package toolbridge

import (
	"context"
	"encoding/json"

	"github.com/google/jsonschema-go/jsonschema"
)

// The A2A surface has no equivalent of MCP's tools/list: a caller that reaches
// this agent over A2A can read the Agent Card, but the card names each skill's
// tools only inside its prose description and carries no argument schemas at
// all. That is enough to call a tool you already know and not enough to
// discover one, which leaves an LLM-driven caller (svpchain-agent's a2amcp
// transport, for one) unable to use this agent unless it also holds an MCP
// connection to the same handlers.
//
// list_tools closes that: it serves the registry's own contents — tool names,
// their skill, and the argument schema reflected from the handler's input type.
// Because it reads the same registry the executor dispatches from, it cannot
// advertise an operation that would not run.
//
// It deliberately carries no per-tool description. Those live in the MCP
// server's registration table, which this repo did not absorb; restating them
// here would create a second copy free to drift from the handlers. The
// reflected schemas keep their jsonschema struct tags, so the per-argument
// prose does come across.
type ListToolsInput struct {
	Skill string `json:"skill,omitempty" jsonschema:"list only this skill's tools, e.g. \"svpchain-account\"; empty lists every tool this agent serves"`
}

// ToolDescriptor is one entry in a list_tools reply.
type ToolDescriptor struct {
	Skill string `json:"skill"`
	Tool  string `json:"tool"`
	// InputSchema is the JSON Schema of the tool's args object. Omitted for an
	// operation registered without a typed input, which a caller should treat
	// as an object of unspecified shape.
	InputSchema *jsonschema.Schema `json:"input_schema,omitempty"`
}

// ListToolsOutput is the list_tools reply, sorted by tool name.
type ListToolsOutput struct {
	Tools []ToolDescriptor `json:"tools"`
}

// RegisterMeta adds the self-description surface. The listing closes over the
// registry and reads it when called, so it reports whatever the binary
// registered regardless of the order these Register* calls run in.
func (r *Registry) RegisterMeta() {
	r.add(SkillMeta, "list_tools", Bound{
		InputSchema: schemaFor[ListToolsInput](),
		Call: func(_ context.Context, raw json.RawMessage) (any, error) {
			var in ListToolsInput
			if len(raw) > 0 {
				if err := json.Unmarshal(raw, &in); err != nil {
					return nil, err
				}
			}
			return r.listTools(in.Skill), nil
		},
	})
}

func (r *Registry) listTools(skill string) ListToolsOutput {
	out := ListToolsOutput{Tools: []ToolDescriptor{}}
	for _, op := range r.List() {
		if skill != "" && op.Skill != skill {
			continue
		}
		out.Tools = append(out.Tools, ToolDescriptor{
			Skill:       op.Skill,
			Tool:        op.Tool,
			InputSchema: op.InputSchema,
		})
	}
	return out
}
