# syntax=docker/dockerfile:1
# Nexmesh control plane: web UI embedded in a single Go binary.
# Build from the repository root:  docker build -f deploy/Dockerfile -t nexmesh/nexmesh .

FROM node:22-alpine AS web
RUN corepack enable
WORKDIR /src/web
COPY web/package.json web/pnpm-lock.yaml ./
RUN pnpm install --frozen-lockfile
COPY web/ ./
RUN pnpm build

FROM golang:1.26 AS server
WORKDIR /src/server
COPY server/go.mod server/go.sum ./
RUN go mod download
COPY server/ ./
COPY --from=web /src/web/dist ./internal/webui/dist
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/nexmesh ./cmd/nexmesh \
 && CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/nexmesh-agent ./cmd/nexmesh-agent

FROM alpine:3.20
# The Docker driver drives the `docker` CLI against the host's Docker Engine.
RUN apk add --no-cache docker-cli ca-certificates tzdata
COPY --from=server /out/nexmesh /usr/local/bin/nexmesh
COPY catalog /opt/nexmesh/catalog
ENV NEXMESH_CATALOG_DIR=/opt/nexmesh/catalog \
    NEXMESH_ADDR=:8080
EXPOSE 8080
ENTRYPOINT ["/usr/local/bin/nexmesh"]
