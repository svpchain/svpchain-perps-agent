#!/usr/bin/env bash
#
# scripts/deploy.sh — install the svpchain perps agent onto a remote SSH host
# as a docker container.
#
# This agent serves the perpetuals slice of the SVP-Chain A2A surface on
# :8082, advertised at the public URL you give it verbatim — no path segment is
# appended, so that URL must reach this agent. It is deployed independently: its
# sibling agents (research, evm, lending) each own their own repo and script,
# so nothing here knows or cares about them.
#
# Flow: build (vendored, so the go.mod replace to ../svpagent/protocol never
# leaves the deploy machine) → docker save (cached by image id) → rsync one
# staging dir (agent.toml, docker-compose.yml) plus the image tar to
# ~/svpchain-perps-agent → docker load → docker compose up -d → smoke-test
# /healthz and the agent card over loopback.
#
# The remote needs only docker + the compose v2 plugin reachable by the ssh
# user without sudo. Auth state is in-memory, so a redeploy wipes it; the
# transfer-out caps persist on the data volume.
#
# Config file (so a routine install needs no flags at all):
#   ~/.config/svpchain-perps-agent/config.sh
#
#   A shell file setting the SVPCHAIN_* variables named below. The directory is
#   this agent's, not the project's, so each agent keeps its own.
#   Precedence: flag > environment > config file > default.
#
#   --config-dir <path>            Look for it here.      SVPCHAIN_CONFIG_DIR
#   --no-config                    Ignore it entirely.
#
#   See scripts/config.sh.example. Copy it, chmod 600, edit.
#
# Required:
#   --host user@hostname           SSH target.            SVPCHAIN_DEPLOY_HOST
#
# Optional:
#   --jump-box user@bastion        Reach --host through this SSH jump host
#                                  (ssh -J). Every ssh and rsync in the run
#                                  goes via it. Comma-separate to chain hops.
#                                                         SVPCHAIN_DEPLOY_JUMP_BOX
#
# The agent-registry chain (registration only — the agent itself never dials a
# chain; its operations are served by the MCP server below):
#   --agent-chain-id <id>          The chain carrying x/agent, which the
#                                  registration signature commits to.
#                                  SVPCHAIN_AGENT_CHAIN_ID   (svp-2517-1)
#                                  --chain-id / SVPCHAIN_CHAIN_ID are the
#                                  deprecated former names, still honoured.
#
# Services:
#   --mcp-endpoint <url>           The remote MCP server implementing this
#                                  agent's operations (svpchain-dex-mcp). It
#                                  serves MCP at the root of its listener, so
#                                  this is an origin, not a path. Reached from
#                                  the agent container, so a sidecar on the
#                                  same host is http://127.0.0.1:<port>.
#                                  SVPCHAIN_MCP_ENDPOINT
#                                  (https://dex-mcp-testnet.svpchain.org/)
#
#   SVPCHAIN_ANTHROPIC_API_KEY
#                                  Enables the svpchain-assistant skill, which
#                                  answers a plain-English question by planning
#                                  MCP tool calls. Unset, the skill is not
#                                  served and the card does not advertise it.
#                                  No flag: a key in argv shows up in `ps`.
#                                  This is the ONE secret shipped to the host —
#                                  the agent calls the model at runtime — as
#                                  ${install_dir}/assistant.env at 0600.
#
#   SVPCHAIN_OPENAI_API_KEY        The same, for the openai-format provider.
#                                  Shipped the same way; which one is read
#                                  follows from --assistant-provider.
#
#   --assistant-provider <name>    anthropic (default) or openai. The openai
#                                  format is served by OpenAI, DeepSeek and
#                                  most local runtimes.
#                                                    SVPCHAIN_ASSISTANT_PROVIDER
#   --assistant-base-url <url>     Override the provider's endpoint — this is
#                                  how one provider reaches several vendors,
#                                  e.g. https://api.deepseek.com with the
#                                  openai provider. Not a secret.
#                                                    SVPCHAIN_ASSISTANT_BASE_URL
#   --assistant-model <id>         Model id. Optional on anthropic, required
#                                  on openai, whose vendors share no names.
#                                                       SVPCHAIN_ASSISTANT_MODEL
#
# Identity and registration:
#   --public-url <url>             The URL this agent advertises, used verbatim.
#                                  SVPCHAIN_PERPS_AGENT_PUBLIC_URL
#
#   SVPCHAIN_PERPS_AGENT_OWNER_KEY
#                                  The hex eth_secp256k1 OWNER key itself, not a
#                                  path. Needed only by --register: it signs the
#                                  registration and pays the fee and the bond.
#                                  NOT needed to install, and never shipped to
#                                  the host — the agent signs nothing, so a key
#                                  there would be risk with no use.
#                                  This one account is registered as both owner
#                                  and operator, so the agent id derives from it
#                                  and it can register exactly ONE agent.
#                                  There is no flag for it, deliberately: a key
#                                  on the command line lands in `ps` and in your
#                                  shell history. Let --gen-owner-key mint one
#                                  and wire the config file to it, or set it
#                                  yourself, which the sourced config file can
#                                  compute:
#                                    SVPCHAIN_PERPS_AGENT_OWNER_KEY="$(op read …)"
#   --operator-capabilities <csv>  Capability tags the registration is indexed
#                                  by. Default "perps.trading,perps.market-data".
#                                  SVPCHAIN_OPERATOR_CAPABILITIES
#   --operator-metadata <text>     SVPCHAIN_OPERATOR_METADATA
#   --price-amount <uint>          Fee per --price-unit, advertised on chain, in
#                                  the settlement token's smallest unit. Default
#                                  1000000 (one USDV at six decimals).
#                                  SVPCHAIN_OPERATOR_PRICE_AMOUNT
#   --price-unit <text>            Default "call". SVPCHAIN_OPERATOR_PRICE_UNIT
#
# Optional families and tuning:
# Build and placement:
#   --image-tag <tag>              Default <git-short-sha>.
#   --platform <p>                 Default linux/amd64.
#   --skip-build                   Reuse the local image.
#   --install-dir <path>           Default ~/svpchain-perps-agent on remote.
#                                  SVPCHAIN_INSTALL_DIR
#
# Modes:
#   --init-config                  Write a starter config file to the config dir
#                                  at 0600 and exit. Refuses to overwrite.
#   --gen-owner-key                Mint the key this agent registers under into
#                                  the config dir at 0600, point the config file
#                                  at it, and print the svp1… address to fund.
#                                  The key is written, never printed, and never
#                                  shipped to the host. Refuses if one already
#                                  exists: this key is both the agent's on-chain
#                                  id and the account holding its bond, so a
#                                  second one is a new agent, not a replacement.
#   --register                     Put the DEPLOYED agent on chain: fetch the
#                                  card from --public-url, hash it, and sign a
#                                  registration with the owner key HERE — or an
#                                  update when the card or the endpoint has
#                                  moved since. Idempotent: an agent that is
#                                  already current is left alone.
#   --agent-chain-rest <url>       --register only, and REQUIRED to register.
#                                  The registry chain's Cosmos REST API (the
#                                  gRPC-gateway, typically :1317), as reachable
#                                  from THIS machine — registration is signed
#                                  and broadcast here, not on the deploy host,
#                                  so tunnel the port if only the deploy host
#                                  can see the node.
#                                  SVPCHAIN_AGENT_CHAIN_REST
#   --bond <coin>                  --register only. Initial bond, e.g.
#                                  5000000000000000000000asvp. Default: the
#                                  module's MinBond.
#   --print-env                    Show every setting, its resolved value and
#                                  where it came from. The owner key prints as
#                                  "set"/"unset", never its value.
#   --print-config / --print-compose / --print-nginx
#   --dry-run / --uninstall
#
# Examples:
#   ./scripts/deploy.sh --init-config       # then edit the file it names
#   ./scripts/deploy.sh --gen-owner-key     # mint an identity, print its address
#   ./scripts/deploy.sh --register          # put the deployed agent on chain
#   ./scripts/deploy.sh                     # a configured install takes no flags
#   ./scripts/deploy.sh --host www@svpdev1.example.com \
#     --public-url https://perps-agent.svpchain.org
#   ./scripts/deploy.sh --uninstall --host www@svpdev1.example.com
#   ./scripts/deploy.sh --host www@10.0.1.7 --jump-box ops@bastion.example.com
#
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=lib/common.sh
source "${SCRIPT_DIR}/lib/common.sh"

