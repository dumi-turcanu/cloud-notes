# Cloud Notes frontend

React single-page application plus the nginx container that serves it and proxies `/api/*` to the
backend (`cloud-notes-frontend` image). The browser only talks to one origin; there is no API URL,
token or other configuration in the JavaScript bundle.

- Container environment variables and behaviour: [../docs/configuration.md](../docs/configuration.md#frontend-container)
- Architecture and request flow: [../docs/architecture.md](../docs/architecture.md)

## Stack

React 19, React Router 8 (data router), TanStack Query 5, Tailwind CSS 4 (CSS-first), Vite 8,
TypeScript 6, ESLint 10, Vitest 5 with Testing Library and happy-dom. Node.js 24 LTS
(`.nvmrc`; `engines.node` is `>=22.12`).

## Routes

| Route | Purpose |
| --- | --- |
| `/login` | Sign in: e-mail + password (`local`), one "Sign in with …" button (`oidc`), or e-mail + password / Google through the Firebase SDK (`identity-platform`) |
| `/register` | Create an account (`local`: hidden when sign-up is disabled, invite code in invite mode; `identity-platform`: through the SDK); redirects to `/login` with `oidc` |
| `/` | Notes: list, create, edit, delete, publish/unpublish (if enabled) |
| `/trash` | Deleted notes: restore |
| `/shared/:shareUuid` | Public read-only note (only when public sharing is enabled) |

Anonymous visitors are redirected to `/login` and back after signing in. Note text is rendered as
plain text. The sign-in page follows `auth.provider` from `GET /api/config`; the Firebase SDK is only
downloaded (separate lazy chunks) when that is `identity-platform`. See
[../docs/authentication.md](../docs/authentication.md).

## Local development

```bash
cd frontend
npm ci
npm run dev        # http://localhost:5173
```

The Vite dev server proxies `/api` to `http://localhost:8080` (override with `VITE_DEV_API_TARGET`).
Start the backend first: `make up` at the repository root (also serves the API on `localhost:8080`),
or `make run` in `../backend`. The local backend uses `SESSION_COOKIE_SECURE=false`, so the session
cookie works over plain HTTP.

## Commands

| Command | What it does |
| --- | --- |
| `npm run dev` | Development server with hot reload |
| `npm run build` | Type check and production build into `dist/` |
| `npm run preview` | Serve the production build locally |
| `npm run lint` | ESLint |
| `npm run typecheck` | `tsc -b` |
| `npm test` | Unit and component tests (`vitest run`); `npm run test:watch` for watch mode |

From the repository root, `make lint` and `make test` run these together with the backend checks.

## Container image

```bash
docker build --build-arg VERSION=1.0.0 --build-arg COMMIT="$(git rev-parse --short HEAD)" \
  -t cloud-notes-frontend:1.0.0 .
docker run --rm -p 3000:8080 -e API_UPSTREAM=http://host.docker.internal:8080 cloud-notes-frontend:1.0.0
```

- Multi-stage: `node:24-alpine` builds the SPA, `nginxinc/nginx-unprivileged:1.30-alpine` serves it.
- Listens on `8080` (`PORT` to change), runs as non-root, works with a read-only root file system when
  `/tmp` and `/etc/nginx/conf.d` are writable (`emptyDir`/`tmpfs`).
- **`API_UPSTREAM` is required** (`scheme://host[:port]`, no path); the container exits with a clear
  error when it is missing or malformed. `API_UPSTREAM_HOST` optionally overrides the `Host` header sent
  to the backend.
- `/api/` is proxied without rewriting, `/healthz` answers `200` without calling the backend, unknown
  paths fall back to `index.html`, `/assets/*` is cached as immutable.
- Sends a strict Content-Security-Policy and other security headers; the SPA therefore contains no
  inline scripts or styles. `FRONTEND_AUTH_PROFILE=identity-platform` (with `IDP_AUTH_DOMAIN` and
  `IDP_SIGN_IN_METHODS`) adds the hosts the Firebase Auth SDK needs; the default `none` keeps the
  strict policy byte for byte.
- nginx configuration lives in `nginx/` (template, headers template, entrypoint validation script that
  renders the upstream and header includes into `/etc/nginx/conf.d` at start).
