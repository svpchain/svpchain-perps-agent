// Package indexer is a small Go client for the svpchain Indexer's Comlink
// REST API (/v4/*). There is no in-tree Go indexer client, so this package
// builds one — typed endpoints + DTOs only for fields the MCP tools actually
// use.
//
// Client carries a *http.Client (5s read / 30s overall), an optional bearer
// token (Comlink may be gated in production), and a per-host rate limiter
// (golang.org/x/time/rate) for outbound throttling. The HTTP-client shape
// mirrors daemons/types.RequestHandler and the pricefeed daemon's sub_task_runner.
package indexer
