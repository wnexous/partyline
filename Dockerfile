# syntax=docker/dockerfile:1

# Build stages run on the build machine's platform; Go cross-compiles for the target, so
# multi-arch images need no emulation.

# 1. Web app (architecture independent).
FROM --platform=$BUILDPLATFORM oven/bun:1.4 AS web
WORKDIR /src/web
COPY web/package.json web/bun.lock ./
RUN bun install --frozen-lockfile
COPY web/ ./
RUN bun run build

# 2. Go binary with the web app embedded.
FROM --platform=$BUILDPLATFORM golang:1.27 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
COPY --from=web /src/web/dist/ internal/webui/dist/
ARG TARGETOS
ARG TARGETARCH
# The version stamped into the binary (`partyline version`, the server's /healthz) comes from
# scripts/version.sh: the VERSION build argument (CI passes `git describe`, releases the tag),
# else `git describe --tags` of the .git in the build context (a PaaS such as Dokploy builds
# from its clone without build arguments), else dev-<12 chars> of that commit or of
# SOURCE_COMMIT (set by some platforms), else dev.
ARG VERSION=""
ARG SOURCE_COMMIT=""
RUN version="$(sh scripts/version.sh)" \
 && echo "partyline version: ${version}" \
 && CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH \
    go build -trimpath -buildvcs=false \
    -ldflags "-s -w -X github.com/wnexous/partyline/internal/cli.Version=${version}" \
    -o /out/partyline ./cmd/partyline \
 && mkdir -p /out/data

# 3. Runtime: distroless, non-root (uid/gid 65532), all state under /data.
FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/partyline /partyline
# An empty /data owned by nonroot: a fresh named volume mounted here inherits this ownership.
COPY --from=build --chown=65532:65532 /out/data /data
ENV PARTYLINE_ADDR=:8080 \
    PARTYLINE_DATA_DIR=/data
USER 65532:65532
EXPOSE 8080
VOLUME /data
HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 CMD ["/partyline", "healthcheck"]
ENTRYPOINT ["/partyline"]
CMD ["serve"]
