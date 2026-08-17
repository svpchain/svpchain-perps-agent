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
# leaves the operator) → docker save (cached by image id) → rsync one staging
# dir (agent.toml, docker-compose.yml, operator.key at 0600 when given) plus
# the image tar to ~/svpchain-perps-agent → docker load → docker compose up -d
# → smoke-test /healthz and the agent card over loopback.
#
# The operator key turns delegated execution on. It must be DISTINCT from every
# other agent's: an agent's on-chain id derives from its key and
# agent_self_register publishes a hash of this binary's own card, so a shared
# key makes two agents collide on one registry record. With the agents in
# separate repos nothing here can check that; it is an operational rule.
#
# The remote needs only docker + the compose v2 plugin reachable by the ssh
# user without sudo. Auth state is in-memory, so a redeploy wipes it; the
# transfer-out caps persist on the data volume.
#
# Config file (so a routine install needs no flags at all):
#   ~/.config/svpchain-perps-agent/config.sh
#
#   A shell file setting the SVPCHAIN_* variables named below. The directory is
#   this agent's, not the project's, so each agent keeps its own — which is what
#   keeps their operator keys distinct.
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
# Chain endpoints:
#   --chain-id <id>                SVPCHAIN_CHAIN_ID     (svp-2517-1)
#   --grpc-addr <host:port>        SVPCHAIN_GRPC_ADDR    (127.0.0.1:9090)
#   --comet-rpc <url>              SVPCHAIN_COMET_RPC    (http://127.0.0.1:26657)
#   --indexer <url>                SVPCHAIN_INDEXER      (http://127.0.0.1:3002)
#   --agent-chain-id <id>          SVPCHAIN_AGENT_CHAIN_ID
#   --agent-chain-rest <url>       SVPCHAIN_AGENT_CHAIN_REST
#                                  Optional separate x/agent + x/agentwallet
#                                  chain over its Cosmos REST API. Both or
#                                  neither; unset, those families run against
#                                  the DEX chain connection.
#
# Identity and execution:
#   --public-url <url>             The URL this agent advertises, used verbatim.
#                                  SVPCHAIN_PERPS_AGENT_PUBLIC_URL
#
#   SVPCHAIN_PERPS_AGENT_OPERATOR_KEY
#                                  The hex eth_secp256k1 operator key ITSELF, not
#                                  a path — there is no flag for it, because a key
#                                  on the command line lands in `ps` and in your
#                                  shell history. Set it in the config file, which
#                                  is sourced and can therefore compute it:
#                                    SVPCHAIN_PERPS_AGENT_OPERATOR_KEY="$(op read …)"
#                                  Unset → keyless, and the execution skills refuse
#                                  with a reason. Set, it ships to the remote as a
#                                  docker compose SECRET mounted at
#                                  /run/secrets/operator_key — never as a container
#                                  environment variable, which `docker inspect` and
#                                  /proc/<pid>/environ would both expose.
#   --operator-capabilities <csv>  Default "perps.execution,perps.trading".
#                                  SVPCHAIN_OPERATOR_CAPABILITIES
#   --operator-metadata <text>     SVPCHAIN_OPERATOR_METADATA
#
# Optional families and tuning:
#   --markets-refresh <dur>        Default 30s.  SVPCHAIN_MARKETS_REFRESH
#   --deposit-max-usdc <n>         Caps on funds movements, in human USDC;
#   --withdraw-max-usdc <n>        unset → no cap.
#   --transfer-max-usdc <n>        SVPCHAIN_DEPOSIT_MAX_USDC,
#   --daily-withdraw-cap-usdc <n>  SVPCHAIN_WITHDRAW_MAX_USDC,
#                                  SVPCHAIN_TRANSFER_MAX_USDC,
#                                  SVPCHAIN_DAILY_WITHDRAW_CAP_USDC
#
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
#   --print-env                    Show every setting, its resolved value and
#                                  where it came from. The operator key prints as
#                                  "set"/"unset", never its value.
#   --print-config / --print-compose / --print-nginx
#   --dry-run / --uninstall
#
# Examples:
#   ./scripts/deploy.sh --init-config       # then edit the file it names
#   ./scripts/deploy.sh                     # a configured install takes no flags
#   ./scripts/deploy.sh --host www@svpdev1.example.com \
#     --public-url https://perps-agent.svpchain.org
#   ./scripts/deploy.sh --uninstall --host www@svpdev1.example.com
#
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=lib/common.sh
source "${SCRIPT_DIR}/lib/common.sh"

