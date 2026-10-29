# syntax=docker/dockerfile:1

# The container AI image (CA-04, CA-05): the four live harnesses (Claude Code, pi, OpenCode and
# omp), bun, git and the partyline binary, on Debian slim, running as the non-root user `agent`.
# `partyline runner` starts one container per container AI from this image; the entrypoint
# registers the harness with Partyline and keeps `partyline <harness>` alive inside a TTY.
# See deploy/agent/README.md for the environment contract.
#
# Build stages run on the build machine's platform; Go cross-compiles for the target, while the
# harness packages are installed for the target platform (node and bun come from their images).

# 1. Web app (architecture independent), embedded in the binary like the server image.
FROM --platform=$BUILDPLATFORM oven/bun:1.4 AS web
WORKDIR /src/web
COPY web/package.json web/bun.lock ./
RUN bun install --frozen-lockfile
COPY web/ ./
RUN bun run build

# 2. The partyline binary: same module, flags and version stamp as the server Dockerfile.
FROM --platform=$BUILDPLATFORM golang:1.27 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
COPY --from=web /src/web/dist/ internal/webui/dist/
ARG TARGETOS
ARG TARGETARCH
# Keep in step with the server Dockerfile (ONB-11): VERSION from CI or the tag in the context,
# else `dev-<commit>` from the context's .git or SOURCE_COMMIT, else `dev`.
ARG VERSION=""
ARG SOURCE_COMMIT=""
RUN version="$(sh scripts/version.sh)" \
 && echo "partyline version: ${version}" \
 && CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH \
    go build -trimpath -buildvcs=false \
    -ldflags "-s -w -X github.com/wnexous/partyline/internal/cli.Version=${version}" \
    -o /out/partyline ./cmd/partyline

# 3. Node LTS and bun from their official images: no installer script runs at build time, and
# copying them for the target platform keeps a multi-arch build correct.
FROM node:22-bookworm-slim AS node
FROM oven/bun:1.4 AS bun

# 4. Runtime: Debian slim, one non-root user, the four harnesses pinned by version.
FROM debian:bookworm-slim

# Pinned harness versions: the ones installed on the development box when this image was
# written (T5). Bump them together with a release; the entrypoint never updates itself.
ARG CLAUDE_CODE_VERSION=2.1.304
ARG PI_VERSION=1.4.0
ARG OPENCODE_VERSION=1.19.3
ARG OMP_VERSION=18.12.3
ARG TARGETARCH

ENV DEBIAN_FRONTEND=noninteractive
RUN apt-get update \
 && apt-get install -y --no-install-recommends \
      ca-certificates \
      curl \
      git \
      tini \
      util-linux \
 && rm -rf /var/lib/apt/lists/*

COPY --from=node /usr/local/bin/node /usr/local/bin/node
COPY --from=node /usr/local/lib/node_modules /usr/local/lib/node_modules
COPY --from=bun /usr/local/bin/bun /usr/local/bin/bun
RUN ln -sf ../lib/node_modules/npm/bin/npm-cli.js /usr/local/bin/npm \
 && ln -sf ../lib/node_modules/npm/bin/npx-cli.js /usr/local/bin/npx \
 && ln -sf bun /usr/local/bin/bunx

# Claude Code, pi and OpenCode are npm packages (their platform binaries come as dependencies).
# `--ignore-scripts=false` is npm's default; it is spelled out so a future npm default cannot
# leave the harnesses without their native binaries.
RUN npm install --global --no-fund --no-audit --ignore-scripts=false \
      "@anthropic-ai/claude-code@${CLAUDE_CODE_VERSION}" \
      "@earendil-works/pi-coding-agent@${PI_VERSION}" \
      "opencode-ai@${OPENCODE_VERSION}" \
 && npm cache clean --force \
 && rm -rf /root/.npm

# omp ships as a release binary; the release's SHA256SUMS.txt pins what is downloaded.
RUN case "$TARGETARCH" in \
      amd64) omp_asset=omp-linux-x64 ;; \
      arm64) omp_asset=omp-linux-arm64 ;; \
      *) echo "unsupported TARGETARCH: $TARGETARCH" >&2; exit 1 ;; \
    esac \
 && base="https://github.com/can1357/oh-my-pi/releases/download/v${OMP_VERSION}" \
 && curl -fsSL -o "/tmp/${omp_asset}" "${base}/${omp_asset}" \
 && curl -fsSL -o /tmp/SHA256SUMS.txt "${base}/SHA256SUMS.txt" \
 && (cd /tmp && grep -E "  ${omp_asset}\$" SHA256SUMS.txt | sha256sum -c -) \
 && install -m 0755 "/tmp/${omp_asset}" /usr/local/bin/omp \
 && rm -f "/tmp/${omp_asset}" /tmp/SHA256SUMS.txt

COPY --from=build /out/partyline /usr/local/bin/partyline
COPY deploy/agent/entrypoint.sh /usr/local/bin/entrypoint.sh
RUN chmod 0755 /usr/local/bin/entrypoint.sh

# uid/gid 1000, home /home/agent: the harness configs, the partyline login and the workspace all
# live under this user, never under root. /workspace is created here so a fresh named volume
# mounted over it inherits the ownership (CA-05).
RUN groupadd --gid 1000 agent \
 && useradd --uid 1000 --gid 1000 --create-home --home-dir /home/agent --shell /bin/bash agent \
 && mkdir -p /home/agent/.config /workspace/repo \
 && chown -R agent:agent /home/agent /workspace \
 && chmod 0700 /home/agent /home/agent/.config

# The harnesses must not update themselves out from under the pinned versions.
ENV HOME=/home/agent \
    XDG_CONFIG_HOME=/home/agent/.config \
    LANG=C.UTF-8 \
    TERM=xterm-256color \
    DISABLE_AUTOUPDATER=1 \
    OPENCODE_DISABLE_AUTOUPDATE=1 \
    NPM_CONFIG_UPDATE_NOTIFIER=false \
    PATH=/home/agent/.local/bin:/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin

USER agent
WORKDIR /workspace
VOLUME /workspace

# tini reaps the harness processes and -g forwards SIGTERM to the whole group, so `docker stop`
# reaches the session inside `script`.
ENTRYPOINT ["/usr/bin/tini", "-g", "--", "/usr/local/bin/entrypoint.sh"]
