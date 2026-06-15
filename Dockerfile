# syntax=docker/dockerfile:1.6
#
# Multi-stage build for gosidian v2.0:
#   1. node:24-alpine builds the Vue 3 SPA (`npm run build`).
#   2. golang:1.25-alpine compiles the binary, embedding the dist/.
#   3. alpine:3.20 runtime with git on PATH for gitsync.
#
# Final image is ~35 MB and runs as nonroot UID 65532. The
# distroless→alpine switch is documented in ADR-003 (gitsync
# requires git at runtime).

# -----------------------------------------------------------------
# 1. SPA build
# -----------------------------------------------------------------
FROM node:24-alpine AS web-builder
WORKDIR /web

# Cache `npm ci` against the lockfile.
COPY web/package.json web/package-lock.json ./
RUN npm ci --no-audit --no-fund

# i18n catalogues are imported via the @catalogs Vite alias and
# precompiled at build time by @intlify/unplugin-vue-i18n. We mount
# them next to the web/ tree so the alias `../internal/i18n/catalogs`
# resolves without escaping the web-builder stage's cwd.
COPY internal/i18n/catalogs /internal/i18n/catalogs

# Source.
COPY web/ ./

# Vite outputs to ../internal/server/web/dist by default. We pin the
# outDir explicitly so the path is always absolute (avoids surprises
# if package.json changes the build script).
RUN npm run build -- --outDir /out/dist --emptyOutDir

# -----------------------------------------------------------------
# 2. Go compile
# -----------------------------------------------------------------
FROM golang:1.25-alpine AS go-builder
RUN apk add --no-cache git ca-certificates
WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

# Bring in the Go source after deps so Go-only edits don't bust npm.
COPY . .

# Drop in the freshly-built SPA. The Go side picks it up via the
# //go:embed all:dist directive in internal/server/web/embed.go, so
# the dist/ tree must be present BEFORE go build runs.
COPY --from=web-builder /out/dist ./internal/server/web/dist

ARG VERSION=dev
RUN CGO_ENABLED=0 GOOS=linux \
    go build -trimpath \
      -ldflags="-s -w -X main.version=${VERSION}" \
      -o /out/gosidian ./cmd/gosidian

# -----------------------------------------------------------------
# 3. Runtime
# -----------------------------------------------------------------
FROM alpine:3.24
RUN apk add --no-cache git ca-certificates \
    && addgroup -g 65532 -S nonroot \
    && adduser -u 65532 -S nonroot -G nonroot \
    && mkdir -p /vault \
    && chown 65532:65532 /vault

COPY --from=go-builder /out/gosidian /gosidian

USER 65532:65532
WORKDIR /data
VOLUME ["/vault"]

ENV GOSIDIAN_VAULT=/vault \
    GOSIDIAN_ADDR=:8080 \
    GOSIDIAN_MCP_ADDR=0.0.0.0:8765

EXPOSE 8080 8765

HEALTHCHECK --interval=30s --timeout=3s --start-period=5s --retries=3 \
    CMD ["/gosidian","healthcheck"]

ENTRYPOINT ["/gosidian"]
