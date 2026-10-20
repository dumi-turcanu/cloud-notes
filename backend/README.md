# Cloud Notes backend

Go REST API for Cloud Notes: e-mail + password sign-in with server-side sessions in PostgreSQL,
notes with trash and optional public sharing, Prometheus metrics. One static binary,
`cloud-notes-api`, packaged in a distroless image (`cloud-notes-backend`).

- Module: `github.com/dumi-turcanu/cloud-notes/backend` (Go 1.27)
- Configuration, commands, ports, metrics: [../docs/configuration.md](../docs/configuration.md)
- HTTP API: [../docs/api/openapi.yaml](../docs/api/openapi.yaml)
- Authentication design: [../docs/security.md](../docs/security.md)

## Layout

```text
cmd/api/                 entry point and subcommands (serve, migrate, create-user, healthcheck, version)
internal/config/         environment loading (with *_FILE secrets) and validation
internal/database/       connection pool, embedded migrations, user/session/note repositories
internal/auth/           Argon2id hashing, sessions, lockout, rate limit, client IP, middleware, handlers
internal/handlers/       router, notes, health endpoints, public config
internal/observability/  JSON logging, request logging, Prometheus metrics
internal/server/         listeners, graceful shutdown
internal/models/         domain types
```

## Local development

Prerequisites: Go 1.27 (the toolchain downloads automatically) and a PostgreSQL 15+ database. The
quickest database is the one from the compose stack:

```bash
docker compose up -d db                  # from the repository root; listens on 127.0.0.1:5432
cd backend
cp .env.example .env                     # local-only values; .env is git-ignored
make run                                 # go run ./cmd/api serve  (migrations run on start)
curl -s localhost:8080/api/config
curl -s localhost:9090/metrics | head
```

`.env` is read only if it exists. See `.env.example` for every variable with comments.
Create a user without open sign-up:

```bash
NEW_USER_PASSWORD='a-long-local-password' make create-user EMAIL=you@example.com
```

For hot reload of the UI against this backend run `npm run dev` in `../frontend`.

## Commands

Run from `backend/` (`make help` is not provided; the targets are in the `Makefile`):

| Command | What it does |
| --- | --- |
| `make run` | Run the API locally (uses `.env`) |
| `make test` | Unit tests with the race detector (`go test -race ./...`) |
| `make test-integration` | All tests including database tests, against a throw-away `postgres:18-alpine` container on port 55432 (needs Docker) |
| `make lint` | `gofmt` check and `go vet` |
| `make cover` | Coverage report (`coverage.html`) |
| `make vuln` | `govulncheck` against the dependencies |
| `make build` | Static binary in `bin/cloud-notes-api` |
| `make docker-build` | Build the image as `cloud-notes-backend:dev` |
| `make migrate` | Apply migrations (uses `.env`) |
| `make create-user EMAIL=…` | Create a user (password from `NEW_USER_PASSWORD` or stdin) |

The database tests are skipped unless `TEST_DATABASE_URL` is set; `make test-integration` sets it
(`postgres://cloudnotes:cloudnotes-local-only@127.0.0.1:55432/cloudnotes?sslmode=disable` — a
throw-away local credential).

## Container image

```bash
docker build --build-arg VERSION=1.0.0 --build-arg COMMIT="$(git rev-parse --short HEAD)" \
  -t cloud-notes-backend:1.0.0 .
```

- Multi-stage build; the runtime image is `gcr.io/distroless/static-debian13:nonroot`, running as
  UID/GID `65532` with the binary at `/cloud-notes-api` (entrypoint) and `serve` as default command.
- Ports: `8080` (API, `/healthz`, `/readyz`) and `9090` (`/metrics`).
- Compatible with a read-only root file system; it writes nothing to disk.
- `HEALTHCHECK` runs `/cloud-notes-api healthcheck`, because the image has neither `curl` nor `wget`.
- One-off jobs use the same image: `migrate`, `create-user --email … --if-not-exists`.
- Required configuration: `DB_HOST`, `DB_NAME`, `DB_USER`, `DB_PASSWORD` (or `DATABASE_URL`). Everything
  else has a safe default (sign-up disabled, TLS to the database required, secure cookies).
- Build for several architectures with `docker buildx build --platform linux/amd64,linux/arm64 …`;
  the Go compilation always runs natively and cross-compiles.