fail() { printf "  ${C_RED}✗${C_RESET} %s\n" "$*" >&2; exit 1; }
warn() { printf "  ${C_YELLOW}!${C_RESET} %s\n" "$*" >&2; }

# ---- this agent ------------------------------------------------------------
#
# AGENT_PORT is the whole route contract, stated once: it lands in listen_addr,
# in the nginx proxy_pass upstream and in the smoke test. The advertised URL is
# whatever --public-url says, verbatim — this agent hangs off its own host at
# the root rather than off a shared host at a path segment, so there is no
# segment to keep in sync. Two copies of either fact is how an agent ends up
# advertising a URL that 404s with every process healthy and nothing in the
# logs, which is why TestDeployScriptNginxRouteMatchesConfig pins the two
# renderers together by cross-checking --print-config against --print-nginx.
#
# Ahead of the arguments because the config directory is named after
# AGENT_NAME, and a second spelling of the agent's own name is exactly the kind
# of duplicated fact the paragraph above is about.
readonly AGENT_NAME="svpchain-perps-agent"
readonly AGENT_PORT="8082"
readonly IMAGE_REPO="ghcr.io/svpchain/svpchain-perps-agent"

# The owner key does NOT ship to the remote, and there is deliberately no
# machinery here for sending it. The agent signs nothing — callers sign their
# own transactions — so the remote has no use for a key, while this one holds
# the agent's bond and its registry record. A key on a deployed host that
# nothing reads is pure downside. It stays in the config dir on the machine
# that registers, and only --register reads it.

# ---- config file -----------------------------------------------------------
#
# Every setting below can come from a sourced shell file, so a routine install
# is `./scripts/deploy.sh` rather than a dozen flags:
#
#   ~/.config/<agent-name>/config.sh
#
# The directory is named after this agent, not after the project, so each agent
# in the fleet carries its own. That is what keeps the owner keys apart: an
# agent's on-chain id derives from its key, so two agents sharing one would
# collide on a single registry record — and a directory per agent makes that
# hard to do by accident rather than merely discouraged.
#
# Precedence: CLI flag > environment > config file > default. The environment
# outranks the file so a one-off `SVPCHAIN_DEPLOY_HOST=… deploy` still works,
# and the flags outrank everything.
#
# It is *sourced*, not parsed: a config file can compute its values, and by the
# same token it is arbitrary code running as you.
config_dir="${SVPCHAIN_CONFIG_DIR:-${XDG_CONFIG_HOME:-$HOME/.config}/${AGENT_NAME}}"
use_config="1"

# Pre-scan, because the file must be sourced before the defaults below read
# the environment, and that happens before the main argument loop runs.
for ((_i = 1; _i <= $#; _i++)); do
  case "${!_i}" in
    --config-dir) _j=$((_i + 1)); config_dir="${!_j:-}" ;;
    --no-config)  use_config="0" ;;
  esac
done
unset _i _j

# Names the config file may set. Snapshotted before sourcing so anything the
# caller already exported survives.
readonly CONFIG_VARS=(
  SVPCHAIN_DEPLOY_HOST SVPCHAIN_DEPLOY_JUMP_BOX SVPCHAIN_AGENT_CHAIN_ID SVPCHAIN_CHAIN_ID
  SVPCHAIN_PERPS_AGENT_PUBLIC_URL SVPCHAIN_PERPS_AGENT_OWNER_KEY
  SVPCHAIN_AGENT_CHAIN_REST
  SVPCHAIN_OPERATOR_CAPABILITIES SVPCHAIN_OPERATOR_METADATA
  SVPCHAIN_OPERATOR_PRICE_AMOUNT SVPCHAIN_OPERATOR_PRICE_UNIT
  SVPCHAIN_INSTALL_DIR
  SVPCHAIN_MCP_ENDPOINT SVPCHAIN_ANTHROPIC_API_KEY SVPCHAIN_OPENAI_API_KEY
  SVPCHAIN_ASSISTANT_PROVIDER SVPCHAIN_ASSISTANT_BASE_URL SVPCHAIN_ASSISTANT_MODEL
)

# source_config — source the config file if it exists, refusing one that other
# users can write. It runs as you; a writable config file is a way into this
# shell, and the key paths it names.
source_config() {
  local file="$1"
  [[ -f "$file" ]] || return 0
  if [[ -n "$(find "$file" -perm -g+w -o -perm -o+w 2>/dev/null)" ]]; then
    fail "refusing to source a group- or world-writable config file: ${file} (chmod 600 it)"
  fi
  # shellcheck disable=SC1090
  source "$file" || fail "config file failed to load: ${file}"
}

# Which names were already set in the environment when the script started.
# Snapshotted unconditionally, and deliberately NOT discarded afterwards:
# restoring the environment over the config file is what implements
# "environment beats config file", and --print-env reads the same list to
# report where each setting came from. A space-padded string rather than an
# associative array, because macOS still ships bash 3.2.
ENV_PRESET=" "
for _v in "${CONFIG_VARS[@]}"; do
  [[ -n "${!_v:-}" ]] && ENV_PRESET+="${_v} "
done
unset _v

# was_preset — did this name arrive from the environment rather than the file?
was_preset() { [[ "$ENV_PRESET" == *" $1 "* ]]; }

if [[ "$use_config" == "1" ]]; then
  _preset=()
  for _v in "${CONFIG_VARS[@]}"; do
    [[ -n "${!_v:-}" ]] && _preset+=("${_v}=${!_v}")
  done

  source_config "${config_dir}/config.sh"

  # Restoring the pre-source environment over whatever the file set is what
  # implements "environment beats config file".
  for _kv in ${_preset+"${_preset[@]}"}; do
    printf -v "${_kv%%=*}" '%s' "${_kv#*=}"
  done
  unset _preset _v _kv
