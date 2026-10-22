# Deployment guide

A cloud-agnostic guide to running the two Cloud Notes images correctly and securely in production:
what the surrounding infrastructure must provide and which settings to use on each platform.
Variable names are explained in [configuration.md](configuration.md); the design behind them in
[architecture.md](architecture.md) and [security.md](security.md).

## Checklist

| # | Requirement | Why |
| --- | --- | --- |
| 1 | The browser reaches the SPA **and** `/api/*` on the **same origin** | The session cookie is `SameSite=Strict` and there is no CORS support. |
| 2 | HTTPS everywhere users connect (TLS at the edge) | The session cookie is `Secure` (`__Host-` prefix); without HTTPS the app cannot sign in. |
| 3 | Only the edge can reach the frontend (origin lock-down) | Otherwise WAF, rate limiting and caching rules can be bypassed. |
| 4 | The backend has no public ingress | It must be reachable from the frontend (and health checkers, monitoring) only. |
| 5 | PostgreSQL 15+ in private networking, TLS enforced, automated backups | It is the only stateful component and holds password hashes and sessions. |
| 6 | Secrets injected at runtime from a secret manager | Nothing secret in Git, images, IaC variables or pipeline logs. |
| 7 | Migrations run as a pre-deploy job | New versions must not receive traffic before the schema is ready. |
| 8 | First user created by an automated one-off job (`AUTH_PROVIDER=local`); with a cloud identity service, sign-up restricted in the service | Sign-up stays closed. |
| 9 | Health checks and graceful shutdown configured per platform | Zero-downtime rollouts. |
| 10 | `TRUSTED_PROXY_HOPS` matches the number of proxies | Correct client IP for rate limiting and audit logs. |
| 11 | Logs and metrics collected | Operating and alerting; see [Observability hooks](#observability-hooks). |
| 12 | `scripts/smoke-test.sh` passes after every deployment | Detects routing, cookie and CSRF misconfiguration. |

## Images

Build both images from this repository (build contexts `backend/` and `frontend/`), tag them
immutably (for example with the commit SHA) and push them to a private registry. Pass
`--build-arg VERSION=… --build-arg COMMIT=…` to stamp them; the version is reported by
`GET /api/config` and in the `cloud_notes_build_info` metric. Details: [README.md](../README.md).

| Image | Entrypoint / default command | Port | Runs as |
| --- | --- | --- | --- |
| `cloud-notes-backend` | `/cloud-notes-api serve` | `8080` (API), `9090` (metrics) | UID 65532, read-only root file system compatible |
| `cloud-notes-frontend` | nginx | `8080` | non-root nginx, read-only root file system with writable `/tmp` and `/etc/nginx/conf.d` |

## Networking

### Same-origin routing

Pick one of two valid designs:

- **Built-in proxy (simplest).** The edge sends *everything* to the frontend container; nginx
  forwards `/api/*` to the backend using `API_UPSTREAM`. The backend only needs to be reachable
  from the frontend.
- **Path-based routing at your load balancer.** The load balancer sends `/api/*` to the backend and
  everything else to the frontend. The frontend container still requires `API_UPSTREAM` to be set
  (the container refuses to start without it) — point it at the backend anyway.

In both cases the public host name must be identical for the SPA and the API, and `/api/*` must
never be cached by the edge. Cache only `/assets/*` (fingerprinted, `immutable`); `index.html`
is served with `no-cache`, and you should purge `/` and `/index.html` after a deployment if your
edge caches them.

### TLS and origin lock-down

Terminate TLS at the edge (and ideally re-encrypt to the origin). Restrict the frontend so that
only the edge can connect — for example with security groups or firewall rules limited to the
edge's address ranges, private connectivity to the edge, internal-only ingress settings, or a
secret header that the edge adds and the origin load balancer verifies. Verify from outside that
the frontend and backend cannot be reached directly.

### Private backend and database

- The backend accepts traffic from the frontend tasks/pods only (and from probes and your
  metrics scraper on `9090`).
- The database accepts connections on `5432` from the backend, the migration job and the bootstrap
  job only. Place it in private subnets without a public address.
- The backend needs outbound access to the database and, with `AUTH_PROVIDER=oidc` or
  `identity-platform`, HTTPS to the identity provider and nothing else. The frontend needs outbound
  access to the backend and DNS.

## PostgreSQL

- **Version 15 or newer.** Any managed PostgreSQL service works; no extensions are installed or
  required (the schema uses only core features such as `gen_random_uuid()`), so there is nothing to
  enable or allow-list.
- **Enforce TLS** on the server and connect with `DB_SSLMODE=require` at least. Prefer
  `verify-full` with `DB_SSLROOTCERT` pointing at the provider's CA bundle (mount the bundle into
  the container).
