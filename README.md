# svpchain-perps-agent

The perpetuals-trading [A2A](https://a2aproject.github.io/A2A/) agent for
SVP-Chain: a remote, server-side agent other agents call over the network.

It serves market data, accounts, unsigned order and funds tx building, the
Cosmos broadcast rail, self-service auth, faucet, the chain's `x/agent` /
`x/agentwallet` modules, and **delegated perps execution** under
[SVP-DT](https://github.com/svpchain/svpdt) credentials.

Everything above is implemented in
[`svpchain-agent-core`](https://github.com/svpchain/svpchain-agent-core); this
repo composes it — `wire.PerpsProfile` selects the operation families, and
`card.go` declares this agent's public identity.

| | |
|---|---|
| Port | 8082 |
| Advertised at | `<public-url>/perps` |
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
  --operator-key-file ./perps.key \
  --public-url https://agents.svpchain.org
```

Inspect without touching anything: `--print-config`, `--print-compose`,
`--dry-run`. Tear down with `--uninstall`.

## The operator key

Optional. Without one the agent runs keyless and the execution skills refuse
with a reason. With one, `agent_self_register` puts this agent on chain and it
can execute delegated orders and be paid through the settlement escrow.

It is a 32-byte hex eth_secp256k1 key, read from `[operator] key_file` or from
`SVPCHAIN_AGENT_OPERATOR_KEY` (which takes precedence), and shipped at mode 600.

**It must be distinct from every other agent's key.** An agent's on-chain id
derives from its key and `agent_self_register` publishes a hash of *this*
binary's card, so two agents sharing a key collide on one registry record and
overwrite each other's capability hash. Fund the key's address before
registering: the bond, plus gas for delegated execution.

## The agent card is an interface

The served card's bytes are hashed into this agent's on-chain registration, and
verifiers recompute that hash from a live fetch. `card.go` is therefore
load-bearing: change it and every deployment must run `agent_self_update`.
`cmd/svpchain-perps-agent/testdata/card.json` is a golden that makes such a
change deliberate rather than accidental — including when core is upgraded.

## Development

`GOWORK=off` is set in every Makefile target. A `go.work` in the parent
directory resolves `svpchain-agent-core` from a local checkout, which is
convenient for cross-repo work but would otherwise hide a missing `go get` bump
and ship against an untagged core.

The build needs the chain's protocol module at `../svpagent/protocol` (a go.mod
`replace`), which is also why the Docker build vendors first. `deps_test.go`
asserts this repo's replace directives still match core's — drift there fails
loudly instead of resolving upstream cosmos and erroring somewhere unrelated.
