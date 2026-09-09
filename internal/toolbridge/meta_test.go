package toolbridge

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

type metaIn struct {
	Ticker string `json:"ticker" jsonschema:"market to read, e.g. BTC-USD"`
}

func metaRegistry() *Registry {
	r := newRegistry()
	r.add("skill-a", "read_thing", Bound{InputSchema: schemaFor[metaIn]()})
	r.add("skill-b", "other_thing", Bound{InputSchema: schemaFor[metaIn]()})
	r.RegisterMeta()
	return r
}

func listTools(t *testing.T, r *Registry, args string) ListToolsOutput {
	t.Helper()
	op, ok := r.Lookup("list_tools")
	if !ok {
		t.Fatal("list_tools must be registered")
	}
	if op.Skill != SkillMeta {
		t.Fatalf("list_tools belongs to %q, want %q", op.Skill, SkillMeta)
	}
	var raw json.RawMessage
	if args != "" {
		raw = json.RawMessage(args)
	}
	res, err := op.Call(context.Background(), raw)
	if err != nil {
		t.Fatalf("list_tools: %v", err)
	}
	out, ok := res.(ListToolsOutput)
	if !ok {
		t.Fatalf("list_tools returned %T, want ListToolsOutput", res)
	}
	return out
}

// The whole point of the surface: a caller with no MCP connection learns the
// tool names, their skill, and the shape of their arguments.
func TestListToolsServesNamesSkillsAndSchemas(t *testing.T) {
	out := listTools(t, metaRegistry(), "")

	byTool := map[string]ToolDescriptor{}
	for _, d := range out.Tools {
		byTool[d.Tool] = d
	}
	for _, want := range []string{"read_thing", "other_thing", "list_tools"} {
		if _, ok := byTool[want]; !ok {
			t.Errorf("list_tools omitted %q", want)
		}
	}
	if got := byTool["read_thing"].Skill; got != "skill-a" {
		t.Errorf("read_thing skill = %q, want skill-a", got)
	}

	schema := byTool["read_thing"].InputSchema
	if schema == nil {
		t.Fatal("read_thing must carry an input schema")
	}
	bz, err := json.Marshal(schema)
	if err != nil {
		t.Fatal(err)
	}
	// The jsonschema struct tags must survive: the per-argument prose is the
	// only description this surface carries.
	if !strings.Contains(string(bz), "market to read") {
		t.Errorf("schema lost its field description: %s", bz)
	}
	if !strings.Contains(string(bz), "ticker") {
		t.Errorf("schema lost its field name: %s", bz)
	}
}

func TestListToolsFiltersBySkill(t *testing.T) {
	out := listTools(t, metaRegistry(), `{"skill":"skill-a"}`)
	if len(out.Tools) != 1 || out.Tools[0].Tool != "read_thing" {
		t.Fatalf("skill filter returned %+v", out.Tools)
	}
}

func TestListToolsIsSortedAndNeverNil(t *testing.T) {
	out := listTools(t, metaRegistry(), `{"skill":"nothing-registered"}`)
	if out.Tools == nil {
		t.Error("an empty listing must marshal as [], not null")
	}

	all := listTools(t, metaRegistry(), "")
	for i := 1; i < len(all.Tools); i++ {
		if all.Tools[i-1].Tool > all.Tools[i].Tool {
			t.Fatalf("listing is not sorted: %q before %q", all.Tools[i-1].Tool, all.Tools[i].Tool)
		}
	}
}

// list_tools reads the registry it was built over, so it cannot advertise an
// operation the executor would refuse to dispatch.
func TestListToolsCannotOutrunTheRegistry(t *testing.T) {
	r := metaRegistry()
	for _, d := range listTools(t, r, "").Tools {
		op, ok := r.Lookup(d.Tool)
		if !ok {
			t.Errorf("list_tools advertised %q, which does not dispatch", d.Tool)
			continue
		}
		if op.Skill != d.Skill {
			t.Errorf("%q listed under %q but dispatches under %q", d.Tool, d.Skill, op.Skill)
		}
	}
}
