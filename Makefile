.PHONY: smoke start verify dev-server dev-web test build lint acceptance it it-services

smoke:
	python3 scripts/smoke.py --base $${BASE:-http://localhost:8080}

start:
	./start.sh

verify:
	./scripts/verify.sh

dev-server:
	cd server && NEXMESH_DRIVER=fake NEXMESH_STORE=memory go run ./cmd/nexmesh

dev-web:
	cd web && pnpm dev

test:
	cd server && go test ./...
	cd web && pnpm test --run

# Builds the web app, embeds it into the control-plane binary, and builds the agent.
build:
	cd web && pnpm install --frozen-lockfile && pnpm build
	rm -rf server/internal/webui/dist && mkdir -p server/internal/webui/dist && cp -r web/dist/. server/internal/webui/dist/
	cd server && go build -o ../bin/nexmesh ./cmd/nexmesh && CGO_ENABLED=0 go build -o ../bin/nexmesh-agent ./cmd/nexmesh-agent
	touch server/internal/webui/dist/.gitkeep

acceptance:
	cd server && go test ./internal/e2e/ -v -count=1

# Probe integration tests against real Redis, MariaDB, MongoDB, RabbitMQ and Kafka.
it-services:
	./scripts/it/start-services.sh all

it:
	. scripts/it/env.sh && cd server && NEXMESH_IT=1 go test ./internal/probe/ ./internal/component/ -count=1

lint:
	cd server && go vet ./...