fi

# ★ The deprecated name, honoured rather than ignored. A config file setting
# only SVPCHAIN_CHAIN_ID would otherwise fall back to the default and sign a
# registration against the wrong chain, silently. Resolved here, where the
# precedence layers have already settled, so --print-env still reports the
# origin correctly rather than calling it a default.
if [[ -z "${SVPCHAIN_AGENT_CHAIN_ID:-}" && -n "${SVPCHAIN_CHAIN_ID:-}" ]]; then
  warn "SVPCHAIN_CHAIN_ID is the deprecated name for SVPCHAIN_AGENT_CHAIN_ID; rename it in your config file"
  SVPCHAIN_AGENT_CHAIN_ID="$SVPCHAIN_CHAIN_ID"
  was_preset SVPCHAIN_CHAIN_ID && ENV_PRESET+="SVPCHAIN_AGENT_CHAIN_ID "
fi


# ---- args ------------------------------------------------------------------

mode="install"        # install | uninstall | init-config | gen-owner-key
                      #         | register | print-env | print-config
                      #         | print-compose | print-nginx

# Settings a flag overrode, so --print-env can say so. Same space-padded-string
# trick as ENV_PRESET, for the same bash 3.2 reason.
FLAG_SET=" "
mark_flag() { FLAG_SET+="$1 "; }
was_flag()  { [[ "$FLAG_SET" == *" $1 "* ]]; }

host=""
jump_box="${SVPCHAIN_DEPLOY_JUMP_BOX:-}"
# ★ One chain id, named for what it is: the chain carrying x/agent, which the
# registration signature commits to. It used to be two — SVPCHAIN_CHAIN_ID for
# the DEX chain the agent served, SVPCHAIN_AGENT_CHAIN_ID to override it when
# x/agent lived elsewhere. The agent stopped serving a chain when its operations
# moved to the MCP server, so the first meaning went and both names came to
# denote the same thing.
#
# The old name is still honoured rather than ignored: a config file setting only
# SVPCHAIN_CHAIN_ID would otherwise fall back to the default and sign against
# the wrong chain, silently.
agent_chain_id="${SVPCHAIN_AGENT_CHAIN_ID:-svp-2517-1}"
# The remote MCP server this agent's operations run on. Unlike the chain
# endpoints above it does NOT default to loopback: there is a deployed server
# already serving this catalog, and pointing at it is the working default.
# Running svpchain-dex-mcp beside this agent instead is a matter of setting
# this to its port.
mcp_endpoint="${SVPCHAIN_MCP_ENDPOINT:-https://dex-mcp-testnet.svpchain.org/}"
# The model API key MATERIAL. Like the owner key it takes no flag, for the same
# reason: a secret in argv is visible in `ps` and lands in shell history. Unlike
# the owner key it IS shipped to the host, because the agent calls the model at
# runtime rather than at deploy time — so it goes in a 0600 env file beside the
# config, never into agent.toml, which is rendered, rsynced and printable.
anthropic_api_key="${SVPCHAIN_ANTHROPIC_API_KEY:-}"
# The same, for the openai-format provider. Which of the two is read follows
# from assistant.provider; both are shipped when both are set, so switching
# provider is a config change rather than a re-keying.
openai_api_key="${SVPCHAIN_OPENAI_API_KEY:-}"
# Which model API the planner speaks, where it lives, and which model. The URL
# and model are ordinary settings, not secrets: the endpoint is public and
# seeing it in --print-config is a diagnostic.
assistant_provider="${SVPCHAIN_ASSISTANT_PROVIDER:-}"
assistant_base_url="${SVPCHAIN_ASSISTANT_BASE_URL:-}"
assistant_model="${SVPCHAIN_ASSISTANT_MODEL:-}"
public_url="${SVPCHAIN_PERPS_AGENT_PUBLIC_URL:-https://agent-testnet.svpchain.org}"
# The owner key MATERIAL, not a path. There is deliberately no flag for it:
# a hex key in argv is visible in `ps` and lands in shell history. The config
# file is sourced, so it can compute the value instead of storing it.
owner_key="${SVPCHAIN_PERPS_AGENT_OWNER_KEY:-}"
operator_capabilities="${SVPCHAIN_OPERATOR_CAPABILITIES:-perps.trading,perps.market-data}"
operator_metadata="${SVPCHAIN_OPERATOR_METADATA:-}"
price_amount="${SVPCHAIN_OPERATOR_PRICE_AMOUNT:-1000000}"
price_unit="${SVPCHAIN_OPERATOR_PRICE_UNIT:-call}"
install_dir="${SVPCHAIN_INSTALL_DIR:-~/svpchain-perps-agent}"
image_tag=""
platform="linux/amd64"
skip_build="0"
dry_run="0"
# --register only. Deliberately not a config setting: the bond is a decision
# made once, at registration, not a property of every deploy — and empty takes
# the x/agent module's own MinBond, which is the right answer for almost
# everyone.
register_bond=""
# --register only, and the only route there is. The registry chain's Cosmos
# REST API as reachable from THIS machine — registration is signed and
# broadcast here, not on the deploy host — so a public gateway, or the local
# end of an ssh tunnel.
#
# ★ There was a gRPC route beside this one, defaulting to 127.0.0.1:9090. It
# only ever suited a local dev node, and left unset it turned "you have not
# said how to reach the chain" into a dial timeout against localhost. A node's
# REST port is far more often exposed than its gRPC port, so one route, no
# default, and a refusal that says what is missing.
agent_chain_rest="${SVPCHAIN_AGENT_CHAIN_REST:-}"

