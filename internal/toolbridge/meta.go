package toolbridge

import (
	"context"
	"encoding/json"

	"github.com/svpchain/svpchain-perps-agent/internal/mcpclient"
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
	// Description is what the tool does, as published by whatever implements
	// it. Empty for an operation whose source publishes none.
	Description string `json:"description,omitempty"`

	// InputSchema is the JSON Schema of the tool's args object. Omitted for an
	// operation registered without a typed input, which a caller should treat
	// as an object of unspecified shape.
	//
	// Typed as any because the two sources differ: this agent's own tools
	// reflect a Go type, and a proxied tool's schema is the MCP server's own
	// JSON, passed through rather than re-derived.
	InputSchema any `json:"input_schema,omitempty"`
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
		Call: func(ctx context.Context, raw json.RawMessage) (any, error) {
			var in ListToolsInput
			if len(raw) > 0 {
				if err := json.Unmarshal(raw, &in); err != nil {
					return nil, err
				}
			}
			return r.listTools(ctx, in.Skill), nil
		},
	})
}

func (r *Registry) listTools(ctx context.Context, skill string) ListToolsOutput {
	remote := r.remoteToolInfo(ctx)

	out := ListToolsOutput{Tools: []ToolDescriptor{}}
	for _, op := range r.List() {
		if skill != "" && op.Skill != skill {
			continue
		}
		d := ToolDescriptor{Skill: op.Skill, Tool: op.Tool}
		if op.InputSchema != nil {
			d.InputSchema = op.InputSchema
		}
		// A proxied tool's description and schema come from the server that
		// implements it, so the prose a caller reads is the prose written next
		// to the handler rather than a copy free to drift.
		if info, ok := remote[op.Tool]; ok {
			d.Description = info.Description
			if info.InputSchema != nil {
				d.InputSchema = info.InputSchema
			}
		}
		out.Tools = append(out.Tools, d)
	}
	return out
}

// toolInfo is what an implementing server publishes about one tool.
type toolInfo struct {
	Description string
	InputSchema any
}

// useRemoteToolInfo makes list_tools serve the MCP server's own descriptions
// and schemas. Fetched on first use and cached; a failure is not cached and
// simply leaves the extra detail out, since the tool list itself is local and
// still correct without it.
func (r *Registry) useRemoteToolInfo(mcp *mcpclient.Client) {
	r.fetchToolInfo = func(ctx context.Context) (map[string]toolInfo, error) {
		tools, err := mcp.ListTools(ctx)
		if err != nil {
			return nil, err
		}
		out := make(map[string]toolInfo, len(tools))
		for _, t := range tools {
			out[t.Name] = toolInfo{Description: t.Description, InputSchema: t.InputSchema}
		}
		return out, nil
	}
}

func (r *Registry) remoteToolInfo(ctx context.Context) map[string]toolInfo {
	if r.fetchToolInfo == nil {
		return nil
	}
	r.infoMu.Lock()
	defer r.infoMu.Unlock()
	if r.toolInfo != nil {
		return r.toolInfo
	}
	info, err := r.fetchToolInfo(ctx)
	if err != nil {
		return nil
	}
	r.toolInfo = info
	return info
}
