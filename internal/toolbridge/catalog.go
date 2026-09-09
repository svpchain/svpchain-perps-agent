package toolbridge

import "sort"

// agentOwnedTools are operations this agent implements itself, with no twin on
// any MCP server. They are excluded from a catalog comparison because a remote
// that does not serve them is not drifting — it was never meant to.
var agentOwnedTools = map[string]bool{
	"list_tools":          true,
	EstimateClearingPrice: true,
}

// CatalogDiff is how a remote MCP server's tool list differs from the surface
// this agent advertises.
//
// Both directions matter, and they fail differently. A Missing tool is one the
// card promises and the server cannot serve, so a caller that reads the card
// and calls it gets an error. An Extra tool is one the server gained and this
// agent does not bridge, which costs a caller nothing today but means the
// registration table is behind the server it dispatches to.
type CatalogDiff struct {
	Missing []string
	Extra   []string
}

// OK reports whether the two surfaces agree.
func (d CatalogDiff) OK() bool { return len(d.Missing) == 0 && len(d.Extra) == 0 }

// DiffCatalog compares this registry against the tool names a remote MCP
// server serves, ignoring the agent's own operations.
func (r *Registry) DiffCatalog(remote []string) CatalogDiff {
	remoteSet := make(map[string]bool, len(remote))
	for _, name := range remote {
		remoteSet[name] = true
	}

	var d CatalogDiff
	local := map[string]bool{}
	for _, op := range r.List() {
		if agentOwnedTools[op.Tool] {
			continue
		}
		local[op.Tool] = true
		if !remoteSet[op.Tool] {
			d.Missing = append(d.Missing, op.Tool)
		}
	}
	for _, name := range remote {
		if !local[name] {
			d.Extra = append(d.Extra, name)
		}
	}
	sort.Strings(d.Missing)
	sort.Strings(d.Extra)
	return d
}