while [[ $# -gt 0 ]]; do
  case "$1" in
    --host)                   host="$2"; mark_flag SVPCHAIN_DEPLOY_HOST;              shift 2 ;;
    --jump-box)               jump_box="$2"; mark_flag SVPCHAIN_DEPLOY_JUMP_BOX;      shift 2 ;;
    --agent-chain-id)         agent_chain_id="$2"; mark_flag SVPCHAIN_AGENT_CHAIN_ID; shift 2 ;;
    --chain-id)               agent_chain_id="$2"; mark_flag SVPCHAIN_AGENT_CHAIN_ID; shift 2 ;;
    --mcp-endpoint)           mcp_endpoint="$2"; mark_flag SVPCHAIN_MCP_ENDPOINT;   shift 2 ;;
    --assistant-provider)     assistant_provider="$2"; mark_flag SVPCHAIN_ASSISTANT_PROVIDER; shift 2 ;;
    --assistant-base-url)     assistant_base_url="$2"; mark_flag SVPCHAIN_ASSISTANT_BASE_URL; shift 2 ;;
    --assistant-model)        assistant_model="$2"; mark_flag SVPCHAIN_ASSISTANT_MODEL;       shift 2 ;;
    --public-url)             public_url="$2"; mark_flag SVPCHAIN_PERPS_AGENT_PUBLIC_URL;  shift 2 ;;
    --operator-capabilities)  operator_capabilities="$2"; mark_flag SVPCHAIN_OPERATOR_CAPABILITIES; shift 2 ;;
    --operator-metadata)      operator_metadata="$2"; mark_flag SVPCHAIN_OPERATOR_METADATA; shift 2 ;;
    --price-amount)           price_amount="$2"; mark_flag SVPCHAIN_OPERATOR_PRICE_AMOUNT; shift 2 ;;
    --price-unit)             price_unit="$2"; mark_flag SVPCHAIN_OPERATOR_PRICE_UNIT; shift 2 ;;
    --install-dir)            install_dir="$2"; mark_flag SVPCHAIN_INSTALL_DIR;       shift 2 ;;
    --image-tag)              image_tag="$2";         shift 2 ;;
    --platform)               platform="$2";          shift 2 ;;
    # Already handled by the pre-scan above; consumed here so they are not
    # rejected as unknown.
    --config-dir)             mark_flag SVPCHAIN_CONFIG_DIR; shift 2 ;;
    --no-config)              shift ;;
    --init-config)            mode="init-config";     shift ;;
    --gen-owner-key)          mode="gen-owner-key";   shift ;;
    --register)               mode="register";        shift ;;
    --bond)                   register_bond="$2";     shift 2 ;;
    --agent-chain-rest)       agent_chain_rest="$2"; mark_flag SVPCHAIN_AGENT_CHAIN_REST; shift 2 ;;
    --print-env)              mode="print-env";       shift ;;
    --skip-build)             skip_build="1";         shift ;;
    --print-config)           mode="print-config";    shift ;;
    --print-compose)          mode="print-compose";   shift ;;
    --print-nginx)            mode="print-nginx";     shift ;;
    --dry-run)                dry_run="1";            shift ;;
    --uninstall)              mode="uninstall";       shift ;;
    -h|--help)
      sed -n '2,/^set -euo/p' "${BASH_SOURCE[0]}" | sed -n '/^#/p' | sed 's/^# \{0,1\}//'
      exit 0 ;;
    *) fail "unknown flag: $1" ;;
  esac
done

: "${host:=${SVPCHAIN_DEPLOY_HOST:-}}"

# Every ssh in this script, and rsync's transport, goes through $ssh_cmd so a
# jump box applies to all of them at once. -J is ProxyJump: the connection to
# $host is tunnelled through the bastion, end-to-end encrypted, and the
# operator's keys never leave this machine (no agent forwarding needed — but
# the bastion must be able to reach $host, and $host must accept the same key
# the bastion did, or one it has its own ssh config for). Kept as a string,
# not an array, because rsync -e wants one.
ssh_cmd="ssh -o BatchMode=yes"
if [[ -n "$jump_box" ]]; then
  ssh_cmd+=" -J $(printf '%q' "$jump_box")"
fi

# Strip a trailing slash (from the flag or env) so the card's
# "<public_url>/invoke" join stays clean. Nothing else is done to it: what you
# pass is what the agent advertises, and a reverse proxy has to route exactly
# that URL here.
public_url="${public_url%/}"

# $owner_key holds the key material itself, seeded from the environment above.
# Normalised and validated once by resolve_owner_key.
#
# Empty is the normal state for everything except --register. An install needs
# no key: nothing it renders or ships mentions one, because the deployed agent
# signs nothing. --register is the one mode that refuses without it, since
# registering IS the owner proving it holds the key the record is bound to.

# ---- shared helpers -------------------------------------------------------

# render_agent_toml — emit this agent's agent.toml on stdout. Takes no
# arguments on purpose: --print-config and the deploy render it the same way
# from the same globals, so a preview is the file that ships.
#
# listen_addr is always 0.0.0.0:<port> inside the container; --network host
# means that's also the host-bound port. The optional blocks mirror
# internal/config exactly: unset keys → those operations refuse at
# call time. There is no EVM surface here — wire.PerpsProfile builds no EVM
# clients, so this binary would never read one.
render_agent_toml() {
  cat <<EOF
# Auto-generated by scripts/deploy.sh — do not edit by hand.
# Agent: ${AGENT_NAME}

listen_addr = "0.0.0.0:${AGENT_PORT}"
public_url  = "${public_url}"

# Every operation this agent serves is one call to this server.
[mcp]
endpoint = "${mcp_endpoint}"
EOF
  if [[ -n "${assistant_provider}${assistant_base_url}${assistant_model}" ]]; then
    echo ""
    echo "[assistant]"
    [[ -n "$assistant_provider" ]] && echo "provider = \"${assistant_provider}\""
    [[ -n "$assistant_base_url" ]] && echo "base_url = \"${assistant_base_url}\""
    [[ -n "$assistant_model"    ]] && echo "model    = \"${assistant_model}\""
  fi
  # Explicit, because the block above ends on a `[[ … ]] && echo` whose false
  # branch would otherwise be this function's exit status — and under `set -e`
  # the `render_agent_toml > file` call site would exit the script silently.
  return 0
}

# render_compose_yaml — emit the docker-compose.yml that runs this agent: the
# image, its config mount, its data volume and its TCP port. Volumes use
# absolute host paths so `docker compose up -d` works from any directory. The
# rendered config and the data volume live flat in ${install_dir}.
render_compose_yaml() {
  echo "# Auto-generated by scripts/deploy.sh — do not edit by hand."
  echo "services:"
  # ★ ARGS ONLY — no binary path. This image declares
  # ENTRYPOINT ["/bin/svpchain-perps-agent"], and compose `command:`
  # overrides CMD, not ENTRYPOINT. Naming the binary here (as the old shared
  # multi-binary image required, since it had no ENTRYPOINT) would launch
  # `svpchain-perps-agent svpchain-perps-agent -config …` and die on flag
  # parsing.
  cat <<EOF
  ${AGENT_NAME}:
    image: ${image_ref}
    container_name: ${AGENT_NAME}
    restart: unless-stopped
    command: ["-config", "/etc/${AGENT_NAME}/agent.toml"]
    # network_mode: host — the listener binds 0.0.0.0:${AGENT_PORT} (compose
    # \`ports:\` is ignored in host mode; the port lives in agent.toml).
    network_mode: host
    volumes:
      - ${install_dir}/agent.toml:/etc/${AGENT_NAME}/agent.toml:ro
      - ${install_dir}/data:/var/lib/${AGENT_NAME}
EOF
  # env_file rather than `environment:`, so the key is never a literal in a
  # generated file that --print-compose will happily echo.
  if [[ -n "${anthropic_api_key}${openai_api_key}" ]]; then
    echo "    env_file:"
    echo "      - ${install_dir}/assistant.env"
  fi
  return 0
}

require_install_args() {
  [[ -n "$host" ]] || fail "--host is required (or set SVPCHAIN_DEPLOY_HOST)"
}

# validate_hex_key — the VALUE must look like a 32-byte hex owner key.
# Takes the key itself, not a path, so validation happens before the material
# is written anywhere. The error deliberately does not echo the value.
validate_hex_key() {
  [[ "$1" =~ ^(0x)?[0-9a-fA-F]{64}$ ]] \
    || fail "SVPCHAIN_PERPS_AGENT_OWNER_KEY does not look like a 32-byte hex key (got ${#1} characters)"
}

