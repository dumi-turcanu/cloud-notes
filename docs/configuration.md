# Configuration reference

Everything environment-specific is configuration; you never need to change application code.
This page lists every setting the two images understand. The backend reads its configuration from
**environment variables** (source of truth: `backend/internal/config/config.go`), the frontend
container from the three variables in [Frontend container](#frontend-container)
(`frontend/nginx/`).

- **Secret** = a value that must never be committed, baked into an image or printed in logs. Every
  secret variable also accepts a `_FILE` variant (for example `DB_PASSWORD_FILE`): the path of a file
  whose trimmed content is the value. The `_FILE` variant wins if both are set. Mounted secret
  files (Kubernetes Secret volumes, Docker/ECS/Cloud Run secret mounts) therefore work without
  putting the value in the environment.
- Durations use Go syntax: `500ms`, `15s`, `15m`, `12h`.
- Empty values count as unset. Booleans accept `true`/`false` (also `1`/`0`, `t`/`f`).
- The backend validates **all** settings at start-up, prints every problem in one list and exits
  with a non-zero status, so a bad deployment fails fast and readably.
- A `.env` file in the working directory is loaded only if it exists (local development).
  See `backend/.env.example`.

## Backend environment variables

### Server

| Variable | Default | Required | Secret | Description |
| --- | --- | --- | --- | --- |
| `PORT` | unset | no | no | If set (Cloud Run and other PaaS inject it), the default of `HTTP_ADDR` becomes `:<PORT>`. |
| `HTTP_ADDR` | `:8080` (or `:<PORT>`) | no | no | Listen address of the main listener (API, `/healthz`, `/readyz`). |
| `API_BASE_PATH` | `/api` | no | no | Prefix of all application routes. Must start with `/` and have no trailing slash. The frontend container proxies `/api/`, so only change it together with your routing. |
| `METRICS_ADDR` | `:9090` | no | no | Listen address of the separate Prometheus listener. An empty value counts as unset and keeps the default, so the listener cannot be switched off through the environment: keep port `9090` private instead. |
| `LOG_LEVEL` | `info` | no | no | `debug`, `info`, `warn` or `error`. Logs are JSON on stdout. |
| `SHUTDOWN_DELAY` | `0s` | no | no | Time between turning `/readyz` to 503 and closing the listener, so load balancers stop sending traffic first. Use about `5s` behind Kubernetes Services, ECS or other load balancers. |
| `SHUTDOWN_TIMEOUT` | `15s` | no | no | Maximum time to drain in-flight requests after `SHUTDOWN_DELAY`. Must be positive. |
| `TRUSTED_PROXY_HOPS` | `0` | no | no | Number of proxies in front of the backend that **append** to `X-Forwarded-For`. `0` uses the TCP peer address. See [deployment-guide.md](deployment-guide.md#client-ip-and-trusted_proxy_hops). |
| `CSRF_TRUSTED_ORIGINS` | empty | no | no | Comma-separated extra origins allowed for state-changing requests, e.g. `https://notes.example.com`. See [CSRF](security.md#csrf-protection). |

### Features

| Variable | Default | Required | Secret | Description |
| --- | --- | --- | --- | --- |
| `FEATURE_PUBLIC_SHARING` | `false` | no | no | When `true`, notes can be published and read anonymously via `/shared/<share_uuid>` (adds the publish and public routes). Off by default: published notes are readable by anyone with the link. |
| `PUBLIC_APP_URL` | empty | when sharing is enabled | no | Public origin used to build share links (`<PUBLIC_APP_URL>/shared/<share_uuid>`), absolute `http(s)` URL, trailing slash removed. |

### Database

| Variable | Default | Required | Secret | Description |
| --- | --- | --- | --- | --- |
| `DATABASE_URL` | empty | no | **yes** | Full DSN (`postgres://user:pass@host:5432/db?sslmode=require`). If set it overrides all `DB_*` connection variables (but see `DB_SSLMODE` below). |
| `DB_HOST` | — | unless `DATABASE_URL` | no | Database host name or IP address. |
| `DB_PORT` | `5432` | no | no | Database port. |
| `DB_NAME` | — | unless `DATABASE_URL` | no | Database name. |
| `DB_USER` | — | unless `DATABASE_URL` | no | Database user. |
| `DB_PASSWORD` | — | unless `DATABASE_URL` | **yes** | Database password. |
| `DB_SSLMODE` | `require` | no | no | `disable`, `require`, `verify-ca` or `verify-full`. Use at least `require` outside local development; prefer `verify-full`. Used when the DSN is assembled from `DB_*`; with `DATABASE_URL` put `sslmode=` in the DSN. |
| `DB_SSLROOTCERT` | empty | for `verify-ca` / `verify-full` | no | Path of the CA bundle that signed the server certificate. |
| `DB_MAX_CONNS` | `10` | no | no | Maximum pooled connections **per backend instance**. Keep `instances × DB_MAX_CONNS` below the database's connection limit. |
| `DB_MIGRATE_ON_START` | `true` | no | no | `serve` applies pending migrations before listening (safe with many replicas: golang-migrate takes a PostgreSQL advisory lock). Set `false` when a pre-deploy job runs `migrate`. |

### Authentication and sessions

| Variable | Default | Required | Secret | Description |
| --- | --- | --- | --- | --- |
| `AUTH_SIGNUP_MODE` | `disabled` | no | no | `disabled` (no self-service sign-up), `invite` (requires `AUTH_INVITE_CODE`) or `open`. Keep `disabled` or `invite` in the cloud. |
| `AUTH_INVITE_CODE` | empty | when mode is `invite` | **yes** | At least 16 characters; compared in constant time. |
| `SESSION_TTL` | `12h` | no | no | Absolute session lifetime. |
| `SESSION_IDLE_TIMEOUT` | `1h` | no | no | A session unused for this long expires. |
| `SESSION_COOKIE_SECURE` | `true` | no | no | `true` sends the cookie as `__Host-cn_session` with the `Secure` flag, which requires HTTPS. `false` (cookie `cn_session`) is **only** for plain-HTTP local development. |
| `AUTH_MAX_FAILED_LOGINS` | `5` | no | no | Consecutive failed logins before the account is locked. |
| `AUTH_LOCKOUT_DURATION` | `15m` | no | no | How long a locked account rejects logins. |
| `AUTH_RATE_LIMIT_PER_MINUTE` | `10` | no | no | Login + register requests per client IP per minute (burst of the same size), per backend instance. |
| `NEW_USER_PASSWORD` | — | for `create-user` | **yes** | Password of the user created by `create-user` (alternative: `--password-stdin`). Not used by `serve`. `NEW_USER_PASSWORD_FILE` is supported. |

Passwords must be 12–128 characters and differ from the e-mail address.

## Backend commands

The image's entrypoint is `/cloud-notes-api`; the default argument is `serve`.

| Command | Description |
| --- | --- |
| `serve` | Runs the API and the metrics listener (default). Applies migrations first when `DB_MIGRATE_ON_START=true`. |
| `migrate` | Applies all pending migrations and exits `0`. Use it as a pre-deploy job. Idempotent. |
| `create-user --email E [--display-name N] [--if-not-exists] [--password-stdin]` | Creates a user with the same validation as sign-up. The password comes from `NEW_USER_PASSWORD` / `NEW_USER_PASSWORD_FILE`, or from stdin with `--password-stdin` — never from a command-line flag. `--if-not-exists` exits `0` when the e-mail already exists, which makes the job safe to repeat. Exit codes: `0` success, `1` error, `2` usage error. |
| `healthcheck [--url URL]` | Requests `http://127.0.0.1:<port of HTTP_ADDR or PORT>/readyz` (or `URL`) with a 3 s timeout; exits `0` on HTTP 200. For container health checks, because the distroless image has no `curl` or `wget`. |
| `version` | Prints version, commit and Go version. |

`create-user` and `migrate` need the same database settings as `serve`.

## Frontend container

| Variable | Default | Required | Description |
| --- | --- | --- | --- |
| `API_UPSTREAM` | — | **yes** | Base URL of the backend: `scheme://host[:port]`, no path, e.g. `http://backend:8080` or `https://backend-xyz.a.run.app`. The container refuses to start with a clear message if it is missing or malformed. nginx re-resolves the name through DNS regularly, so changing IP addresses are picked up. |
| `API_UPSTREAM_HOST` | host of `API_UPSTREAM` | no | `Host` header (and TLS SNI name) used towards the backend. The default is right for Kubernetes Services, Cloud Run, Container Apps and ECS service discovery. |
| `PORT` | `8080` | no | Port nginx listens on. |

Behaviour of the container, independent of configuration:

- Runs as a non-root user and works with a read-only root file system provided `/tmp` and
  `/etc/nginx/conf.d` are writable (for example `emptyDir` volumes or `tmpfs`). The entrypoint writes the
  validated upstream settings into `/etc/nginx/conf.d` at start.
- `/api/` is proxied to `API_UPSTREAM` **without path rewriting** (30 s timeouts); `X-Forwarded-For` is
  appended, `X-Forwarded-Proto` and `X-Request-ID` are forwarded. If the backend cannot be reached it answers
  `503` with a JSON body.
- `/healthz` answers `200 ok` without calling the backend. Unknown paths fall back to `index.html`.
- `/assets/*` is cached for a year (`immutable`); `index.html` and the fallback use `no-cache`.
- Security headers on all non-API responses: `Content-Security-Policy` (`default-src 'self'` …),
  `X-Content-Type-Options`, `Referrer-Policy`, `Permissions-Policy`, `Cross-Origin-Opener-Policy`,
  `Strict-Transport-Security`. The backend itself sends no HTML.
- Request bodies are limited to 1 MiB; text responses are gzip-compressed; the access log is JSON on stdout.

The frontend has **no build-time configuration**: the SPA only calls relative `/api/...` URLs.

## Ports

| Container | Port | Purpose | Exposure |
| --- | --- | --- | --- |
| Frontend | `8080` | SPA, `/api/*` proxy, `/healthz` | Reachable from the edge only |
| Backend | `8080` | API under `/api`, `/healthz`, `/readyz` | Internal: reachable from the frontend (and the health checker) only |
| Backend | `9090` | Prometheus `/metrics` | Internal: reachable from your monitoring only; never through the edge |
| PostgreSQL | `5432` | Database | Reachable from the backend, migration and bootstrap jobs only |

## HTTP endpoints at a glance

Full details: [api/openapi.yaml](api/openapi.yaml).

| Endpoint | Auth | Purpose |
| --- | --- | --- |
| `GET /healthz` (backend) | none | Liveness: `200` while the process runs. No dependencies. |
| `GET /readyz` (backend) | none | Readiness: `200` if the database answers within 2 s; `503` otherwise and as soon as shutdown starts. |
| `GET /healthz` (frontend) | none | Frontend liveness, `200 ok`. |
| `GET :9090/metrics` (backend) | none | Prometheus metrics (separate listener). |
| `GET /api/config` | none | Sign-up mode, feature flags and version, used by the SPA. |
| `/api/auth/*` | mixed | register, login, logout, me |
| `/api/notes…` | session | notes CRUD, trash, restore, publish (if enabled) |
| `GET /api/public/notes/{share_uuid}` | none | Public note (only if sharing is enabled) |

## Metrics

Served on `METRICS_ADDR` (default `:9090`) at `/metrics`, own registry:

| Metric | Type | Labels |
| --- | --- | --- |
| `http_requests_total` | counter | `method`, `route` (route pattern, `unmatched` for unknown paths), `code` |
| `http_request_duration_seconds` | histogram | `method`, `route` |
| `http_requests_in_flight` | gauge | — |
| `auth_login_attempts_total` | counter | `result` = `success`, `invalid_credentials`, `locked`, `rate_limited` |
| `auth_registrations_total` | counter | `result` |
| `pgxpool_acquired_conns`, `pgxpool_idle_conns`, `pgxpool_total_conns`, `pgxpool_max_conns` | gauge | — |
| `pgxpool_acquire_total`, `pgxpool_acquire_duration_seconds_total`, `pgxpool_empty_acquire_total`, `pgxpool_canceled_acquire_total` | counter | — |
| `cloud_notes_build_info` | gauge (always 1) | `version`, `commit`, `go_version` |
| `go_*`, `process_*` | standard | Go runtime and process collectors |

## Logs

Both containers write one JSON object per line to stdout/stderr. The backend logs one line per
request with `method`, `route`, `status`, `bytes`, `duration_ms`, `request_id`, `client_ip` and
`user_id` (when signed in); `/healthz` and `/readyz` are logged at `debug` level only. An incoming
`X-Request-ID` (up to 64 characters of `A-Za-z0-9._-`) is kept, otherwise one is generated, and it is
echoed in the response, so a request can be followed from nginx to the backend. Passwords, cookies,
session tokens and invite codes are never logged.
