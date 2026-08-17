package tools

// ★ Rescued verbatim from upstream tools/registry.go during the absorption of
// svpchain-mcp v0.1.0 (see internal/mcp/doc.go). registry.go was
// Register(srv *mcp.Server, h *Handlers) — the MCP-server registration path,
// which this binary does not use: it bridges these same handlers onto A2A
// operations through internal/toolbridge instead. The Handlers type and its
// constructor were the only parts of that file anything here reached.

// Handlers bundles all MCP tool handlers. ChainID is read at boot from
// config and stamped onto every TxPayload + whoami response. Deps carries
// the rest.
type Handlers struct {
	ChainID string
	Deps    Deps
}

// New constructs a Handlers from the chain id and dep bundle.
func New(chainID string, deps Deps) *Handlers {
	return &Handlers{ChainID: chainID, Deps: deps}
}