# resolve_owner_key — normalise and validate the key material supplied in
# SVPCHAIN_PERPS_AGENT_OWNER_KEY. Empty is allowed through: an install needs no
# key at all, and only --register refuses without one.
#
# The trim matters more than it looks: the natural way to set this is
# `="$(cat …)"` or `="$(op read …)"`, and a trailing newline from either would
# fail the hex check for a key that is perfectly good.
#
# The key must be distinct from every other agent's. This one account is
# registered as BOTH owner and operator, and x/agent binds an operator address
# to at most one agent — so a key shared between two agents does not merely
# collide, it makes the second registration impossible. With the agents in
# separate repos nothing can check that here; the per-agent config dir is what
# makes sharing one hard to do by accident.
resolve_owner_key() {
  [[ -n "$owner_key" ]] || return 0
  # Strip surrounding whitespace, including a trailing newline.
  owner_key="$(printf '%s' "$owner_key" | tr -d '[:space:]')"
  validate_hex_key "$owner_key"
}

# resolve_remote_install_dir — expand a leading ~ in $install_dir to the
# remote $HOME (docker bind-mounts need absolute host paths).
resolve_remote_install_dir() {
  case "$install_dir" in
    "~"|"~/"*)
      [[ "$dry_run" == "1" ]] && return 0
      local home
      home="$($ssh_cmd "$host" 'printf %s "$HOME"')" \
        || fail "could not resolve remote \$HOME on $host"
      [[ -n "$home" ]] || fail "remote \$HOME is empty on $host"
      install_dir="${home}${install_dir#\~}"
      ;;
  esac
}

run_or_print() {
  if [[ "$dry_run" == "1" ]]; then
    printf "  [dry-run] %s\n" "$*"
  else
    eval "$@"
  fi
}

remote_exec() {
  run_or_print "$ssh_cmd '$host' $(printf '%q ' "$@")"
}

remote_image_id() {
  local img="$1"
  if [[ "$dry_run" == "1" ]]; then
    echo ""
    return
  fi
  $ssh_cmd "$host" "docker image inspect --format '{{.Id}}' $img 2>/dev/null || true"
}

local_image_id() {
  docker image inspect --format '{{.Id}}' "$1" 2>/dev/null || true
}

# save_if_changed IMG TAR — docker save IMG to TAR, skipped when TAR.id
# already matches the current image id.
save_if_changed() {
  local img="$1" tar="$2" id
  if [[ "$dry_run" == "1" ]]; then
    info "[dry-run] would docker save $img → $(basename "$tar") (if image id changed)"
    run_or_print "docker save -o '$tar' '$img'"
    return 0
  fi
  id="$(local_image_id "$img")"
  [[ -n "$id" ]] || fail "image $img not found locally; build failed?"
  if [[ -f "$tar" && -f "${tar}.id" && "$(cat "${tar}.id")" == "$id" ]]; then
    info "$img unchanged — skipping save"
    return 0
  fi
  info "$img → $(basename "$tar")"
  run_or_print "docker save -o '$tar' '$img'"
  echo "$id" > "${tar}.id"
}

# load_if_missing IMG REMOTE_TAR EXPECTED_ID — docker load on the remote only
# when the remote doesn't already have IMG at EXPECTED_ID.
load_if_missing() {
  local img="$1" remote_tar="$2" expected_id="$3"
  local remote_id; remote_id="$(remote_image_id "$img")"
  if [[ "$remote_id" == "$expected_id" && -n "$expected_id" ]]; then
    info "$img already loaded on remote — skipping load"
    return 0
  fi
  remote_exec "docker load < $remote_tar"
}

# render_nginx_conf — this agent's location block for the reverse proxy.
#
# This agent owns the host it advertises and hangs off its root, so the block
# is a plain `location /` to its own local port. AGENT_PORT is the only fact
# shared with the rendered config and the listener, so a route printed here
# cannot disagree with what deployed.
#
# Nothing installs this. The server block it belongs in owns TLS and the host
# name, which are outside this repo; two scripts racing to edit one nginx file
# is how you get a half-written config on reload. Print it, review it, paste it.
render_nginx_conf() {
  cat <<EOF
# ${AGENT_NAME} — generated by scripts/deploy.sh --print-nginx
# Paste into the server block for $(printf '%s' "${public_url#*://}"), then
# \`nginx -t && systemctl reload nginx\`.

location / {
    # No prefix to strip: the agent binds at root and serves
    # /.well-known/agent-card.json and /invoke there, which is exactly the
    # shape of the public_url it advertises inside the card.
    proxy_pass http://127.0.0.1:${AGENT_PORT};

    proxy_set_header Host              \$host;
    proxy_set_header X-Real-IP         \$remote_addr;
    proxy_set_header X-Forwarded-For   \$proxy_add_x_forwarded_for;
    proxy_set_header X-Forwarded-Proto \$scheme;

    # A2A streams task updates over SSE. Without HTTP/1.1 and unbuffered
    # proxying nginx holds the events until the response ends, which turns a
    # stream into one delivery at the end and looks like a hung agent.
    proxy_http_version 1.1;
    proxy_set_header Connection "";
    proxy_buffering off;
    proxy_cache off;
    proxy_read_timeout 300s;
}
EOF
}

# ---- mode: init-config ----------------------------------------------------
#
# The three-step setup this replaces (mkdir, cp, chmod) had a delayed failure
# mode: forget the chmod and nothing complains until the NEXT deploy refuses to
# source a group-writable file. Doing it in one step removes the window.
#
# Runs before require_install_args on purpose — bootstrapping a config needs
# no host, which is the whole point of it.
if [[ "$mode" == "init-config" ]]; then
  src="${SCRIPT_DIR}/config.sh.example"
  dst="${config_dir}/config.sh"
  [[ -f "$src" ]] || fail "template not found: ${src} (running a copy of the script outside its repo?)"
  # An explicit if, not `[[ -e … ]] && fail`: when the file does NOT exist the
  # && chain evaluates false, and as the last command under `set -e` that would
  # exit 1 with no message — refusing to bootstrap precisely when it should.
  if [[ -e "$dst" ]]; then
    fail "refusing to overwrite ${dst} — delete it first if you meant to start over"
  fi
  mkdir -p "$config_dir" || fail "could not create ${config_dir}"
  # install(1) sets the mode as it writes, so the file is never briefly 0644.
  install -m 600 "$src" "$dst" || fail "could not write ${dst}"
  step "Wrote ${dst} (mode 600)"
  info "Edit it — at minimum SVPCHAIN_DEPLOY_HOST and SVPCHAIN_PERPS_AGENT_PUBLIC_URL —"
  info "then run ./scripts/deploy.sh"
  info "To register this agent on chain it also needs an owner key:"
  info "  ./scripts/deploy.sh --gen-owner-key"
  exit 0
fi

