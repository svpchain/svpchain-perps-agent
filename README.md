# svpchain-perps-agent

The perpetuals-trading [A2A](https://a2aproject.github.io/A2A/) agent for
SVP-Chain: a remote, server-side agent other agents call over the network.

It serves market data, accounts, unsigned order and funds tx building, the
Cosmos broadcast rail, and self-service auth. The agent never holds a caller's
key: every write is built unsigned, signed by the caller, and landed through
`broadcast_signed_tx`.

Everything above is implemented under `internal/`, which was the shared
`svpchain-agent-core` library until that repo was retired and folded in here.
`cmd/svpchain-perps-agent` composes it — `wire.PerpsProfile` selects the
operation families, and `card.go` declares this agent's public identity.

`internal/mcp` is a second such absorption: `svpchain-mcp`'s `lib/mcp` at tag
`v0.1.0`, the MCP tool handlers the A2A bridge dispatches into, plus the chain
clients, tx builders and policy engine under them. Unlike agent-core that repo
is still live — the EVM, lending and research agents keep importing it — so
this copy is a fork, kept diffable against the tag. `internal/mcp/doc.go` has
the details and the re-sync recipe.

Both copies were pruned to what this binary serves: no EVM DeFi, swap, bridge
or Lendora surface, and no profile but perps.

| | |
|---|---|
| Port | 8082 |
| Advertised at | `<public-url>`, verbatim |
| Image | `ghcr.io/svpchain/svpchain-perps-agent` |

## Running

```sh
go run ./cmd/svpchain-perps-agent -config cmd/svpchain-perps-agent/agent.toml
```

`/healthz` answers load-balancer liveness checks; the Agent Card is at
`/.well-known/agent-card.json`.

## Deploying

```sh
./scripts/deploy.sh --host www@host.example.com \
  --public-url https://perps-agent.svpchain.org
```

### Settings in a file instead of flags

Rather than retyping the flags, put them in a sourced shell file:

```sh
./scripts/deploy.sh --init-config     # writes the file at 0600 and names it
```

Edit what it names, then a routine install is just `./scripts/deploy.sh`.

To see what actually resolved, and from which layer:

```sh
./scripts/deploy.sh --print-env
```

The directory is named after **this agent**, not after the project, so every
agent in the fleet carries its own settings.

Precedence is flag > environment > config file > default, so
`./scripts/deploy.sh --public-url https://staging.example.org` still overrides,
and `--no-config` ignores the file. `--config-dir` (or `SVPCHAIN_CONFIG_DIR`)
points elsewhere. Because the file is sourced rather than parsed it can compute
values — and by the same token it is code, so the script refuses one that is
group- or world-writable.

The caps and `--markets-refresh` had no environment variable before this file
existed; they are settable now as `SVPCHAIN_DEPOSIT_MAX_USDC` and friends.

Inspect without touching anything: `--print-env`, `--print-config`,
`--print-compose`, `--print-nginx`, `--dry-run`. Tear down with `--uninstall`.

`--help` lists every flag. There are no EVM or bridge options: this agent has no
EVM surface, so there is nothing to configure and no `[evm]` schema to set.

## Behind the reverse proxy

This agent owns the host it advertises: it answers at the root of
`SVPCHAIN_PERPS_AGENT_PUBLIC_URL` and listens on `127.0.0.1:8082`. Nothing is
appended to that URL. Print its location block:

```sh
./scripts/deploy.sh --public-url https://perps-agent.svpchain.org --print-nginx
```

Nothing installs it. The server block it belongs in owns TLS and the host name,
both outside this repo — so paste it, then
`nginx -t && systemctl reload nginx`.

The route is not cosmetic. `public_url` is advertised inside the Agent Card
and callers dial it; if nginx does not route that host to this port the agent
advertises a URL that 404s, with every process healthy and nothing in the logs.
`TestDeployScriptNginxRouteMatchesConfig` pins the two together.

## The agent card is an interface

The served card is what callers read to learn this agent's surface, so
`card.go` is load-bearing. `cmd/svpchain-perps-agent/testdata/card.json` is a
golden that makes a change deliberate rather than accidental — including when
the skill text under `internal/a2aserver` changes, which moves the card just as
surely.

## Development

`GOWORK=off` is set in every Makefile target. A `go.work` in the parent
directory would resolve dependencies from sibling checkouts rather than the
versions `go.mod` pins — convenient for cross-repo work, but it can ship a build
against a revision no tag points at.

The build needs the chain's protocol module at `../svpagent/protocol` (a go.mod
`replace`), which is also why the Docker build vendors first. Because Go does
not apply a dependency's own `replace` directives, this `go.mod` must restate
every one of protocol's verbatim; `deps_test.go` diffs the two on every
`go test ./...`, so drift fails loudly instead of resolving upstream cosmos and
erroring somewhere unrelated.

`internal/` is the former `svpchain-agent-core` and `internal/mcp` the former
`svpchain-mcp/lib/mcp`. The sibling agent repos (`evm`, `lending`, `research`)
still import both modules and are unaffected by these copies; agent-core needs
their migration before it can be deleted, and `svpchain-mcp` is not going away
at all — it still ships `cmd/mcp-server`. Fixes landing there do not reach
`internal/mcp` on their own.
