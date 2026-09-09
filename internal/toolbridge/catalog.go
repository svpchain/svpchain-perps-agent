package toolbridge

import "sort"

// isProxied reports whether a tool is one the MCP server implements, as
// opposed to one this agent implements itself.
//
// ★ Derived from remoteOps rather than listed. It was a hand-kept list of the
// agent's own tools — list_tools, estimate_clearing_price — and "ask" was
// never added to it, so an agent with the assistant configured refused to
// start: the boot check read the assistant's own tool as an operation the card
// promised and the server could not serve. It crash-looped, and only on a
// deployment that had configured a model, which is why nothing here saw it.
//
// A second list of which tools are proxied was always going to drift from the
// first. remoteOps is the one that decides what gets proxied, so it is the one
// that decides what a catalog comparison covers.
func isProxied(tool string) bool {
	for _, tools := range remoteOps {
		for _, name := range tools {
			if name == tool {
				return true
			}
		}
	}
	return false
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
		if !isProxied(op.Tool) {
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
