// Package toolbridge exposes the MCP tool handlers in internal/mcp/tools as
// A2A operations.
//
// Every MCP handler shares one shape, func(ctx, *mcp.CallToolRequest, In)
// (*mcp.CallToolResult, Out, error), and none of them reads the request
// parameter (auth, IP, and session travel in ctx). That uniformity is what
// lets a single generic adapter serve all of them: decode the A2A args into
// In, call the handler with a nil request, return Out. The bridge adds no
// behavior — authorization, limits, and refusal messages are the handlers'
// own, so the A2A surface and the MCP surface cannot drift.
package toolbridge

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"sync"

	"github.com/google/jsonschema-go/jsonschema"
)

// Op is one invokable operation: a tool bound to the A2A skill it is
// advertised under.
type Op struct {
	Skill string
	Tool  string
	Call  func(ctx context.Context, args json.RawMessage) (any, error)

	// InputSchema describes the args object, reflected from the same In type
	// the MCP server's mcp.AddTool reflects over — including the jsonschema
	// struct tags, so the per-field prose comes across too. Nil for an
	// operation registered without a typed input; callers should read that
	// as "an object, contents unspecified".
	InputSchema *jsonschema.Schema
}

// Bound is what an adapt helper produces: a call plus the schema of the
// arguments it decodes. Returning both together is what lets add() record a
// schema without every registration site restating the handler's input type.
type Bound struct {
	Call        func(ctx context.Context, args json.RawMessage) (any, error)
	InputSchema *jsonschema.Schema
}

// schemaFor reflects In the way mcp.AddTool does. A type that will not reflect
// yields nil rather than a panic: an unusable schema must not stop the agent
// from serving the tool.
func schemaFor[In any]() *jsonschema.Schema {
	s, err := jsonschema.For[In](nil)
	if err != nil {
		return nil
	}
	return s
}

// Registry maps tool names to operations and groups them by skill for the
// Agent Card.
type Registry struct {
	ops map[string]Op

	// Descriptions and schemas for proxied tools, fetched from the MCP server
	// on first use by list_tools. Nil on a registry with no remote behind it.
	infoMu        sync.Mutex
	fetchToolInfo func(context.Context) (map[string]toolInfo, error)
	toolInfo      map[string]toolInfo
}

func newRegistry() *Registry { return &Registry{ops: map[string]Op{}} }

func (r *Registry) add(skill, tool string, b Bound) {
	if _, dup := r.ops[tool]; dup {
		panic(fmt.Sprintf("toolbridge: duplicate tool %q", tool))
	}
	r.ops[tool] = Op{Skill: skill, Tool: tool, Call: b.Call, InputSchema: b.InputSchema}
}

// List returns every registered operation, sorted by tool name. The listing
// surface (list_tools) and the completeness tests read this.
func (r *Registry) List() []Op {
	out := make([]Op, 0, len(r.ops))
	for _, op := range r.ops {
		out = append(out, op)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Tool < out[j].Tool })
	return out
}

// Lookup returns the operation registered under tool.
func (r *Registry) Lookup(tool string) (Op, bool) {
	op, ok := r.ops[tool]
	return op, ok
}

// BySkill returns tool names grouped by skill, each group sorted — the card
// generator and the completeness tests read this.
func (r *Registry) BySkill() map[string][]string {
	out := map[string][]string{}
	for _, op := range r.ops {
		out[op.Skill] = append(out[op.Skill], op.Tool)
	}
	for _, tools := range out {
		sort.Strings(tools)
	}
	return out
}

// AddOpForTest registers a bare operation. It exists so tests in other
// packages can put a stand-in on the registry without a model or an MCP
// server behind it; nothing in the binary calls it.
func (r *Registry) AddOpForTest(skill, tool string, call func(context.Context, json.RawMessage) (any, error)) {
	r.add(skill, tool, Bound{Call: call})
}
