# syntax=docker/dockerfile:1.7
# Multi-stage build for the Onion Spider API.
# Stage 1 builds a statically-linked Go binary; stage 2 is distroless static
# (no shell, no package manager, ~2MB base). Runs as nonroot by default.

ARG GO_VERSION=1.25
FROM golang:${GO_VERSION}-alpine AS builder

WORKDIR /src

# Cache module downloads in a separate layer so dependency changes don't
# invalidate the source-code layer.
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod \
    go mod download

COPY . ./

# CGO disabled → fully static binary, runnable on distroless/scratch.
# -s -w strips DWARF and symbol table; -trimpath removes local paths.
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=linux \
    go build -ldflags="-s -w" -trimpath -o /out/onion-spider-api ./cmd/api

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=builder /out/onion-spider-api /usr/local/bin/onion-spider-api
USER nonroot:nonroot
EXPOSE 8900
ENTRYPOINT ["/usr/local/bin/onion-spider-api"]