- Create a database and a user for the application. The app creates its tables via migrations, so
  the user that runs `migrate` needs DDL rights on that database. For least privilege use two
  users (see the [hardening checklist](security.md#hardening-checklist)).
- **Connections:** each backend instance opens up to `DB_MAX_CONNS` (default `10`) connections. Make
  sure `instances × DB_MAX_CONNS` (plus jobs) stays below the database's connection limit, or add
  a pooler.
- Enable automated backups and test a restore.

## Secrets

Up to four values are secret: the database password (`DB_PASSWORD` or the whole `DATABASE_URL`), the invite
code (`AUTH_INVITE_CODE`, only in `invite` mode), the password of the first user
(`NEW_USER_PASSWORD`, `local` mode) and the OIDC client secret (`OIDC_CLIENT_SECRET`, only with
`AUTH_PROVIDER=oidc`; Cognito and Entra client secrets expire or can be rotated, so plan it). The
Identity Platform Web API key is public by design and not a secret. Store the secret ones in your cloud's secret manager and inject them at runtime, either

- as environment variables from the platform's secret integration, or
- as files, with the matching `*_FILE` variable (for example `DB_PASSWORD_FILE=/run/secrets/db-password`).

Never put them in image layers, committed files, plain-text IaC variables or logs. After rotating a
secret, restart the backend (the values are read at start-up).

## Migrations

The schema is embedded in the backend image. Two options:

1. **Pre-deploy job (recommended).** Run `cloud-notes-api migrate` as a one-off task with the
   backend image and database settings *before* the new version receives traffic, and deploy the
   backend with `DB_MIGRATE_ON_START=false`. A failing migration then stops the rollout while the old
   version keeps serving.
2. **At start-up.** Leave `DB_MIGRATE_ON_START=true` (the default). Every instance runs `migrate`
   first; golang-migrate holds a PostgreSQL advisory lock, so concurrent instances are safe, but a
   broken migration makes all new instances fail.

`migrate` is idempotent. On Kubernetes, run it as a Job (for example a Helm `pre-install,pre-upgrade` hook).

## User sign-in

Choose the sign-in mode per environment with `AUTH_PROVIDER` (the same two images everywhere):

| Platform | Service | Settings |
| --- | --- | --- |
| AWS | Amazon Cognito user pool | `AUTH_PROVIDER=oidc`, issuer `https://cognito-idp.<region>.amazonaws.com/<pool-id>`, `OIDC_LOGOUT_URL` |
| Azure | Microsoft Entra External ID | `AUTH_PROVIDER=oidc`, issuer `https://<sub>.ciamlogin.com/<tenant-id>/v2.0` |
| Google Cloud | Identity Platform | `AUTH_PROVIDER=identity-platform`, `IDP_*` on the backend, `FRONTEND_AUTH_PROFILE=identity-platform` and `IDP_AUTH_DOMAIN` on the frontend |

Step-by-step setup, the exact variables and troubleshooting are in
[authentication.md](authentication.md). Register `https://<app>/api/auth/oidc/callback` as the
redirect URI at the provider, keep the public host name identical to `PUBLIC_APP_URL`, and note that
the backend needs outbound HTTPS to the provider (discovery, token endpoint, keys), and to
`www.googleapis.com` for Identity Platform. With an external provider the first-user bootstrap below
does not apply: users are created on their first sign-in.

## First-user bootstrap (`local` mode)

With `AUTH_SIGNUP_MODE=disabled` nobody can register, so create the first user with a one-off job
using the backend image, the same database settings and the password from your secret manager:

```text
cloud-notes-api create-user --email you@example.com --if-not-exists
```

The password is read from `NEW_USER_PASSWORD` (or `NEW_USER_PASSWORD_FILE`), or from stdin with
`--password-stdin`; it is never passed as a flag. `--if-not-exists` makes the job safe to run on
every deployment. Run it after the migration job. Additional users can be created the same way (or
use `AUTH_SIGNUP_MODE=invite` with a secret `AUTH_INVITE_CODE`). Do not use `open` sign-up in the
cloud.

## Health checks and graceful shutdown

| Probe | Backend | Frontend |
| --- | --- | --- |
| Liveness / process alive | `GET /healthz` on `8080` → `200` | `GET /healthz` on `8080` → `200` |
| Readiness / load balancer target health | `GET /readyz` on `8080` → `200` when the database answers within 2 s; `503` otherwise and during shutdown | `GET /healthz` (does not call the backend) |
| Container health check without `curl` | `/cloud-notes-api healthcheck` (exit `0` = healthy) | `wget -qO /dev/null http://127.0.0.1:8080/healthz` (built into the image) |

On `SIGTERM` the backend answers `/readyz` with `503`, waits `SHUTDOWN_DELAY`, stops accepting
connections and drains requests for up to `SHUTDOWN_TIMEOUT`, then exits. Choose
`SHUTDOWN_DELAY` so that the load balancer has noticed the failing readiness check before connections
are closed, and keep `SHUTDOWN_DELAY + SHUTDOWN_TIMEOUT` below the platform's stop timeout.

| Platform | Settings to apply |
| --- | --- |
| **Kubernetes** | Liveness `/healthz`, readiness `/readyz`, startup probe on `/healthz`; `SHUTDOWN_DELAY=5s`, `SHUTDOWN_TIMEOUT=15s`, `terminationGracePeriodSeconds: 30`; `RollingUpdate` with `maxUnavailable: 0`; PodDisruptionBudget. |
| **ECS on Fargate** | Target group health check path `/readyz` (backend) and `/healthz` (frontend); container health check `/cloud-notes-api healthcheck`; `stopTimeout` ≥ 30 s (Fargate maximum is 120 s); `SHUTDOWN_DELAY=5s`; target group deregistration delay ≥ `SHUTDOWN_DELAY`; run migrations as a one-off task before updating the service. |
| **Cloud Run** | Platform injects `PORT` (the backend honours it; no need to set `HTTP_ADDR`). Startup and liveness probes on `/healthz`; Cloud Run sends `SIGTERM` and allows about 10 s before `SIGKILL`, so use `SHUTDOWN_DELAY=0s` and `SHUTDOWN_TIMEOUT=8s`. Metrics on `9090` are not reachable (one port per container): use the platform's request metrics. Run migrations and `create-user` as Cloud Run jobs. The frontend needs `PORT` too (supported). |
| **Azure Container Apps** | Liveness/readiness/startup probes as above on port `8080`; `terminationGracePeriodSeconds` up to 600 s; `SHUTDOWN_DELAY=5s`; internal ingress for the backend; run migrations and `create-user` as Container Apps jobs. |

Cloud Run and Container Apps send traffic to a revision only after its startup/readiness probe
succeeds; configure those probes rather than relying on the port being open.

### Client IP and `TRUSTED_PROXY_HOPS`

The backend uses the client address for the per-IP rate limit and audit data. It takes it from
`X-Forwarded-For`, counting `TRUSTED_PROXY_HOPS` entries from the **right**, because every
trusted proxy appends the address of its peer. A value too **high** lets clients forge their
address; too **low** merges several users into one rate-limit bucket. When unsure, choose the lower
value. Count only proxies that really append to the header:

| Topology | `TRUSTED_PROXY_HOPS` |
| --- | --- |
| Backend called directly (no proxy) | `0` |
| Edge → frontend nginx → backend, edge **does not** add `X-Forwarded-For` | `1` (nginx) |
| Edge (CDN/WAF) → frontend nginx → backend, edge appends the client address | `2` |
| Edge → load balancer → frontend nginx → backend, both append | `3` |
| Load balancer → backend directly (path-based routing), load balancer appends | `1` |
| Docker Compose in this repository (nginx only) | `1` |

Check it with the smoke test or a request through the edge: the `client_ip` field in the backend log
must show the real client address, not a proxy address.

### CSRF and `CSRF_TRUSTED_ORIGINS`

State-changing `/api` requests are checked with Go's `http.CrossOriginProtection` (see
[security.md](security.md#csrf-protection)). Browsers send `Sec-Fetch-Site`, which makes this work
without configuration. For clients that only send an `Origin` header, the backend compares it with
the `Host` header it receives — behind a proxy chain that is an internal host name, so set
`CSRF_TRUSTED_ORIGINS` to the public origin (for example `https://notes.example.com`).

## Observability hooks

- **Logs:** both containers write JSON lines to stdout/stderr. Collect them with your platform's
  agent or log driver. Search by `request_id` to follow one request across nginx and the backend.
- **Metrics:** the backend exposes Prometheus metrics on port `9090` at `/metrics` (see
  [configuration.md](configuration.md#metrics)). Scrape them with Prometheus or your cloud's
  managed Prometheus/agent; keep the port off the edge. Useful series: `http_requests_total` by `code`
  (4xx/5xx rates), `http_request_duration_seconds`, `pgxpool_*` (database connection use),
  `auth_login_attempts_total` (brute-force signals).
- **Platform metrics:** CPU/memory of the containers and database activity from your cloud's monitoring.

## Smoke test after every deployment

```bash
BASE_URL=https://notes.example.com SMOKE_EMAIL=ci-user@example.com SMOKE_PASSWORD="$PASSWORD_FROM_SECRET_MANAGER" \
  ./scripts/smoke-test.sh
```

Requires `bash`, `curl` and `jq`. With `AUTH_PROVIDER=local` the script checks the frontend health check, security and cache
headers, `/api/config`, sign-in with the session cookie, a full note life cycle (create, read, update,
delete, restore), sharing if enabled, that cross-site writes are rejected, and sign-out. It exits non-zero on any
failure. Without `SMOKE_EMAIL`/`SMOKE_PASSWORD` it registers a random user, which only works when
`AUTH_SIGNUP_MODE=open` — use a dedicated test user in the cloud. Run it from a pipeline stage
after the rollout, and fail the pipeline if it fails. The password endpoints do not exist with
`AUTH_PROVIDER=oidc` or `identity-platform`, so the script does not apply there: check
`GET /api/config` (`auth.provider`), the redirect to the provider and a manual sign-in instead
(`scripts/smoke-test-oidc.sh` automates the browser flow against the local Keycloak stack only).

## Service mapping

| Need | AWS | Google Cloud | Azure |
| --- | --- | --- | --- |
| Edge: CDN / global load balancer | Amazon CloudFront | Cloud CDN + external Application Load Balancer | Azure Front Door |
| WAF / rate limiting for `/api/auth/*` | AWS WAF | Cloud Armor | Front Door WAF |
| Managed Kubernetes | Amazon EKS | Google Kubernetes Engine | Azure Kubernetes Service |
| Serverless containers | Amazon ECS on AWS Fargate | Cloud Run | Azure Container Apps |
| Managed PostgreSQL | Amazon RDS / Aurora PostgreSQL | Cloud SQL for PostgreSQL | Azure Database for PostgreSQL – Flexible Server |
| Secret manager | AWS Secrets Manager | Secret Manager | Azure Key Vault |
| Container registry | Amazon ECR | Artifact Registry | Azure Container Registry |
| Private connectivity to the edge origin | CloudFront VPC origins | Cloud Run internal ingress + load balancer | Front Door Premium Private Link |
| Pod/task identity (no static keys) | IAM roles (IRSA, Pod Identity, task roles) | Service accounts, Workload Identity | Managed identities, Workload Identity |
| User sign-in | Amazon Cognito (`AUTH_PROVIDER=oidc`) | Identity Platform (`AUTH_PROVIDER=identity-platform`) | Microsoft Entra External ID (`AUTH_PROVIDER=oidc`) |
| Logs | CloudWatch Logs | Cloud Logging | Azure Monitor Log Analytics |
| Metrics | CloudWatch / Amazon Managed Service for Prometheus | Cloud Monitoring / Managed Service for Prometheus | Azure Monitor / managed Prometheus |
| One-off jobs (migrate, create-user) | ECS `RunTask` | Cloud Run jobs | Container Apps jobs; Kubernetes: Jobs / Helm hooks |