# ---- mode: gen-owner-key --------------------------------------------------
#
# Mint the key this agent registers under and point the config file at it. One
# step, on purpose: a key generated and not referenced leaves --register saying
# it has none, while a config line naming a key that was never generated fails
# the *source* and takes every other mode down with it.
#
# The key material never passes through this script. cmd/owner-keygen creates
# the file itself with O_EXCL at 0600 and prints only the derived address, so
# the secret is never in a shell variable, in argv, or in `set -x` output. What
# comes back is the one thing needed next: the address to fund.
#
# Every refusal below is about the same fact. This account is registered as
# BOTH owner and operator, so it is simultaneously the agent's on-chain id and
# the account holding its bond — replacing one strands a registration and its
# bond with nothing on either side reporting a fault. There is deliberately no
# --force: an operator who really means to start over deletes the file, which
# is harder to do by accident than passing a flag.
#
# Runs before require_install_args for the same reason init-config does:
# bootstrapping an identity needs no host.
if [[ "$mode" == "gen-owner-key" ]]; then
  key_file="${config_dir}/owner.key"
  config_file="${config_dir}/config.sh"

  # --no-config asks the script to ignore the file this mode's whole second
  # half writes to, so there is no coherent thing to do.
  [[ "$use_config" == "1" ]] \
    || fail "--gen-owner-key wires up the config file, so it cannot run with --no-config"
  require_cmd go

  # Already keyed, from whichever layer — --print-env names it. Includes the
  # case where the key came from the environment for this one invocation, which
  # is still an identity this agent may be registered under.
  if [[ -n "$owner_key" ]]; then
    fail "an owner key is already configured (--print-env says from where) — generating another would be a second identity, not a replacement"
  fi
  if [[ -e "$key_file" ]]; then
    fail "refusing to overwrite ${key_file} — that key may already hold this agent's registration and its bond; move it aside first if you truly mean to start over"
  fi
  if [[ ! -f "$config_file" ]]; then
    fail "no config file at ${config_file} — run ./scripts/deploy.sh --init-config first"
  fi
  # Catches what the $owner_key check above cannot: a live assignment whose
  # command substitution resolved to nothing (an `op read` against a vault that
  # is not unlocked, say). Rewriting that line would throw away the operator's
  # own key source.
  if grep -q '^SVPCHAIN_PERPS_AGENT_OWNER_KEY=' "$config_file"; then
    fail "${config_file} already assigns SVPCHAIN_PERPS_AGENT_OWNER_KEY (it resolved to nothing — a locked vault?); fix or remove that line first"
  fi

  mkdir -p "$config_dir" || fail "could not create ${config_dir}"
  repo_dir="$(cd "${SCRIPT_DIR}/.." && pwd)"
  # GOWORK=off matches the Makefile: a go.work in the parent directory would
  # resolve this module from sibling checkouts rather than the pinned versions.
  # stdout is the address and nothing else; the key went to the file.
  owner_addr="$(cd "$repo_dir" && GOWORK=off go run ./cmd/owner-keygen -out "$key_file")" \
    || fail "key generation failed; ${key_file} was not written"

  # Rewrite rather than append, so a second run cannot leave two assignments
  # with the last one silently winning. The pattern is deliberately tight —
  # an optional '#' immediately followed by the name — because the template
  # carries indented `#   SVPCHAIN_…_OWNER_KEY="$(op read …)"` lines as
  # documentation, and rewriting one of those would eat the docs and leave the
  # real line untouched.
  key_line="SVPCHAIN_PERPS_AGENT_OWNER_KEY=\"\$(cat \"${key_file}\")\""
  tmp_config="${config_file}.gen.$$"
  (
    umask 077
    awk -v line="$key_line" '
      /^#?SVPCHAIN_PERPS_AGENT_OWNER_KEY=/ && !seen { print line; seen = 1; next }
      { print }
      END { if (!seen) { print ""; print line } }
    ' "$config_file" > "$tmp_config"
  ) || { rm -f "$tmp_config"; fail "could not rewrite ${config_file}"; }
  # mv rather than an in-place edit: the config file is never a half-written
  # file that the next deploy would source.
  mv "$tmp_config" "$config_file" || { rm -f "$tmp_config"; fail "could not replace ${config_file}"; }
  chmod 600 "$config_file"

  step "Owner key created"
  pass "key     ${key_file} (mode 600)"
  pass "address ${owner_addr}"
  pass "config  ${config_file} now reads the key from that file"
  info "Back up the key file. It is this agent's on-chain identity AND the"
  info "account holding its bond: lose it and the registration and the bond are"
  info "unreachable, and a new key is a different agent rather than a recovery."
  info "It stays on this machine — the deploy never ships it to the host."
  info "Next: fund ${owner_addr} with the registration fee, the bond and gas,"
  info "deploy, then ./scripts/deploy.sh --register"
  exit 0
fi

# ---- mode: register -------------------------------------------------------
#
# Put the deployed agent on chain, or bring an already-registered one back in
# line with what it now serves.
#
# This cannot be a purely local operation. What gets published is the sha256 of
# the agent card as SERVED, so the thing being registered has to be a running
# agent answering at a URL — cmd/agent-register fetches the card from
# $public_url and hashes exactly those bytes.
#
# It runs against $public_url deliberately, not over the ssh connection. That
# URL is what goes into the registration and what a verifier will fetch, so a
# registration that succeeds through it has proven the route as a side effect.
# A host that DNS or nginx does not point here fails at this step instead of
# registering an endpoint that 404s.
#
# The SIGNING is local. There is no key on the remote, so the owner key here
# signs MsgRegisterAgent directly and the agent's only role is serving the
# bytes that get hashed. The key travels in the child process's environment
# rather than in argv, where `ps` would show it.
if [[ "$mode" == "register" ]]; then
  require_cmd go
  resolve_owner_key
  [[ -n "$owner_key" ]] \
    || fail "no owner key configured — registration is the owner proving it holds the key this agent is registered under (see --gen-owner-key)"
  # How the registry chain is reached: REST when --agent-chain-rest is set,
  # else gRPC. Which chain it is no longer varies by route — there is one, and
  # --agent-chain-id names it.
  register_chain_id="$agent_chain_id"
  [[ -n "$agent_chain_rest" ]] \
    || fail "--agent-chain-rest is required to register: it is the registry chain's REST API as reachable from THIS machine, where the transaction is signed. Tunnel it first if only the deploy host can see the node: ssh -N -L 1317:127.0.0.1:1317 <host>"
  chain_args=(-rest "$agent_chain_rest")
  route="REST ${agent_chain_rest}"
  [[ -n "$register_chain_id" ]] || fail "--agent-chain-id is required to register: it names the chain carrying x/agent, and the signature commits to it"

  repo_dir="$(cd "${SCRIPT_DIR}/.." && pwd)"
  step "Registering ${AGENT_NAME} at ${public_url} on ${register_chain_id} via ${route}"
  # A subshell so the export and the cd die with it. GOWORK=off matches the
  # Makefile: a go.work in the parent directory would resolve this module from
  # sibling checkouts rather than the versions go.mod pins.
  (
    cd "$repo_dir" || exit 1
    export SVPCHAIN_PERPS_AGENT_OWNER_KEY="$owner_key"
    args=(
      -url "$public_url"
      -chain-id "$register_chain_id"
      "${chain_args[@]}"
      -capabilities "$operator_capabilities"
      -price-amount "$price_amount"
      -price-unit "$price_unit"
    )
    if [[ -n "$register_bond" ]]; then    args+=(-bond "$register_bond");        fi
    if [[ -n "$operator_metadata" ]]; then args+=(-metadata "$operator_metadata"); fi
    if [[ "$dry_run" == "1" ]]; then       args+=(-dry-run);                      fi
    GOWORK=off go run ./cmd/agent-register "${args[@]}"
  ) || fail "registration failed"
  exit 0
