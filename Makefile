.PHONY: dev-server dev-web test build lint

dev-server:
	cd server && NEXMESH_DRIVER=fake NEXMESH_STORE=memory go run ./cmd/nexmesh

dev-web:
	cd web && pnpm dev

test:
	cd server && go test ./...
	cd web && pnpm test --run

build:
	cd web && pnpm build
	cd server && go build -o ../bin/nexmesh ./cmd/nexmesh && go build -o ../bin/nexmesh-agent ./cmd/nexmesh-agent

lint:
	cd server && go vet ./...
