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

A host that is only reachable through a bastion takes `--jump-box` (or
`SVPCHAIN_DEPLOY_JUMP_BOX`); every ssh and rsync in the run then goes through
it as `ssh -J`, so nothing but the connection is forwarded and your keys stay
on this machine. Comma-separate hops to chain them.

```sh
./scripts/deploy.sh --host www@10.0.1.7 --jump-box ops@bastion.example.com
```

### Settings in a file instead of flags

Rather than retyping the flags, put them in a sourced shell file:

```sh
./scripts/deploy.sh --init-config     # writes the file at 0600 and names it
```

Edit what it names, then a routine install is just `./scripts/deploy.sh`.

To see what actually resolved, and from which layer:

```sh
./scripts/deploy.sh --print-env       # the key prints as "set (64 chars)", never its value
```

The directory is named after **this agent**, not after the project, so every
agent in the fleet carries its own. That is not filing tidiness: an agent's
on-chain id derives from its owner key, so two agents sharing one key would be
a single id claiming two cards — and the chain refuses the second. A directory
per agent makes that hard to do by accident, where one shared file would
invite it.

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

The route is not cosmetic. `public_url` is advertised inside the Agent Card,
and a verifier fetches that URL to recompute the capability hash; if nginx does
not route that host to this port the agent advertises a URL that 404s and reads
as unverified, with every process healthy and nothing in the logs.
`TestDeployScriptNginxRouteMatchesConfig` pins the two together.

## The owner key

The agent itself holds no key: every write it builds is signed by the caller,
so nothing on the deployed host can sign anything, and the deploy ships no
key there. What *does* need a key is putting the agent on chain. The **owner
key** is the account that signs `MsgRegisterAgent`, pays the registration fee
and the bond, and controls the record thereafter. It lives in the config
directory on the machine you deploy from and is read by `--register` alone.

Mint one:

```sh
./scripts/deploy.sh --gen-owner-key
```

It writes `owner.key` into the config dir at 0600, rewrites `config.sh` to
read the key from there, and prints the `svp1…` address — the part you cannot
work out by looking at the key, and the address the fee, the bond and gas must
be funded to. The key itself is written, never printed. A second run refuses:
this one account is registered as **both owner and operator**, so it is the
agent's on-chain id (`did:svp:<address>`) as well as the account holding its
bond, and another key is a new agent, not a replacement. Back the file up —
losing it strands the registration and the bond, and a fresh key is not a
recovery.

The same fact has a second consequence: `x/agent` binds an operator address to
at most one agent, so **one key registers exactly one agent**. A fleet keeps
one per agent, which the per-agent config directory already arranges.

Setting it by hand works too — `config.sh` is sourced, so it can compute the
value rather than store it:

```sh
SVPCHAIN_PERPS_AGENT_OWNER_KEY="$(op read op://vault/svpchain-perps-agent/owner-key)"
```

There is no flag for it: a key in `argv` shows up in `ps` and in your shell
history.

## Putting it on chain

Registration is not a deploy step and cannot be one. What gets published is the
sha256 of the agent card **as served**, so the thing being registered has to be
a running agent answering at a URL. Deploy first, then:

```sh
./scripts/deploy.sh --register
```

It fetches the card from the public URL, hashes exactly those bytes, checks the
card's own interface URL agrees with the endpoint being registered, and then
signs locally with the owner key — the right message for the state it finds:

| state | call |
|---|---|
| not registered | `MsgRegisterAgent` (`--bond` overrides the module's `MinBond`) |
| registered, card hash moved | `MsgUpdateAgent` |
| registered, endpoint, capabilities or pricing moved | `MsgUpdateAgent` |
| registered and current | nothing, and it says so |

Both drifts are otherwise silent. A stale capability hash makes verifiers read
the agent as unverified while every process is healthy; a stale endpoint points
them at a URL that may no longer answer.

The transaction is broadcast from **your** machine, so it needs the registry
chain's Cosmos REST API (the gRPC-gateway, typically `:1317`) reachable from
there — `--agent-chain-rest` / `SVPCHAIN_AGENT_CHAIN_REST`. `--agent-chain-id`
/ `SVPCHAIN_AGENT_CHAIN_ID` names the chain the signature commits to.

There was a gRPC route beside it, defaulting to a loopback address. It only
ever suited a local dev node, and left unset it turned "you have not said how
to reach the chain" into a dial timeout against localhost. A node's REST port
is far more often exposed anyway.

When only the deploy host can see the node, tunnel its REST port first and
point the setting at the local end:

```sh
ssh -N -L 1317:127.0.0.1:1317 www@svpdev1.example.com   # -J bastion if there is one
SVPCHAIN_AGENT_CHAIN_REST=http://127.0.0.1:1317 ./scripts/deploy.sh --register
```

`--dry-run` prints what would be submitted without broadcasting.

`cmd/agent-register` is the client underneath. Run it directly to reach the
agent some other way — over an ssh tunnel before DNS is live, say:

```sh
SVPCHAIN_PERPS_AGENT_OWNER_KEY=… go run ./cmd/agent-register \
  -url http://127.0.0.1:8082 -chain-id svp-2517-1 -rest http://127.0.0.1:1317 \
  -capabilities perps.trading,perps.market-data -price-amount 1000000
```

`-rest` is required: REST is the only route. `-agent-chain-rest` / `-agent-chain-id` are
accepted as aliases (the deploy script's flag names), and `-rest` / `-chain-id` default to
`SVPCHAIN_AGENT_CHAIN_REST` and `SVPCHAIN_AGENT_CHAIN_ID` from the environment
— the same names `config.sh` sets — so sourcing the config file is enough.

`-price-amount` is the fee advertised on chain per `-price-unit` (default
`call`), in the settlement token's smallest unit; the chain refuses a
registration without one. The deploy passes `--price-amount` /
`SVPCHAIN_OPERATOR_PRICE_AMOUNT` (default `1000000`) through.

## The agent card is an interface

The served card's bytes are hashed into this agent's on-chain registration, and
verifiers recompute that hash from a live fetch. `card.go` is therefore
load-bearing: change it and every deployment must re-run
`./scripts/deploy.sh --register`, which updates the record when it sees the
drift. `cmd/svpchain-perps-agent/testdata/card.json` is a golden that makes
such a change deliberate rather than accidental — including when the skill
text under `internal/a2aserver` changes, which moves the card just as surely.

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