fail() { printf "  ${C_RED}✗${C_RESET} %s\n" "$*" >&2; exit 1; }

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

# The operator key travels as a docker compose secret rather than a bind mount
# or a container environment variable. Compose mounts a secret at
# /run/secrets/<name>, and unlike `environment:` it stays out of
# `docker inspect` and /proc/<pid>/environ. Stated once here because the name
# lands in three places — the service's secrets list, the top-level secrets
# block, and the key_file path in agent.toml — and the agent boots keyless,
# with nothing in the logs, if they disagree.
readonly SECRET_NAME="operator_key"
readonly SECRET_MOUNT_PATH="/run/secrets/${SECRET_NAME}"
# Staged and shipped under this name; the top-level secrets block points here.
readonly SECRET_FILE="operator.key"

# ---- config file -----------------------------------------------------------
#
# Every setting below can come from a sourced shell file, so a routine install
# is `./scripts/deploy.sh` rather than a dozen flags:
#
#   ~/.config/<agent-name>/config.sh
#
# The directory is named after this agent, not after the project, so each agent
# in the fleet carries its own. That is what keeps the operator keys apart:
# an agent's on-chain id derives from its key, so two agents sharing one would
# collide on a single registry record — and a directory per agent makes that
# impossible to do by accident rather than merely discouraged.
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
  SVPCHAIN_DEPLOY_HOST SVPCHAIN_CHAIN_ID SVPCHAIN_GRPC_ADDR SVPCHAIN_COMET_RPC
  SVPCHAIN_INDEXER SVPCHAIN_AGENT_CHAIN_ID SVPCHAIN_AGENT_CHAIN_REST
  SVPCHAIN_PERPS_AGENT_PUBLIC_URL SVPCHAIN_PERPS_AGENT_OPERATOR_KEY
  SVPCHAIN_OPERATOR_CAPABILITIES SVPCHAIN_OPERATOR_METADATA SVPCHAIN_INSTALL_DIR
  SVPCHAIN_MARKETS_REFRESH SVPCHAIN_DEPOSIT_MAX_USDC
  SVPCHAIN_WITHDRAW_MAX_USDC SVPCHAIN_TRANSFER_MAX_USDC
  SVPCHAIN_DAILY_WITHDRAW_CAP_USDC
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

# ---- args ------------------------------------------------------------------

mode="install"        # install | uninstall | init-config | print-env
                      #         | print-config | print-compose | print-nginx

# Settings a flag overrode, so --print-env can say so. Same space-padded-string
# trick as ENV_PRESET, for the same bash 3.2 reason.
FLAG_SET=" "
mark_flag() { FLAG_SET+="$1 "; }
was_flag()  { [[ "$FLAG_SET" == *" $1 "* ]]; }

