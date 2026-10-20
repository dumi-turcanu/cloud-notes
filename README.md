# Cloud Notes

A secure, cloud-agnostic note-taking application: a React single-page app, a Go REST API and
PostgreSQL. Users sign in with e-mail and password, write notes, move them to the trash and
restore them, and can optionally publish a note through an unguessable share link.

The application is delivered as **two container images** (`cloud-notes-frontend`,
`cloud-notes-backend`) and needs **PostgreSQL 15+** as its only stateful dependency. It contains
no cloud SDKs and depends on no external identity provider, so the same images run unchanged on
AWS, Google Cloud, Azure, any Kubernetes cluster or a single Docker host.

## Features

- E-mail + password sign-in with Argon2id hashing, server-side sessions and an `HttpOnly`,
  `Secure`, `SameSite=Strict` cookie; account lockout and per-IP rate limiting
- Sign-up modes: disabled (default), invite code, or open; `create-user` command for operators
- Notes: create, edit, delete to trash, restore
- Optional public, read-only share links (`FEATURE_PUBLIC_SHARING`)
- Strict Content-Security-Policy, CSRF protection, no tokens in JavaScript
- Prometheus metrics, JSON logs with request ids, liveness/readiness probes, graceful shutdown
- Distroless, non-root, read-only-root-file-system compatible container images

## Quickstart (about 60 seconds)

Prerequisites: Docker with Compose v2, and `make`.

```bash
make up                      # build the images and start db + backend + frontend
open http://localhost:3000   # or just browse to it: register an account, create a note
make smoke                   # end-to-end smoke test against http://localhost:3000
make down                    # stop (keeps data);  make clean  also deletes the database volume
```

The local stack runs with open sign-up, plain HTTP and **throw-away credentials that are for local
development only** (see [docker-compose.yml](docker-compose.yml)). Never reuse them elsewhere.
`make help` lists every command.

| URL | What |
| --- | --- |
| `http://localhost:3000` | Production-like entry point: nginx serves the SPA and proxies `/api/*` to the backend |
| `http://localhost:8080` | Backend API directly (debugging) |
| `http://localhost:5173` | `npm run dev` in `frontend/` (hot reload, proxies `/api` to `localhost:8080`) |

## Repository layout

```text
backend/                 Go API (module github.com/dumi-turcanu/cloud-notes/backend)
  cmd/api/               entry point: serve | migrate | create-user | healthcheck | version
  internal/              config, database (+ embedded migrations), auth, handlers, observability, server
frontend/                React SPA and the nginx container that serves it and proxies /api
  src/                   application code        nginx/   container configuration
scripts/smoke-test.sh    end-to-end smoke test (bash + curl + jq)
docker-compose.yml       local stack
Makefile                 developer and CI commands
docs/                    documentation (below)
```

## Documentation

| Document | Contents |
| --- | --- |
| [docs/architecture.md](docs/architecture.md) | Components, request flow, login sequence, data model, deployment topologies |
| [docs/configuration.md](docs/configuration.md) | Every environment variable, subcommand, port and endpoint |
| [docs/deployment-guide.md](docs/deployment-guide.md) | Running the images in production: networking, database, secrets, migrations, probes, per-platform settings |
| [docs/security.md](docs/security.md) | Authentication design, threat model, limitations, hardening checklist |
| [docs/api/openapi.yaml](docs/api/openapi.yaml) | OpenAPI 3.1 description of the HTTP API |
| [backend/README.md](backend/README.md) | Backend development, tests, image |
| [frontend/README.md](frontend/README.md) | Frontend development, tests, image and container contract |

## Toolchain

| Tool | Version |
| --- | --- |
| Go | 1.27 (`go.mod`: `go 1.27.0`; the toolchain downloads automatically) |
| Node.js | 24 LTS (`frontend/.nvmrc`; `engines.node` is `>=22.12`) |
| PostgreSQL | 15 or newer, no extensions (local stack: `postgres:18-alpine`) |
| Docker | with Compose v2 and BuildKit |

Images are built from `golang:1.27-alpine` / `gcr.io/distroless/static-debian13:nonroot`
(backend) and `node:24-alpine` / `nginxinc/nginx-unprivileged:1.30-alpine` (frontend).

## Commands

All of these run from the repository root (locally or in CI):

| Command | What it does |
| --- | --- |
| `make lint` | gofmt + `go vet`, ESLint + TypeScript type check, hadolint on both Dockerfiles, shellcheck on the scripts |
| `make test` | Backend unit tests (`go test -race ./...`) and frontend tests (`vitest`) |
| `make test-integration` | Backend tests against a throw-away PostgreSQL container |
| `make vuln` | `govulncheck` on the backend's Go dependencies and standard library |
| `make build` | Build both images, tagged `cloud-notes-backend:<git-sha>` and `cloud-notes-frontend:<git-sha>` |
| `BASE_URL=https://… ./scripts/smoke-test.sh` | Smoke test against a deployed environment |

Build the images directly (the build contexts are `backend/` and `frontend/`):

```bash
docker build --build-arg VERSION="$(git describe --tags --always)" --build-arg COMMIT="$(git rev-parse --short HEAD)" \
  -t cloud-notes-backend:"$(git rev-parse --short HEAD)" backend
docker build --build-arg VERSION="$(git describe --tags --always)" --build-arg COMMIT="$(git rev-parse --short HEAD)" \
  -t cloud-notes-frontend:"$(git rev-parse --short HEAD)" frontend
```

Both Dockerfiles cross-compile / build on the build host's architecture, so
`docker buildx build --platform linux/amd64,linux/arm64 …` is fast and needs no emulation.

## Deploying it

Start with [docs/deployment-guide.md](docs/deployment-guide.md). In short: run both images
(Kubernetes or a serverless container service), route `/api/*` and the SPA through one HTTPS
origin, give the backend a private PostgreSQL 15+ database, inject secrets at
runtime, run `cloud-notes-api migrate` before the new version gets traffic, create the first user
with `cloud-notes-api create-user`, then run `scripts/smoke-test.sh`.
