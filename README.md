# svpchain-perps-agent

The perpetuals-trading [A2A](https://a2aproject.github.io/A2A/) agent for
SVP-Chain: a remote, server-side agent other agents call over the network.

It serves market data, accounts, unsigned order and funds tx building, the
Cosmos broadcast rail, self-service auth, faucet, the chain's `x/agent` /
`x/agentwallet` modules, and **delegated perps execution** under
[SVP-DT](https://github.com/svpchain/svpdt) credentials.

Everything above is implemented under `internal/`, which was the shared
`svpchain-agent-core` library until that repo was retired and folded in here.
`cmd/svpchain-perps-agent` composes it — `wire.PerpsProfile` selects the
operation families, and `card.go` declares this agent's public identity.

Being the only consumer, the vendored copy was pruned to what this binary
serves: no EVM DeFi or Lendora surface, and no profile but perps.

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
  --public-url https://agents.svpchain.org
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
on-chain id derives from its operator key, so two agents sharing one key would
be a single id claiming two cards. A directory per agent makes that hard to do
by accident, where one shared file would invite it.

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

The agents share one host, each on its own path: this one answers at
`<base>/perps` and listens on `127.0.0.1:8082`. Print its location block:

```sh
./scripts/deploy.sh --public-url https://agents.svpchain.org --print-nginx
```

Nothing installs it. The server block it belongs in owns TLS and the base
host, both shared with agents this repo must not know about — so paste it,
then `nginx -t && systemctl reload nginx`.

The route is not cosmetic. `public_url` is advertised inside the Agent Card,
and a verifier fetches that URL to recompute the capability hash; if nginx
does not route `/perps` to this port the agent advertises a URL that 404s and
reads as unverified, with every process healthy and nothing in the logs.
`TestDeployScriptNginxRouteMatchesConfig` pins the two together.

## The operator key

Optional. Without one the agent runs keyless and the execution skills refuse
with a reason. With one, `agent_self_register` puts this agent on chain and it
can execute delegated orders and be paid through the settlement escrow.

It is a 32-byte hex eth_secp256k1 key. A local run reads it from
`[operator] key_file` or from `SVPCHAIN_PERPS_AGENT_OPERATOR_KEY`, which takes
precedence. The variable is named for *this* agent rather than the fleet, for
the same reason the config directory is: one shared name across agents is one
id claiming several cards.

For a deploy the key goes in `config.sh` as
`SVPCHAIN_PERPS_AGENT_OPERATOR_KEY`, holding the hex itself rather than a path.
There is no flag for it — a key in `argv` shows up in `ps` and in your shell
history. Because the file is sourced it can compute the value, so the key need
not sit in plaintext on the machine you deploy from:

```sh
SVPCHAIN_PERPS_AGENT_OPERATOR_KEY="$(op read op://vault/perps/key)"
```

On the remote it lands as a **docker compose secret** mounted read-only at
`/run/secrets/operator_key`, which is what `key_file` then points at. Not a
container environment variable — `docker inspect` and `/proc/<pid>/environ`
would both expose that.

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
change deliberate rather than accidental — including when the skill text under
`internal/a2aserver` changes, which moves the card just as surely.

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

`internal/` is the former `svpchain-agent-core`. The sibling agent repos
(`evm`, `lending`, `research`) still import that module and are unaffected by
this copy; they need their own migration before it can be deleted.