host=""
chain_id="${SVPCHAIN_CHAIN_ID:-svp-2517-1}"
grpc_addr="${SVPCHAIN_GRPC_ADDR:-127.0.0.1:9090}"
comet_rpc="${SVPCHAIN_COMET_RPC:-http://127.0.0.1:26657}"
indexer="${SVPCHAIN_INDEXER:-http://127.0.0.1:3002}"
agent_chain_id="${SVPCHAIN_AGENT_CHAIN_ID:-}"
agent_chain_rest="${SVPCHAIN_AGENT_CHAIN_REST:-}"
public_url="${SVPCHAIN_PERPS_AGENT_PUBLIC_URL:-https://agent-testnet.svpchain.org}"
# The operator key MATERIAL, not a path. There is deliberately no flag for it:
# a hex key in argv is visible in `ps` and lands in shell history. The config
# file is sourced, so it can compute the value instead of storing it.
operator_key="${SVPCHAIN_PERPS_AGENT_OPERATOR_KEY:-}"
operator_capabilities="${SVPCHAIN_OPERATOR_CAPABILITIES:-perps.execution,perps.trading}"
operator_metadata="${SVPCHAIN_OPERATOR_METADATA:-}"
install_dir="${SVPCHAIN_INSTALL_DIR:-~/svpchain-perps-agent}"
image_tag=""
platform="linux/amd64"
deposit_max="${SVPCHAIN_DEPOSIT_MAX_USDC:-}"
withdraw_max="${SVPCHAIN_WITHDRAW_MAX_USDC:-}"
transfer_max="${SVPCHAIN_TRANSFER_MAX_USDC:-}"
daily_withdraw_cap="${SVPCHAIN_DAILY_WITHDRAW_CAP_USDC:-}"
markets_refresh="${SVPCHAIN_MARKETS_REFRESH:-30s}"
skip_build="0"
dry_run="0"

while [[ $# -gt 0 ]]; do
  case "$1" in
    --host)                   host="$2"; mark_flag SVPCHAIN_DEPLOY_HOST;              shift 2 ;;
    --chain-id)               chain_id="$2"; mark_flag SVPCHAIN_CHAIN_ID;          shift 2 ;;
    --grpc-addr)              grpc_addr="$2"; mark_flag SVPCHAIN_GRPC_ADDR;         shift 2 ;;
    --comet-rpc)              comet_rpc="$2"; mark_flag SVPCHAIN_COMET_RPC;         shift 2 ;;
    --indexer)                indexer="$2"; mark_flag SVPCHAIN_INDEXER;           shift 2 ;;
    --agent-chain-id)         agent_chain_id="$2"; mark_flag SVPCHAIN_AGENT_CHAIN_ID;    shift 2 ;;
    --agent-chain-rest)       agent_chain_rest="$2"; mark_flag SVPCHAIN_AGENT_CHAIN_REST;  shift 2 ;;
    --public-url)             public_url="$2"; mark_flag SVPCHAIN_PERPS_AGENT_PUBLIC_URL;  shift 2 ;;
    --operator-capabilities)  operator_capabilities="$2"; mark_flag SVPCHAIN_OPERATOR_CAPABILITIES; shift 2 ;;
    --operator-metadata)      operator_metadata="$2"; mark_flag SVPCHAIN_OPERATOR_METADATA; shift 2 ;;
    --install-dir)            install_dir="$2"; mark_flag SVPCHAIN_INSTALL_DIR;       shift 2 ;;
    --image-tag)              image_tag="$2";         shift 2 ;;
    --platform)               platform="$2";          shift 2 ;;
    --deposit-max-usdc)       deposit_max="$2"; mark_flag SVPCHAIN_DEPOSIT_MAX_USDC;       shift 2 ;;
    --withdraw-max-usdc)      withdraw_max="$2"; mark_flag SVPCHAIN_WITHDRAW_MAX_USDC;      shift 2 ;;
    --transfer-max-usdc)      transfer_max="$2"; mark_flag SVPCHAIN_TRANSFER_MAX_USDC;      shift 2 ;;
    --daily-withdraw-cap-usdc) daily_withdraw_cap="$2"; mark_flag SVPCHAIN_DAILY_WITHDRAW_CAP_USDC; shift 2 ;;
    --markets-refresh)        markets_refresh="$2"; mark_flag SVPCHAIN_MARKETS_REFRESH;   shift 2 ;;
    # Already handled by the pre-scan above; consumed here so they are not
    # rejected as unknown.
    --config-dir)             mark_flag SVPCHAIN_CONFIG_DIR; shift 2 ;;
    --no-config)              shift ;;
    --init-config)            mode="init-config";     shift ;;
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

# Strip a trailing slash (from the flag or env) so the card's
# "<public_url>/invoke" join stays clean. Nothing else is done to it: what you
# pass is what the agent advertises, and a reverse proxy has to route exactly
# that URL here.
public_url="${public_url%/}"