fi

# ---- mode: print-env ------------------------------------------------------
#
# Precedence is flag > environment > config file > default, and the config file
# is SOURCED, so a value can be computed rather than written. That combination
# makes "why is it deploying there" genuinely hard to answer by reading. This
# prints the resolved value of every setting next to where it came from.
#
# The owner key is reported as set/unset with a length, never echoed: the main
# reason to reach for this mode after configuring a key is to confirm the
# config file computed one, and that must not require printing a secret.
if [[ "$mode" == "print-env" ]]; then
  # Name → the local variable holding the resolved value. Parallel arrays
  # rather than an associative array, because macOS still ships bash 3.2.
  env_names=(
    SVPCHAIN_CONFIG_DIR SVPCHAIN_DEPLOY_HOST SVPCHAIN_DEPLOY_JUMP_BOX SVPCHAIN_AGENT_CHAIN_ID
    SVPCHAIN_PERPS_AGENT_PUBLIC_URL
    SVPCHAIN_PERPS_AGENT_OWNER_KEY
    SVPCHAIN_AGENT_CHAIN_REST
    SVPCHAIN_OPERATOR_CAPABILITIES SVPCHAIN_OPERATOR_METADATA
    SVPCHAIN_OPERATOR_PRICE_AMOUNT SVPCHAIN_OPERATOR_PRICE_UNIT
    SVPCHAIN_MCP_ENDPOINT SVPCHAIN_ANTHROPIC_API_KEY SVPCHAIN_OPENAI_API_KEY
    SVPCHAIN_ASSISTANT_PROVIDER SVPCHAIN_ASSISTANT_BASE_URL SVPCHAIN_ASSISTANT_MODEL
    SVPCHAIN_INSTALL_DIR
  )
  env_values=(
    "$config_dir" "$host" "$jump_box" "$agent_chain_id"
    "$public_url"
    "$owner_key"
    "$agent_chain_rest"
    "$operator_capabilities" "$operator_metadata"
    "$price_amount" "$price_unit"
    "$mcp_endpoint" "$anthropic_api_key" "$openai_api_key"
    "$assistant_provider" "$assistant_base_url" "$assistant_model"
    "$install_dir"
  )

  if [[ "$use_config" == "1" && -f "${config_dir}/config.sh" ]]; then
    printf 'config file: %s\n\n' "${config_dir}/config.sh"
  elif [[ "$use_config" == "1" ]]; then
    printf 'config file: %s (not present)\n\n' "${config_dir}/config.sh"
  else
    printf 'config file: ignored (--no-config)\n\n'
  fi

  for _i in "${!env_names[@]}"; do
    name="${env_names[$_i]}"
    value="${env_values[$_i]}"

    if was_flag "$name";       then origin="flag"
    elif was_preset "$name";   then origin="environment"
    elif [[ -n "${!name:-}" ]]; then origin="config file"
    else                            origin="default"
    fi

    # Never print the key. A length is enough to tell "computed correctly"
    # from "the command substitution returned nothing". Trimmed but NOT
    # validated: a malformed key should still be diagnosable here rather than
    # aborting the one mode you would reach for to diagnose it.
    case "$name" in SVPCHAIN_PERPS_AGENT_OWNER_KEY|SVPCHAIN_ANTHROPIC_API_KEY|SVPCHAIN_OPENAI_API_KEY) _secret=1 ;; *) _secret=0 ;; esac
    if [[ "$_secret" == "1" ]]; then
      value="$(printf '%s' "$value" | tr -d '[:space:]')"
      if [[ -n "$value" ]]; then value="set (${#value} chars)"; else value="unset"; fi
    fi
    [[ -n "$value" ]] || value="(empty)"

    printf '%-36s %-14s %s\n' "$name" "$origin" "$value"
  done
  exit 0
fi

# ---- mode: print-config ---------------------------------------------------

if [[ "$mode" == "print-config" ]]; then
  # Preview the agent.toml this deploy would ship.
  render_agent_toml
  exit 0
fi

# ---- mode: print-compose --------------------------------------------------

if [[ "$mode" == "print-compose" ]]; then
  # Preview the docker-compose.yml. Uses a placeholder install_dir/image when
  # not resolved.
  image_ref="${IMAGE_REPO}:${image_tag:-<tag>}"
  render_compose_yaml
  exit 0
fi

# ---- mode: print-nginx ----------------------------------------------------

if [[ "$mode" == "print-nginx" ]]; then
  render_nginx_conf
  exit 0
fi

# ---- mode: uninstall ------------------------------------------------------

if [[ "$mode" == "uninstall" ]]; then
  [[ -n "$host" ]] || fail "--host is required (or set SVPCHAIN_DEPLOY_HOST)"
  step "svpchain agents uninstall on $host"
  resolve_remote_install_dir
  remote_exec "docker compose -f $install_dir/docker-compose.yml down 2>/dev/null || true"
  # Belt-and-braces: remove the container by name in case the compose file is
  # gone, then the image, then the install dir.
  remote_exec "docker rm -f $AGENT_NAME 2>/dev/null || true"
  remote_exec "sh -c 'docker images --format \"{{.Repository}}:{{.Tag}}\" $IMAGE_REPO 2>/dev/null | xargs -r docker rmi 2>/dev/null || true'"
  remote_exec "rm -rf $install_dir"
  step "Done"
  exit 0
fi

# ---- mode: install --------------------------------------------------------

require_install_args
require_cmd docker
require_cmd rsync
require_cmd ssh
require_cmd go

REPO_DIR="$(cd "${SCRIPT_DIR}/.." && pwd)"
cd "$REPO_DIR"

if [[ -z "$image_tag" ]]; then
  if image_tag="$(git rev-parse --short HEAD 2>/dev/null)"; then :
  else image_tag="dev"; fi
fi
image_ref="${IMAGE_REPO}:${image_tag}"
image_tar="${REPO_DIR}/build/${AGENT_NAME}.image.tar"
mkdir -p "${REPO_DIR}/build"

step "Preflight (local + remote)"
info "host=$host${jump_box:+ via jump-box=$jump_box} image=$image_ref platform=$platform"
info "install_dir=$install_dir public_url=$public_url"
if [[ "$dry_run" != "1" ]]; then
  # Two separate checks so a network failure is not reported as a docker one.
  ssh_err="$($ssh_cmd -o ConnectTimeout=15 "$host" true 2>&1 >/dev/null)" \
    || fail "cannot ssh to $host${jump_box:+ via $jump_box}: ${ssh_err:-unknown error} (network/VPN/firewall allowlist? ssh keys ok?)"
  $ssh_cmd "$host" "docker version --format '{{.Server.Version}}'" \
    >/dev/null 2>&1 \
    || fail "ssh to $host works but docker is not usable without sudo (docker installed? daemon running? ssh user in the docker group?)"
  $ssh_cmd "$host" "docker compose version" >/dev/null 2>&1 \
    || fail "remote 'docker compose' (v2 plugin) not available at $host"
  pass "remote docker + compose reachable"
