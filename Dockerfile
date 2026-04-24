# syntax=docker/dockerfile:1.6
#
# Multi-stage build for gosidian. Final stage is alpine:3.20 with git
# installed so internal/gitsync can shell-exec the binary; runs as nonroot
# UID 65532 (~35 MB). See ADR-003 for the distroless→alpine switch
# rationale: gitsync fatal-at-boot when git missing from PATH.

FROM golang:1.25-alpine AS builder
RUN apk add --no-cache git ca-certificates
WORKDIR /src

# Cache module downloads
COPY go.mod go.sum ./
RUN go mod download

# Build
COPY . .
ARG VERSION=dev
RUN CGO_ENABLED=0 GOOS=linux \
    go build -trimpath \
      -ldflags="-s -w -X main.version=${VERSION}" \
      -o /out/gosidian ./cmd/gosidian

FROM alpine:3.20
RUN apk add --no-cache git ca-certificates \
    && addgroup -g 65532 -S nonroot \
    && adduser -u 65532 -S nonroot -G nonroot \
    && mkdir -p /vault \
    && chown 65532:65532 /vault

COPY --from=builder /out/gosidian /gosidian

USER 65532:65532
WORKDIR /data
VOLUME ["/vault"]

# Container-friendly defaults: bind MCP on 0.0.0.0 so it can be reached
# through the published port (auth via bearer token is still required).
ENV GOSIDIAN_VAULT=/vault \
    GOSIDIAN_ADDR=:8080 \
    GOSIDIAN_MCP_ADDR=0.0.0.0:8765

EXPOSE 8080 8765

HEALTHCHECK --interval=30s --timeout=3s --start-period=5s --retries=3 \
    CMD ["/gosidian","healthcheck"]

ENTRYPOINT ["/gosidian"]