# $operator_key holds the key material itself, seeded from the environment
# above. Empty means keyless — a fully supported mode here: the agent still
# advertises the execution skills and refuses them at call time with a reason.
# Normalised and validated once by resolve_operator_key, which runs before
# anything is rendered.

# ---- shared helpers -------------------------------------------------------

# emit_operator_capabilities — render the capabilities list as a TOML array.
emit_operator_capabilities() {
  local out="[" first=1 cap
  local saved_ifs="$IFS"; IFS=','
  for cap in $operator_capabilities; do
    [[ -z "$cap" ]] && continue
    [[ "$first" == "1" ]] || out+=", "
    out+="\"$cap\""
    first=0
  done
  IFS="$saved_ifs"
  out+="]"
  printf '%s' "$out"
}

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

listen_addr      = "0.0.0.0:${AGENT_PORT}"
public_url       = "${public_url}"
broadcast_mode   = "server"
EOF
  # Persist per-symbol transfer-out caps on the agent's own writable data
  # volume (the config dir holds only read-only mounts) — the path is under the
  # agent's name because that is what the compose service mounts
  # ${install_dir}/data onto. See render_compose_yaml.
  echo "transfer_out_cap_path   = \"/var/lib/${AGENT_NAME}/transfer-out-caps.json\""
  cat <<EOF

[dex_chain]
id               = "${chain_id}"
grpc_addr        = "${grpc_addr}"
comet_rpc_url    = "${comet_rpc}"
indexer_base_url = "${indexer}"
EOF
  # A separate chain carrying x/agent + x/agentwallet, reached over its
  # Cosmos REST API; unset, the agent-identity families run against the DEX
  # chain connection.
  if [[ -n "$agent_chain_id" || -n "$agent_chain_rest" ]]; then
    [[ -n "$agent_chain_id" && -n "$agent_chain_rest" ]] || \
      fail "--agent-chain-id and --agent-chain-rest must be set together"
    echo ""
    echo "[agent_chain]"
    echo "id       = \"${agent_chain_id}\""
    echo "rest_url = \"${agent_chain_rest}\""
  fi
  cat <<EOF

[cache]
markets_refresh = "${markets_refresh}"
EOF
  if [[ -n "${deposit_max}${withdraw_max}${transfer_max}${daily_withdraw_cap}" ]]; then
    echo ""
    echo "[limits]"
    [[ -n "$deposit_max"        ]] && echo "deposit_max_usdc        = ${deposit_max}"
    [[ -n "$withdraw_max"       ]] && echo "withdraw_max_usdc       = ${withdraw_max}"
    [[ -n "$transfer_max"       ]] && echo "transfer_max_usdc       = ${transfer_max}"
    [[ -n "$daily_withdraw_cap" ]] && echo "daily_withdraw_cap_usdc = ${daily_withdraw_cap}"
  fi
  # The operator key turns this agent's delegated execution on. key_file points
  # at the docker compose secret mount. The path is ABSOLUTE on purpose:
  # internal/config
  # resolves it against the agent.toml directory, so it points at the file
  # mounted beside the config.
  if [[ -n "$operator_key" ]]; then
    cat <<EOF

[operator]
key_file     = "${SECRET_MOUNT_PATH}"
capabilities = $(emit_operator_capabilities)
metadata     = "${operator_metadata}"
EOF
  fi
}

# render_compose_yaml — emit the docker-compose.yml that runs this agent: the
# image, its config mount, its data volume and its TCP port. Volumes use
# absolute host paths so `docker compose up -d` works from any directory. The
# rendered config, the operator key and the data volume live flat in
# ${install_dir}; the key is mounted read-only beside the config so the
# config-dir-relative key_file = "operator.key" resolves.
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
  # An explicit if, not `[[ … ]] && echo`: a false test as the last command
  # would make the function return non-zero, and under `set -e` the
  # `render_compose_yaml > file` call site would exit the script silently.
  # A compose secret rather than a bind mount. Both end up as a read-only file
  # in the container, but the secret keeps the operator key out of the volume
  # list, which `docker inspect` prints in full.
  #
  # Note the uid/gid/mode options compose accepts on a secret are swarm-only
  # and silently ignored here; the mount inherits the source file's ownership.
  # That is fine because the image declares no USER, so the process is root and
  # can read the 0600 file rsync lands.
  if [[ -n "$operator_key" ]]; then
    cat <<EOF
    secrets:
      - ${SECRET_NAME}

