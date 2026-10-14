# syntax=docker/dockerfile:1
# Imagem do DataReplication (S.3). Construída uma vez e usada pelas três células.

# ---- build: binário estático ----
FROM golang:1.27-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download
COPY api ./api
COPY cmd ./cmd
COPY internal ./internal
RUN --mount=type=cache,target=/go/pkg/mod --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 go build -trimpath -o /out/datareplication ./cmd/datareplication

# ---- test: go test -race (CA-14). O -race exige CGO e glibc, por isso Debian. ----
FROM golang:1.27 AS test
WORKDIR /src
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download
COPY api ./api
COPY cmd ./cmd
COPY internal ./internal
COPY observability ./observability
RUN --mount=type=cache,target=/go/pkg/mod --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=1 go test -race -count=1 ./...

# ---- execução: alpine (permite ls nos volumes), usuário não root ----
FROM alpine:3.22
RUN adduser -D -u 10001 dr \
    && mkdir -p /var/lib/dr/raft /var/lib/dr/replog \
    && chown -R 10001:10001 /var/lib/dr
COPY --from=build /out/datareplication /usr/local/bin/datareplication
USER 10001
EXPOSE 8000 7000 9090
HEALTHCHECK --interval=5s --timeout=2s --retries=3 CMD wget -qO- http://127.0.0.1:9090/livez || exit 1
ENTRYPOINT ["/usr/local/bin/datareplication"]
