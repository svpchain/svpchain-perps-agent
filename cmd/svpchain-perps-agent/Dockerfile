# Dockerfile for the svpchain perps agent (cmd/svpchain-perps-agent), the
# perps-category slice of the DEX agent.
#
# The resulting image contains only the agent binary plus ca-certificates.
# Runtime config lives in agent.toml, mounted read-only at
# /etc/svpchain-perps-agent/agent.toml; the optional operator key is mounted
# beside it. Use a DISTINCT operator key per binary — the agent id derives
# from the key and self-registration hashes this binary's own card.
#
# Build from the repo root, from a VENDORED dependency tree (see the
# svpchain-remote-agents Dockerfile header for why):
#   go mod vendor
#   docker build --platform linux/amd64 \
#     -t svpchain-perps-agent:<tag> \
#     -f cmd/svpchain-perps-agent/Dockerfile .

FROM golang:1.25-alpine AS builder

RUN set -eux; apk add --no-cache ca-certificates build-base; apk add git linux-headers bash binutils-gold

WORKDIR /svpchain

COPY . .

ARG VERSION
ARG COMMIT

RUN --mount=type=cache,target=/root/.cache/go-build \
    --mount=type=cache,target=/root/go/pkg/mod \
    go build \
      -mod=vendor \
      -tags "netgo,muslc" \
      -ldflags "-X github.com/cosmos/cosmos-sdk/version.Name=svpchain \
              -X github.com/cosmos/cosmos-sdk/version.AppName=svpchain-perps-agent \
              -X github.com/cosmos/cosmos-sdk/version.Version=$VERSION \
              -X github.com/cosmos/cosmos-sdk/version.Commit=$COMMIT \
              -X github.com/cosmos/cosmos-sdk/version.BuildTags='netgo,muslc' \
              -w -s -linkmode=external -extldflags '-Wl,-z,muldefs -static'" \
      -trimpath \
      -o /svpchain/build/svpchain-perps-agent \
      ./cmd/svpchain-perps-agent

# --------------------------------------------------------
# Runner
# --------------------------------------------------------

FROM alpine:3.21

RUN apk add --no-cache bash ca-certificates

COPY --from=builder /svpchain/build/svpchain-perps-agent /bin/svpchain-perps-agent

# Default A2A listen port. Override via agent.toml listen_addr.
EXPOSE 8082

ENTRYPOINT ["/bin/svpchain-perps-agent"]
CMD ["-config", "/etc/svpchain-perps-agent/agent.toml"]