secrets:
  ${SECRET_NAME}:
    file: ${install_dir}/${SECRET_FILE}
EOF
  fi
}

require_install_args() {
  [[ -n "$host" ]] || fail "--host is required (or set SVPCHAIN_DEPLOY_HOST)"
}

# validate_hex_key — the VALUE must look like a 32-byte hex operator key.
# Takes the key itself, not a path, so validation happens before the material
# is written anywhere. The error deliberately does not echo the value.
validate_hex_key() {
  [[ "$1" =~ ^(0x)?[0-9a-fA-F]{64}$ ]] \
    || fail "SVPCHAIN_PERPS_AGENT_OPERATOR_KEY does not look like a 32-byte hex key (got ${#1} characters)"
}

# resolve_operator_key — normalise and validate the key material supplied in
# SVPCHAIN_PERPS_AGENT_OPERATOR_KEY. Without one the agent runs keyless: it
# advertises execution but refuses with a reason.
#
# The trim matters more than it looks: the natural way to set this is
# `="$(cat …)"` or `="$(op read …)"`, and a trailing newline from either would
# fail the hex check for a key that is perfectly good.
#
# The key must be distinct from every other agent's — an agent's on-chain id
# derives from it and agent_self_register hashes this binary's own card, so a
# shared key makes two agents collide on one registry record. With the agents
# in separate repos nothing can check that here; it is an operational rule.
resolve_operator_key() {
  [[ -n "$operator_key" ]] || return 0
  # Strip surrounding whitespace, including a trailing newline.
  operator_key="$(printf '%s' "$operator_key" | tr -d '[:space:]')"
  validate_hex_key "$operator_key"
}

# resolve_remote_install_dir — expand a leading ~ in $install_dir to the
# remote $HOME (docker bind-mounts need absolute host paths).
resolve_remote_install_dir() {
  case "$install_dir" in
    "~"|"~/"*)
      [[ "$dry_run" == "1" ]] && return 0
      local home
      home="$(ssh -o BatchMode=yes "$host" 'printf %s "$HOME"')" \
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
  run_or_print "ssh -o BatchMode=yes '$host' $(printf '%q ' "$@")"
}

