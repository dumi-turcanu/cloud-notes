# Root developer commands. `make` lists them. Docker (with compose v2) is the
# only prerequisite for `up`, `smoke`, `build` and the container-based linters.

SHA        ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo unknown)
VERSION    ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
BASE_URL   ?= http://localhost:3000

HADOLINT   := hadolint/hadolint:latest-alpine
SHELLCHECK := koalaman/shellcheck:stable
SHELL_SRC  := scripts/smoke-test.sh scripts/smoke-test-oidc.sh $(wildcard frontend/nginx/docker-entrypoint.d/*.sh)
COMPOSE_OIDC := docker compose -f docker-compose.yml -f docker-compose.oidc.yml

.DEFAULT_GOAL := help
.PHONY: help up up-oidc down clean logs ps smoke smoke-oidc test test-integration vuln lint build demo-user

help: ## Show this help
	@awk 'BEGIN {FS = ":.*## "} /^[a-z-]+:.*## / {printf "  %-16s %s\n", $$1, $$2}' $(MAKEFILE_LIST)

up: ## Build and start the local stack (http://localhost:3000)
	VERSION=$(VERSION) COMMIT=$(SHA) docker compose up -d --build --wait

up-oidc: ## Like `up`, plus Keycloak as OIDC provider (sign in as student@example.com)
	VERSION=$(VERSION) COMMIT=$(SHA) $(COMPOSE_OIDC) up -d --build --wait

down: ## Stop the stack (keeps the database volume)
	docker compose down --remove-orphans

clean: ## Stop the stack and delete the database volume
	docker compose down -v --remove-orphans

logs: ## Follow the logs of all services
	docker compose logs -f

ps: ## Show service status
	docker compose ps

smoke: ## Run the end-to-end smoke test against BASE_URL
	BASE_URL=$(BASE_URL) ./scripts/smoke-test.sh

smoke-oidc: ## Run the OIDC login + notes smoke test against BASE_URL (needs `make up-oidc`)
	BASE_URL=$(BASE_URL) ./scripts/smoke-test-oidc.sh

test: ## Backend and frontend unit tests
	$(MAKE) -C backend test
	cd frontend && npm ci --no-audit --no-fund && npm test

test-integration: ## Backend tests against a throw-away PostgreSQL container (needs Docker)
	$(MAKE) -C backend test-integration

vuln: ## Scan the backend Go dependencies and standard library with govulncheck
	$(MAKE) -C backend vuln

lint: ## Backend, frontend, Dockerfile and shell linting
	$(MAKE) -C backend lint
	cd frontend && npm ci --no-audit --no-fund && npm run lint && npm run typecheck
	docker run --rm -i $(HADOLINT) < backend/Dockerfile
	docker run --rm -i $(HADOLINT) < frontend/Dockerfile
	docker run --rm -v "$(CURDIR):/mnt" -w /mnt $(SHELLCHECK) $(SHELL_SRC)

build: ## Build both images tagged with the git short SHA
	docker build --build-arg VERSION=$(VERSION) --build-arg COMMIT=$(SHA) -t cloud-notes-backend:$(SHA) backend
	docker build --build-arg VERSION=$(VERSION) --build-arg COMMIT=$(SHA) -t cloud-notes-frontend:$(SHA) frontend

demo-user: ## Create a user: NEW_USER_PASSWORD=... make demo-user EMAIL=you@example.com
	@test -n "$(EMAIL)" || { echo "usage: NEW_USER_PASSWORD=... make demo-user EMAIL=you@example.com"; exit 2; }
	@test -n "$$NEW_USER_PASSWORD" || { echo "NEW_USER_PASSWORD must be set in the environment"; exit 2; }
	docker compose exec -T -e NEW_USER_PASSWORD backend /cloud-notes-api create-user --email "$(EMAIL)" --if-not-exists
