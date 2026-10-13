# syntax=docker/dockerfile:1
# Workspace image: Ubuntu 24.04 LTS plus the Nexmesh workspace agent, which
# dials out to the control plane. Build from the repository root:
#   docker build -f deploy/workspace/Dockerfile -t nexmesh/workspace:local .
# then set NEXMESH_WORKSPACE_IMAGE=nexmesh/workspace:local.

FROM golang:1.26 AS agent
WORKDIR /src/server
COPY server/go.mod server/go.sum ./
RUN go mod download
COPY server/ ./
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/nexmesh-agent ./cmd/nexmesh-agent

FROM ubuntu:24.04
ENV DEBIAN_FRONTEND=noninteractive
# Tools that runtime installs (mise) and common builds need. Runtimes themselves
# are installed on demand by the workspace's Runtimes panel.
RUN apt-get update \
 && apt-get install -y --no-install-recommends ca-certificates curl git build-essential pkg-config unzip xz-utils locales \
 && rm -rf /var/lib/apt/lists/* \
 && locale-gen en_US.UTF-8
ENV LANG=en_US.UTF-8
COPY --from=agent /out/nexmesh-agent /usr/local/bin/nexmesh-agent
WORKDIR /workspace
ENTRYPOINT ["/usr/local/bin/nexmesh-agent"]