remote_image_id() {
  local img="$1"
  if [[ "$dry_run" == "1" ]]; then
    echo ""
    return
  fi
  ssh -o BatchMode=yes "$host" "docker image inspect --format '{{.Id}}' $img 2>/dev/null || true"
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
# neither a host nor a key, which is the whole point of it.
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
  exit 0
fi

# ---- mode: print-env ------------------------------------------------------
#
# Precedence is flag > environment > config file > default, and the config file
# is SOURCED, so a value can be computed rather than written. That combination
# makes "why is it deploying there" genuinely hard to answer by reading. This
# prints the resolved value of every setting next to where it came from.
#
# The operator key is reported as set/unset with a length, never echoed: the
# main reason to reach for this mode after configuring a key is to confirm the
# config file computed one, and that must not require printing a secret.
if [[ "$mode" == "print-env" ]]; then
  # Name → the local variable holding the resolved value. Parallel arrays
  # rather than an associative array, because macOS still ships bash 3.2.
  env_names=(
    SVPCHAIN_CONFIG_DIR SVPCHAIN_DEPLOY_HOST SVPCHAIN_CHAIN_ID SVPCHAIN_GRPC_ADDR
    SVPCHAIN_COMET_RPC SVPCHAIN_INDEXER SVPCHAIN_AGENT_CHAIN_ID
    SVPCHAIN_AGENT_CHAIN_REST SVPCHAIN_PERPS_AGENT_PUBLIC_URL
    SVPCHAIN_PERPS_AGENT_OPERATOR_KEY SVPCHAIN_OPERATOR_CAPABILITIES
    SVPCHAIN_OPERATOR_METADATA SVPCHAIN_MARKETS_REFRESH
    SVPCHAIN_DEPOSIT_MAX_USDC SVPCHAIN_WITHDRAW_MAX_USDC
    SVPCHAIN_TRANSFER_MAX_USDC SVPCHAIN_DAILY_WITHDRAW_CAP_USDC
    SVPCHAIN_INSTALL_DIR
  )
  env_values=(
    "$config_dir" "$host" "$chain_id" "$grpc_addr"
    "$comet_rpc" "$indexer" "$agent_chain_id"
    "$agent_chain_rest" "$public_url"
    "$operator_key" "$operator_capabilities"
    "$operator_metadata" "$markets_refresh"
    "$deposit_max" "$withdraw_max"
    "$transfer_max" "$daily_withdraw_cap"
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
    if [[ "$name" == "SVPCHAIN_PERPS_AGENT_OPERATOR_KEY" ]]; then
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
  # Preview the agent.toml this deploy would ship, [operator] block included
  # when the environment supplies a key. The key material is never in this
  # file — it ships as a separate compose secret.
  resolve_operator_key
  render_agent_toml
  exit 0
fi

# ---- mode: print-compose --------------------------------------------------

if [[ "$mode" == "print-compose" ]]; then
  # Preview the docker-compose.yml. Uses a placeholder install_dir/image when
  # not resolved, and shows the secrets block when a key is configured. The key
  # itself is not here either — the block points at the file the deploy stages.
  resolve_operator_key
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

# Normalise and validate the operator key material. With no key the agent runs
# keyless.
resolve_operator_key

REPO_DIR="$(cd "${SCRIPT_DIR}/.." && pwd)"
cd "$REPO_DIR"

if [[ -z "$image_tag" ]]; then
  if image_tag="$(git rev-parse --short HEAD 2>/dev/null)"; then :
  else image_tag="dev"; fi
fi
image_ref="${IMAGE_REPO}:${image_tag}"
image_tar="${REPO_DIR}/build/${AGENT_NAME}.image.tar"
mkdir -p "${REPO_DIR}/build"

step "Preflight (operator + remote)"
info "host=$host image=$image_ref platform=$platform"
info "install_dir=$install_dir public_url=$public_url"
if [[ -n "$operator_key" ]]; then
  info "  ${AGENT_NAME} :${AGENT_PORT} — operator key set (execution ON)"
else
  info "  ${AGENT_NAME} :${AGENT_PORT} — keyless (execution refuses with a reason)"
fi
if [[ "$dry_run" != "1" ]]; then
  ssh -o BatchMode=yes "$host" "docker version --format '{{.Server.Version}}'" \
    >/dev/null 2>&1 \
    || fail "remote docker not reachable at $host without sudo (ssh keys ok? docker installed? ssh user in the docker group?)"
  ssh -o BatchMode=yes "$host" "docker compose version" >/dev/null 2>&1 \
    || fail "remote 'docker compose' (v2 plugin) not available at $host"
  pass "remote docker + compose reachable"
else
  info "[dry-run] skipping ssh-to-docker reachability check"
fi

resolve_remote_install_dir
info "install_dir=$install_dir"

# Phase 1: build (On operator)
step "On operator: docker build --platform $platform"
if [[ "$skip_build" == "1" ]]; then
  info "--skip-build: reusing existing local image $image_ref"
  [[ -n "$(local_image_id "$image_ref")" ]] || fail "image $image_ref not found locally; drop --skip-build"
else
  # Vendored build (see cmd/svpchain-perps-agent/Dockerfile): the go.mod
  # replace to ../svpagent/protocol resolves on the operator, and the vendored
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

# Phase 2: save (On operator)
step "On operator: docker save (cached by image id)"
save_if_changed "$image_ref" "$image_tar"
expected_id="$(cat "${image_tar}.id" 2>/dev/null || echo "")"

# Phase 3: ship config + compose + the image tar (operator → remote)
step "On operator → remote: rsync configs + image tar to $install_dir"

# One staging directory, one rsync: everything the remote needs beside the
# image is rendered here first, so the transfer is a single round trip.
#
# The modes are deliberate. The operator key is a secret and must land 0600,
# and rsync -a carrying the staged mode is the only portable way to get it
# there — macOS's openrsync rejects --chmod=F600. The other two files shipped
# from mktemp (0600) before this, so pin the whole directory to match rather
# than let the umask quietly relax them to 0644 on every deployed host. 0755
# on the directory itself keeps $install_dir at the mode `mkdir -p` gave it:
# with a trailing slash on the source, rsync applies the source root's
# attributes to the destination root.
stage_dir="$(mktemp -d -t "${AGENT_NAME}.stage.XXXXXX")"
trap 'rm -rf "$stage_dir"' EXIT

render_agent_toml   > "$stage_dir/agent.toml"
render_compose_yaml > "$stage_dir/docker-compose.yml"
if [[ -n "$operator_key" ]]; then
  # printf, not cp: the key came from the environment, and this is the one
  # place it touches a disk. mktemp -d gave $stage_dir 0700, so the file is
  # never readable by other users even between creation and the chmod below.
  printf '%s\n' "$operator_key" > "$stage_dir/${SECRET_FILE}"
fi
chmod 600 "$stage_dir"/*
chmod 755 "$stage_dir"

remote_exec "mkdir -p $install_dir $install_dir/data"
# The trailing slash on the source is load-bearing: without it rsync creates
# $install_dir/<staging-dir-name>/ and the agent keeps running against its old
# agent.toml, with nothing anywhere reporting an error.
run_or_print "rsync -avz '$stage_dir/' '$host:$install_dir/'"
# The image tar ships separately: save_if_changed keys its skip on the
# ${image_tar}.id sidecar in build/, so folding a multi-hundred-MB file into
# the staging dir would mean copying it on every run.
run_or_print "rsync -avz '$image_tar' '$host:$install_dir/${AGENT_NAME}.image.tar'"

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

# Phase 6: verify (On operator) — smoke-test over loopback on the remote.
step "On remote: smoke test (healthz + agent card over loopback via ssh)"
if [[ "$dry_run" == "1" ]]; then
  info "[dry-run] would ssh $host curl -> http://127.0.0.1:${AGENT_PORT}/healthz + agent card"
else
  # The agent dials the chain gRPC and finishes an initial markets-cache
  # refresh before serving; give it a few seconds to come up.
  healthy=""
  for _ in 1 2 3 4 5 6 7 8 9 10; do
    code=$(ssh -o BatchMode=yes "$host" \
      "curl -sS -o /dev/null -w '%{http_code}' --max-time 5 http://127.0.0.1:${AGENT_PORT}/healthz" \
      2>/dev/null || echo 000)
    if [[ "$code" == "200" ]]; then healthy="1"; break; fi
    sleep 2
  done
  if [[ -z "$healthy" ]]; then
    info "healthz on :${AGENT_PORT} did not answer 200. Check logs with:"
    info "  ssh $host 'docker logs $AGENT_NAME --tail=80'"
    info "Common cause: the gRPC/RPC endpoints in agent.toml are not reachable"
    info "from inside the container."
    fail "smoke test failed for $AGENT_NAME"
  fi
  skills=$(ssh -o BatchMode=yes "$host" \
    "curl -sS --max-time 5 http://127.0.0.1:${AGENT_PORT}/.well-known/agent-card.json" \
    2>/dev/null | { command -v jq >/dev/null 2>&1 && jq -r '.skills | length' || cat; } || echo "")
  if [[ "$skills" =~ ^[0-9]+$ ]]; then
    pass "$AGENT_NAME :${AGENT_PORT} — /healthz 200, card served ($skills skills)"
  else
    # jq may be missing on the operator — the card body already proves the
    # endpoint answers; don't fail the deploy over the count.
    pass "$AGENT_NAME :${AGENT_PORT} — /healthz 200, card fetched (skill count unverified)"
  fi
fi

step "Done — $AGENT_NAME $image_tag running on $host (:${AGENT_PORT}, advertised at $public_url)"