else
  info "[dry-run] skipping ssh-to-docker reachability check"
fi

resolve_remote_install_dir
info "install_dir=$install_dir"

# Phase 1: build (local)
step "Local: docker build --platform $platform"
if [[ "$skip_build" == "1" ]]; then
  info "--skip-build: reusing existing local image $image_ref"
  [[ -n "$(local_image_id "$image_ref")" ]] || fail "image $image_ref not found locally; drop --skip-build"
else
  # Vendored build (see cmd/svpchain-perps-agent/Dockerfile): the go.mod
  # replace to ../svpagent/protocol resolves on the deploy machine, and the vendored
  # tree makes the Docker context self-contained.
  run_or_print "go mod vendor"
  build_cmd="docker build --platform $platform"
  build_cmd+=" --build-arg VERSION=$image_tag"
  build_cmd+=" --build-arg COMMIT=$(git rev-parse HEAD 2>/dev/null || echo unknown)"
  build_cmd+=" -t $image_ref"
  build_cmd+=" -t ${IMAGE_REPO}:latest"
  build_cmd+=" -f cmd/${AGENT_NAME}/Dockerfile ."
  run_or_print "$build_cmd"
fi

# Phase 2: save (local)
step "Local: docker save (cached by image id)"
save_if_changed "$image_ref" "$image_tar"
expected_id="$(cat "${image_tar}.id" 2>/dev/null || echo "")"

# Phase 3: ship config + compose + the image tar (local → remote)
step "Local → remote: rsync configs + image tar to $install_dir"

# One staging directory, one rsync: everything the remote needs beside the
# image is rendered here first, so the transfer is a single round trip.
#
# The modes are deliberate. Both files shipped from mktemp (0600) before this,
# so pin the whole directory to match rather than let the umask quietly relax
# them to 0644 on every deployed host — rsync -a carrying the staged mode is
# the only portable way, since macOS's openrsync rejects --chmod=F600. 0755
# on the directory itself keeps $install_dir at the mode `mkdir -p` gave it:
# with a trailing slash on the source, rsync applies the source root's
# attributes to the destination root.
stage_dir="$(mktemp -d -t "${AGENT_NAME}.stage.XXXXXX")"
trap 'rm -rf "$stage_dir"' EXIT

render_agent_toml   > "$stage_dir/agent.toml"
render_compose_yaml > "$stage_dir/docker-compose.yml"
if [[ -n "${anthropic_api_key}${openai_api_key}" ]]; then
  render_assistant_env > "$stage_dir/assistant.env"
fi
chmod 600 "$stage_dir"/*
chmod 755 "$stage_dir"

remote_exec "mkdir -p $install_dir $install_dir/data"
# ★ rsync runs without --delete, so a key removed from the config would leave
# the old assistant.env in place and the skill quietly still enabled. Clearing
# it here is what makes unsetting the key turn the assistant off.
if [[ -z "${anthropic_api_key}${openai_api_key}" ]]; then
  remote_exec "rm -f $install_dir/assistant.env"
fi
# The trailing slash on the source is load-bearing: without it rsync creates
# $install_dir/<staging-dir-name>/ and the agent keeps running against its old
# agent.toml, with nothing anywhere reporting an error.
run_or_print "rsync -avz -e '$ssh_cmd' '$stage_dir/' '$host:$install_dir/'"
# The image tar ships separately: save_if_changed keys its skip on the
# ${image_tar}.id sidecar in build/, so folding a multi-hundred-MB file into
# the staging dir would mean copying it on every run.
run_or_print "rsync -avz -e '$ssh_cmd' '$image_tar' '$host:$install_dir/${AGENT_NAME}.image.tar'"

# Phase 4: load (On remote)
step "On remote: docker load (skipped if image already loaded)"
load_if_missing "$image_ref" "$install_dir/${AGENT_NAME}.image.tar" "$expected_id"
remote_exec "docker tag $image_ref ${IMAGE_REPO}:latest"

# Phase 5: run (On remote)
step "On remote: docker compose up -d"
# The explicit rm first: compose will not recreate a container it considers
# up-to-date, so a config-only change to a mounted file would otherwise leave
# the old process running.
remote_exec "docker rm -f $AGENT_NAME 2>/dev/null || true"
remote_exec "docker compose -f $install_dir/docker-compose.yml up -d"

# Phase 6: verify (local) — smoke-test over loopback on the remote.
step "On remote: smoke test (healthz + agent card over loopback via ssh)"
if [[ "$dry_run" == "1" ]]; then
  info "[dry-run] would ssh $host curl -> http://127.0.0.1:${AGENT_PORT}/healthz + agent card"
else
  # The agent dials the chain gRPC and finishes an initial markets-cache
  # refresh before serving; give it a few seconds to come up.
  healthy=""
  for _ in 1 2 3 4 5 6 7 8 9 10; do
    code=$($ssh_cmd "$host" \
      "curl -sS -o /dev/null -w '%{http_code}' --max-time 5 http://127.0.0.1:${AGENT_PORT}/healthz" \
      2>/dev/null || echo 000)
    if [[ "$code" == "200" ]]; then healthy="1"; break; fi
    sleep 2
  done
  if [[ -z "$healthy" ]]; then
    info "healthz on :${AGENT_PORT} did not answer 200. Check logs with:"
    info "  ssh${jump_box:+ -J $jump_box} $host 'docker logs $AGENT_NAME --tail=80'"
    info "Common cause: the gRPC/RPC endpoints in agent.toml are not reachable"
    info "from inside the container."
    fail "smoke test failed for $AGENT_NAME"
  fi
  skills=$($ssh_cmd "$host" \
    "curl -sS --max-time 5 http://127.0.0.1:${AGENT_PORT}/.well-known/agent-card.json" \
    2>/dev/null | { command -v jq >/dev/null 2>&1 && jq -r '.skills | length' || cat; } || echo "")
  if [[ "$skills" =~ ^[0-9]+$ ]]; then
    pass "$AGENT_NAME :${AGENT_PORT} — /healthz 200, card served ($skills skills)"
  else
    # jq may be missing locally — the card body already proves the
    # endpoint answers; don't fail the deploy over the count.
    pass "$AGENT_NAME :${AGENT_PORT} — /healthz 200, card fetched (skill count unverified)"
  fi
fi

step "Done — $AGENT_NAME $image_tag running on $host (:${AGENT_PORT}, advertised at $public_url)"

# Deploying does not touch the chain, and a card change that never reaches the
# registry is the failure this line exists to prevent: verifiers recompute the
# capability hash from a live fetch, so an agent serving a card that no longer
# matches its registration reads as unverified with every process healthy.
info "If the card or the public URL changed, publish it:"
info "  ./scripts/deploy.sh --register    (also does the first registration)"
