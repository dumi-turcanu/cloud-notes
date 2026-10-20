# Backend developer commands. Run from the backend/ directory.
# A local .env (see .env.example) is picked up by the binary itself.

BIN        ?= bin/cloud-notes-api
IMAGE      ?= cloud-notes-backend
VERSION    ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT     ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo unknown)
LDFLAGS    := -s -w -X main.version=$(VERSION) -X main.commit=$(COMMIT)

# Throw-away database used by `make test-integration` (local only).
TEST_DB_NAME      ?= cn-backend-test-db
TEST_DB_PORT      ?= 55432
TEST_DATABASE_URL ?= postgres://cloudnotes:cloudnotes-local-only@127.0.0.1:$(TEST_DB_PORT)/cloudnotes?sslmode=disable

.PHONY: run test test-integration lint build docker-build migrate create-user cover vuln

run: ## Run the API locally (uses .env)
	go run ./cmd/api serve

test: ## Unit tests with the race detector
	go test -race ./...

test-integration: ## Tests against a throw-away PostgreSQL 18 container on port $(TEST_DB_PORT)
	@docker rm -f $(TEST_DB_NAME) >/dev/null 2>&1 || true
	docker run -d --rm --name $(TEST_DB_NAME) -p 127.0.0.1:$(TEST_DB_PORT):5432 \
		-e POSTGRES_USER=cloudnotes -e POSTGRES_PASSWORD=cloudnotes-local-only -e POSTGRES_DB=cloudnotes \
		postgres:18-alpine >/dev/null
	@i=0; until docker exec $(TEST_DB_NAME) pg_isready -U cloudnotes -d cloudnotes >/dev/null 2>&1 \
		&& docker exec $(TEST_DB_NAME) psql -U cloudnotes -d cloudnotes -c 'select 1' >/dev/null 2>&1; do \
		i=$$((i+1)); [ $$i -lt 60 ] || { echo "database did not become ready"; docker logs $(TEST_DB_NAME); docker rm -f $(TEST_DB_NAME) >/dev/null; exit 1; }; \
		sleep 1; done
	@TEST_DATABASE_URL='$(TEST_DATABASE_URL)' go test -race -count=1 ./...; rc=$$?; \
		docker rm -f $(TEST_DB_NAME) >/dev/null 2>&1; exit $$rc

lint: ## gofmt check + go vet
	@test -z "$$(gofmt -l .)" || { echo "gofmt needed on:"; gofmt -l .; exit 1; }
	go vet ./...

build: ## Build the binary into bin/
	CGO_ENABLED=0 go build -trimpath -ldflags "$(LDFLAGS)" -o $(BIN) ./cmd/api

docker-build: ## Build the container image
	docker build --build-arg VERSION=$(VERSION) --build-arg COMMIT=$(COMMIT) -t $(IMAGE):dev .

migrate: ## Apply database migrations (uses .env)
	go run ./cmd/api migrate

create-user: ## make create-user EMAIL=you@example.com  (password: NEW_USER_PASSWORD env or stdin)
	@test -n "$(EMAIL)" || { echo "usage: make create-user EMAIL=you@example.com"; exit 2; }
	go run ./cmd/api create-user --email '$(EMAIL)' --if-not-exists

cover: ## Coverage report (coverage.out, coverage.html)
	go test -race -coverprofile=coverage.out ./...
	go tool cover -html=coverage.out -o coverage.html
	go tool cover -func=coverage.out | tail -1

vuln: ## Scan dependencies for known vulnerabilities (built with this module's Go toolchain)
	GOTOOLCHAIN=$$(go env GOVERSION) go run golang.org/x/vuln/cmd/govulncheck@latest ./...
