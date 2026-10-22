# Plan: Cloud-agnostic authentication for Cloud Notes (AWS Cognito, Azure Entra External ID, GCP Identity Platform)

Branch: `claude/happy-euler-yl439f`. Executor: Sonnet, high effort. Do one phase at a time and commit after each one.
Every phase must leave `make lint` and `make test` green.

**Handoff step (done in this session once the plan is approved):** commit this plan to the repo as
`docs/plans/cloud-agnostic-auth.md` on branch `claude/happy-euler-yl439f` and push it, so a fresh
Sonnet session can read it from the checkout. The prompt for that session is in the appendix at
the end of this file.

## Context

Cloud Notes is a capstone template: a React SPA, a Go API and PostgreSQL. Students deploy it on
AWS, Azure or GCP, and the course requires them to use their cloud's managed identity service.
The app was first built for Cognito. It was later moved to built-in e-mail/password login, with
Argon2id hashes, server-side sessions in PostgreSQL and a `__Host-cn_session` cookie that is
HttpOnly and SameSite=Strict. It has a strict CSP (`'self'` only) and no cloud SDKs.

**Goal:** one repository and one pair of container images. At runtime an env var selects the
identity backend:

| `AUTH_PROVIDER` | Used by | Mechanism |
| --- | --- | --- |
| `local` (default) | local dev, CI, smoke test | today's e-mail/password login, unchanged |
| `oidc` | AWS Cognito, Azure Entra External ID, Keycloak (local), Google Sign-In, Auth0 and others | Backend-for-Frontend (BFF): the Go API is a confidential OIDC client (Authorization Code + PKCE). It verifies the ID token and then issues **the same session cookie as today** |
| `identity-platform` | GCP Identity Platform | The SPA lazy-loads the Firebase Auth JS SDK, signs the user in and POSTs the ID token to `/api/auth/session`. The backend verifies it and issues the same session cookie |

### Research summary (why this design)

- **One codebase, not three frontends.** All three services are standards-based, so the
  differences fit in config plus one small adapter. Well-known multi-provider projects work this
  way: oauth2-proxy (one generic OIDC client plus a few specialised providers), Grafana
  `generic_oauth`, Auth.js/NextAuth providers, Gitea, Outline. Three forks would mean three times
  the maintenance, and the forks drift apart. Asking students to write the auth code with AI
  makes the security-critical code differ from student to student, which also makes grading
  inconsistent.
- **BFF pattern.** The IETF *OAuth 2.0 for Browser-Based Applications* BCP (now in the RFC Editor
  queue) treats a backend-for-frontend as the most secure option. It also fits the existing
  design: no tokens in JavaScript, HttpOnly cookie, same origin, and the `'self'` CSP is kept in
  `oidc` mode.
- **AWS Cognito** is a standard OIDC provider with discovery at
  `https://cognito-idp.<region>.amazonaws.com/<pool-id>`. Its discovery document has **no
  `end_session_endpoint`**, so the logout URL must be configurable
  (`https://<domain>/logout?client_id=…&logout_uri=…`).
- **Azure:** Azure AD B2C has been closed to new customers since 1 May 2025. Use **Microsoft
  Entra External ID** (external tenant). It is a standard OIDC provider with issuer
  `https://<tenant-subdomain>.ciamlogin.com/<tenant-id>/v2.0`. The `email` claim may need to be
  turned on as an optional claim.
- **GCP Identity Platform** (Firebase Auth's paid tier) is **not** a redirect-style OIDC provider
  for third-party apps: it has no hosted authorization endpoint. Apps sign in with the client SDK
  and verify the ID token on the server (`iss=https://securetoken.google.com/<project>`,
  `aud=<project>`, RS256, public JWKs from Google). That is why GCP gets its own small adapter.
  The backend side is still only JWT verification with the same library.
- **No automatic account linking by e-mail.** External users are keyed on `(issuer, subject)`.
  Linking by e-mail would let anyone who controls a matching e-mail at the IdP take over a local
  account.

Libraries: `github.com/coreos/go-oidc/v3` and `golang.org/x/oauth2` (backend). `firebase`, the
modular SDK, is loaded only through a dynamic `import()` in `identity-platform` mode (frontend).
Local test IdP: Keycloak, with an exact pinned tag.

---

## Phase 1: Backend config and provider seam

Read first: `backend/internal/config/config.go`, `backend/internal/auth/service.go`,
`backend/internal/handlers/router.go`, `backend/cmd/api/main.go`, `backend/cmd/api/createuser.go`.

1. In `config.go`, add these fields to `Config` and validate them in `Load`. Collect all errors,
   as the existing code does. Secrets go through `l.secret` so the `_FILE` variants work.
   - `AUTH_PROVIDER`: `local|oidc|identity-platform`, default `local`.
   - OIDC: `OIDC_ISSUER_URL` (required). `OIDC_CLIENT_ID` (required). `OIDC_CLIENT_SECRET`
     (secret, required). `OIDC_REDIRECT_URL`, defaulting to
     `PUBLIC_APP_URL + API_BASE_PATH + "/auth/oidc/callback"`. `OIDC_SCOPES`, default
     `openid email profile`; it must contain `openid`. `OIDC_PROVIDER_NAME`, the button label,
     default `Single sign-on`. `OIDC_LOGOUT_URL`, optional, a template with the placeholders
     `{client_id}` and `{post_logout_redirect_uri}`. `OIDC_POST_LOGOUT_REDIRECT_URL`, default
     `PUBLIC_APP_URL + "/login"`. `OIDC_DISCOVERY_URL` is optional and for dev only: it lets the
     backend fetch discovery from an internal hostname while still checking the token's `iss`
     against `OIDC_ISSUER_URL`.
   - Identity Platform: `IDP_PROJECT_ID` (required). `IDP_API_KEY` (required; public by design,
     because it is sent to the browser). `IDP_AUTH_DOMAIN` (required, e.g.
     `<project>.firebaseapp.com`). `IDP_TENANT_ID` (optional). `IDP_SIGN_IN_METHODS`, a CSV
     subset of `password,google`, default `password`.
   - `PUBLIC_APP_URL` becomes required when `AUTH_PROVIDER=oidc`, in addition to its current
     rule for public sharing.
   - Issuer, redirect and logout URLs must use `https`. The one exception: `http` is allowed when
     `SESSION_COOKIE_SECURE=false`, which is the existing local-dev switch.
   - Add tests to `config_test.go` for each new rule, in the same table style as the existing
     tests.
2. Migration `backend/internal/database/migrations/000002_external_identities.{up,down}.sql`:
   - `ALTER TABLE users ALTER COLUMN password_hash DROP NOT NULL;`
   - `CREATE TABLE user_identities (issuer TEXT NOT NULL, subject TEXT NOT NULL, user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE, created_at TIMESTAMPTZ NOT NULL DEFAULT now(), last_login_at TIMESTAMPTZ, PRIMARY KEY (issuer, subject));`
     plus an index on `user_id`.
   - The down migration drops the table. It restores `NOT NULL` only when no user has a NULL
     hash; otherwise it fails with a clear message.
3. `database/user_repo.go` and a new `database/identity_repo.go`. Add
   `UpsertExternal(ctx, ExternalIdentity) (*models.User, error)`. It runs in one transaction:
   - Look up the identity by `(issuer, subject)`.
   - If found, update `last_login_at` and refresh `users.email` / `display_name` from the claims.
     If the new e-mail is already taken, keep the old one and log a warning.
   - If not found, insert a user with a NULL password hash, then insert the identity.
   - A unique-violation on e-mail returns the new error `models.ErrAccountConflict`.
   `scanUser` must accept a NULL `password_hash` (scan it into `*string`). Cover this in
   `integration_test.go`.
4. `auth/service.go`:
   - Add `type ExternalIdentity struct{ Issuer, Subject, Email, DisplayName string }` and
     `func (s *Service) LoginExternal(ctx, id ExternalIdentity, meta ClientMeta) (*Issued, error)`.
   - `LoginExternal` normalises the e-mail with the existing `NormalizeEmail`, truncates the
     display name to `maxDisplayNameLen`, upserts, and then calls the existing `issue()`.
   - A missing or invalid e-mail returns the new error `ErrEmailClaimMissing`.
   - `Login` (local) must treat a user with a NULL hash as invalid credentials and still run the
     dummy hash, so timing stays equal.
   - Extend `UserStore`, `auth/authtest/fakes.go` and the service tests.
5. Router and `main.go`:
   - When `AUTH_PROVIDER != local`, do not mount `/auth/register` or `/auth/login`. Instead they
     answer `404 not_found`.
   - `create-user` refuses to run in external modes and prints a clear message.
   - `/api/config` gains `auth: { provider, display_name, identity_platform?: { api_key, auth_domain, project_id, tenant_id, sign_in_methods } }`.
     Keep `signup_mode`; it only applies to `local`.

## Phase 2: OIDC adapter (AWS / Azure / Keycloak)

New file `backend/internal/auth/oidc.go`, plus handlers in `auth/handlers.go` or a new
`auth/oidc_handlers.go`.

- **Lazy, cached discovery.** On first use, call `oidc.NewProvider`. Wrap the context with
  `oidc.InsecureIssuerURLContext` when `OIDC_DISCOVERY_URL` is set. Guard the cached provider with
  a mutex and do not cache failures. An IdP outage then blocks only new logins; existing sessions
  and `/readyz` keep working.
- **`GET /auth/oidc/login?from=<path>`:**
  - Sanitise `from` with a Go port of the frontend's `safeRedirect` (`frontend/src/lib/errors.ts`):
    it must start with `/`, must not start with `//` or `/\`, must contain no control characters,
    and is at most 2048 bytes; otherwise fall back to `/`.
  - Generate `state`, `nonce` and a PKCE verifier (`oauth2.GenerateVerifier`).
  - Store them, together with `from`, in a cookie `__Host-cn_oidc` (with the `Secure` flag
    switched by `SESSION_COOKIE_SECURE`, mirroring `cookie.go`). Use HttpOnly, Path `/`,
    **SameSite=Lax** (the IdP's redirect back is a cross-site navigation), Max-Age 600, and a
    JSON value, base64url-encoded.
  - Respond `302` to `AuthCodeURL(state, oidc.Nonce(nonce), oauth2.S256ChallengeOption(verifier))`.
- **`GET /auth/oidc/callback`:**
  - Apply the rate limiter.
  - If the IdP returned `error=`, redirect to `/login?error=oidc_cancelled`.
  - Compare `state` with the cookie in constant time.
  - `Exchange` with `oauth2.VerifierOption`, take `id_token` from the token response, and verify
    it with `provider.Verifier(&oidc.Config{ClientID})`. Then check `nonce`.
  - Read the claims `sub`, `email`, `name`, `preferred_username`, `given_name` and
    `family_name`. Email fallback: `email`, then `preferred_username` if it parses as an e-mail.
  - Call `LoginExternal`, delete the state cookie and `SetSessionCookie`. Redirect `302` to the
    relative path `from`. The SPA's next `/api/auth/me` fetch is same-site, so the Strict session
    cookie is sent.
  - On failure, redirect to `/login?error=oidc_failed|account_conflict|email_claim_missing`. Log
    the details server-side and never put them in the URL.
  - Record metrics through `Recorder.LoginAttempt` with results such as `oidc_success` and
    `oidc_failed`.
- **Logout.** In `oidc` mode, `POST /auth/logout` revokes the local session as it does today, then
  answers `200 {"logout_url": "..."}`. The URL is `OIDC_LOGOUT_URL` with its placeholders
  URL-encoded and filled in. If that is not set, it is the discovery `end_session_endpoint` plus
  `client_id` and `post_logout_redirect_uri`. If neither exists, return `204` as today.
- **Tests.**
  - New package `backend/internal/auth/oidctest`: an `httptest` fake IdP that serves discovery,
    JWKS, `/authorize` (redirects straight back with a code) and `/token`. It signs ID tokens with
    an RSA key generated in the test (go-jose ships with go-oidc). Claims, nonce, expiry and
    audience can be overridden.
  - Cover: the happy path; a state mismatch; a missing state cookie; a nonce mismatch; a wrong
    audience; an expired token; a missing e-mail; an account conflict; an open-redirect attempt
    in `from`; the logout URL template; and discovery failing and then succeeding on retry.

## Phase 3: Identity Platform adapter (GCP)

- **Backend `auth/identityplatform.go`.**
  - Build the verifier as
    `oidc.NewVerifier("https://securetoken.google.com/"+project, oidc.NewRemoteKeySet(ctx, "https://www.googleapis.com/service_accounts/v1/jwk/securetoken@system.gserviceaccount.com"), &oidc.Config{ClientID: project})`.
    Make the key set injectable for tests.
  - Extra checks: `auth_time` no more than 5 minutes ago (a fresh sign-in, which blocks replay of
    old tokens); `sub` is non-empty; `firebase.tenant` equals `IDP_TENANT_ID` when one is set;
    and `firebase.sign_in_provider` is one of the allowed methods (`password`→`password`,
    `google`→`google.com`).
  - Issuer for `user_identities`: the token `iss`.
  - `POST /auth/session {"id_token": "..."}`. It is already CSRF-protected by
    `CrossOriginProtection`; also apply the rate limiter. On success it calls `LoginExternal` and
    sets the session cookie, then returns `200 {user}`. Errors: `401 invalid_token` and
    `409 account_conflict`.
  - Tests use the `oidctest` signer with issuer `https://securetoken.google.com/test-project`.
- **Frontend `src/auth/identityPlatform.ts`.**
  - `async function getIdpAuth(cfg)` uses `await import('firebase/app')` and
    `await import('firebase/auth')`, then
    `initializeAuth(app, { persistence: inMemoryPersistence })`, so no tokens are written to
    IndexedDB or localStorage. Set `auth.tenantId` when configured.
  - Exports: `signInPassword`, `signUpPassword`, `signInGoogle` (popup) and `sendReset`. Each one
    returns `await user.getIdToken()` and then calls `signOut(auth)`, because our cookie is the
    session.
  - Map Firebase error codes (`auth/invalid-credential`, `auth/email-already-in-use`,
    `auth/weak-password`, `auth/too-many-requests`) to friendly messages.
- **Frontend container headers.**
  - Turn `frontend/nginx/snippets/security-headers.conf` into a file that
    `frontend/nginx/docker-entrypoint.d/05-validate-env.sh` renders. That script already writes
    `00-upstream.conf`, so follow its validation style.
  - New env `FRONTEND_AUTH_PROFILE=none|identity-platform`, default `none`, plus `IDP_AUTH_DOMAIN`
    (validated as a hostname).
  - With `identity-platform`: add `connect-src https://identitytoolkit.googleapis.com https://securetoken.googleapis.com`
    and `frame-src https://<IDP_AUTH_DOMAIN>`. If the Google popup is used, also add
    `script-src https://apis.google.com` and set `Cross-Origin-Opener-Policy: same-origin-allow-popups`.
  - With `none`, the headers must stay byte-identical to today's.

## Phase 4: Frontend UI per provider

Read first: `src/api/types.ts`, `src/api/auth.ts`, `src/context/AuthProvider.tsx`,
`src/context/authContext.ts`, `src/pages/LoginPage.tsx`, `src/pages/RegisterPage.tsx`,
`src/test/mockApi.ts`.

- **`types.ts`:** add the `AppConfig.auth` type. **`api/auth.ts`:** add
  `createSession(idToken)`, and give `logout()` the return type `{ logout_url?: string } | void`.
- **`AuthProvider`:** add `loginWithIdToken(idToken)`. `logout()` calls
  `window.location.assign(logout_url)` when one is returned, after clearing the local state.
- **`LoginPage`:**
  - `local` is unchanged.
  - `oidc` shows a single primary button, "Sign in with {display_name}". It navigates with
    `window.location.assign('/api/auth/oidc/login?from=' + encodeURIComponent(from))`. It is a
    link or plain navigation, not a `<form>`, because of the `form-action 'self'` CSP.
  - `identity-platform` keeps the existing e-mail/password form, calls `signInPassword`, then
    `loginWithIdToken`. It also shows "Continue with Google" when that method is enabled, and a
    "Forgot password" link that calls `sendReset`.
  - Show a banner for the `?error=` codes from Phase 2.
- **`RegisterPage`:** `oidc` redirects to `/login`, because sign-up happens on the IdP's hosted
  page. `identity-platform` uses `signUpPassword` and then `loginWithIdToken`.
- **Tests:** in `LoginPage.test.tsx` and `RegisterPage.test.tsx`, add per-mode cases, with
  `vi.mock('../auth/identityPlatform')`. Extend `mockApi.ts` for the config variants.
- **Bundle check:** after `npm run build`, confirm that Firebase code is in a separate lazy chunk
  and not in the entry chunk.

## Phase 5: Local OIDC stack and end-to-end check

- `deploy/keycloak/realm-cloud-notes.json` defines realm `cloud-notes`; a confidential client
  `cloud-notes` with PKCE S256, redirect `http://localhost:3000/api/auth/oidc/callback`, post-logout
  redirect `http://localhost:3000/login`, and a secret marked LOCAL-ONLY; and a test user
  `student@example.com` with an e-mail and a local-only password.
- `docker-compose.oidc.yml` is an override file:
  - A `keycloak` service with a pinned exact tag, `start-dev --import-realm`, published on
    `127.0.0.1:8081:8080`, with `KC_HOSTNAME=http://localhost:8081`,
    `KC_HOSTNAME_BACKCHANNEL_DYNAMIC=true` and a healthcheck.
  - Backend env: `AUTH_PROVIDER=oidc`, `OIDC_ISSUER_URL=http://localhost:8081/realms/cloud-notes`,
    `OIDC_DISCOVERY_URL=http://keycloak:8080/realms/cloud-notes`, the client ID and secret, and
    `PUBLIC_APP_URL`. The backend uses `depends_on` on Keycloak being healthy.
- **Makefile:** `make up-oidc` runs `docker compose -f docker-compose.yml -f docker-compose.oidc.yml up -d --build`.
  Add `make smoke-oidc`, and add both to `make help`.
- **`scripts/smoke-test-oidc.sh`** (bash + curl + jq, shellcheck-clean). With a cookie jar:
  - GET `/api/auth/oidc/login`, following redirects, and extract the Keycloak login form's
    `action` (HTML-unescape `&amp;`).
  - POST the username and password, and follow redirects back to the app.
  - Assert that `GET /api/auth/me` returns 200 with the test e-mail, that creating and listing a
    note works, and that logout returns a `logout_url`.

## Phase 6: Documentation

- **New `docs/authentication.md`.** It covers the provider model, the sequence diagrams (Mermaid,
  matching `architecture.md`) for `oidc` and `identity-platform`, and the security decisions:
  BFF, no linking by e-mail, `auth_time` freshness, in-memory Firebase persistence, the Lax state
  cookie. It has one **step-by-step setup section per cloud**, with console steps and the exact
  env vars:
  - **AWS Cognito.** Create a user pool, a managed login domain, and an app client *with a
    secret*. Callback `https://<app>/api/auth/oidc/callback`, sign-out URL
    `https://<app>/login`, scopes `openid email profile`.
    `OIDC_ISSUER_URL=https://cognito-idp.<region>.amazonaws.com/<pool-id>`.
    `OIDC_LOGOUT_URL=https://<domain>/logout?client_id={client_id}&logout_uri={post_logout_redirect_uri}`.
  - **Azure Entra External ID.** Create an external tenant and an app registration (Web
    platform) with a redirect URI, a client secret and front-channel logout URL. Create a
    sign-up/sign-in user flow linked to the app, and add the optional `email` claim.
    Issuer `https://<sub>.ciamlogin.com/<tenant-id>/v2.0`. Note that B2C is closed to new
    customers.
  - **GCP Identity Platform.** Enable it, enable the Email/Password provider (and optionally
    Google), add the app's domain to the authorised domains, take the Web API key (restrict it by
    HTTP referrer) and the authDomain. Set the `IDP_*` env vars on the backend, and
    `FRONTEND_AUTH_PROFILE` / `IDP_AUTH_DOMAIN` on the frontend.
  - **Troubleshooting table.** Issuer mismatch, a missing e-mail claim, a redirect URI mismatch,
    the CSP blocking Firebase, and clock skew.
- **Update:**
  - `docs/configuration.md`: every new variable.
  - `docs/api/openapi.yaml`: the new endpoints and the `/config` shape.
  - `docs/architecture.md`, `README.md` and `docs/security.md`: remove "no external identity
    provider" wording and describe the three modes.
  - `docs/deployment-guide.md`: add a "User sign-in" row (Cognito | Identity Platform | Entra
    External ID) to the service-mapping table, and the secret `OIDC_CLIENT_SECRET`.
  - `backend/.env.example` and both component READMEs.

## Critical files

- Backend: `internal/config/config.go`, `internal/auth/{service,handlers,cookie}.go`, a new
  `internal/auth/{oidc,identityplatform}.go` and `internal/auth/oidctest/`,
  `internal/database/{user_repo,identity_repo}.go` and migration `000002`,
  `internal/handlers/router.go`, `cmd/api/{main,createuser}.go`.
- Frontend: `src/api/{types,auth}.ts`, `src/context/{AuthProvider.tsx,authContext.ts}`,
  `src/pages/{LoginPage,RegisterPage}.tsx`, a new `src/auth/identityPlatform.ts`,
  `nginx/docker-entrypoint.d/05-validate-env.sh`, `nginx/snippets/security-headers.conf`.
- Infra/docs: `docker-compose.oidc.yml`, `deploy/keycloak/`, `Makefile`,
  `scripts/smoke-test-oidc.sh`, `docs/*`.

Reuse rather than rewrite: `Service.issue()`, `SetSessionCookie`/`ClearSessionCookie`,
`RateLimiter`, `ClientIP`, `NormalizeEmail`, `httpx.WriteError`, `loader.secret` (for `_FILE`
support), the frontend's `safeRedirect`, `apiFetch` and `ApiError`.

## Constraints for the executor

- `AUTH_PROVIDER=local` behaviour, API responses and all existing tests must not change, apart
  from the additive `auth` field in `/config`.
- No cloud SDKs in the backend. The only new Go dependencies are `go-oidc/v3` and `x/oauth2`.
  Pin exact versions, run `make vuln`, and match the repo's dependency pinning style (`~` in
  `package.json`).
- Never log tokens, codes or the client secret. Never put error details in redirect URLs.
- Match the existing code style: comment density, table tests, error collection in config.

## Verification

1. `make lint && make test`, then `make test-integration` (migration 000002 and `UpsertExternal`).
2. `make up && make smoke`: local mode is unchanged.
3. `make clean && make up-oidc && make smoke-oidc`: a full redirect login through Keycloak. Also
   click through manually at http://localhost:3000: sign in, create a note, log out, and land on
   the Keycloak logout and then back on `/login`.
4. `cd frontend && npm run build`: Firebase is in a separate chunk. Run the frontend container
   with `FRONTEND_AUTH_PROFILE=none` and diff its response headers against the current ones; they
   must be identical.
5. Identity Platform cannot be exercised without a GCP project. It is covered by the Go verifier
   tests and the mocked frontend tests. Document a manual checklist in `docs/authentication.md`
   for the first GCP student or instructor to run.

---

## Appendix: prompt for the Sonnet implementation session

```text
You are implementing a planned feature in the repository dumi-turcanu/cloud-notes, on branch
claude/happy-euler-yl439f (check it out; create it from origin if it is missing locally).

The complete, approved implementation plan is in docs/plans/cloud-agnostic-auth.md. Read it in
full before you do anything. It is the spec: follow its design decisions, names, env vars,
endpoints and constraints exactly. Do not re-research or redesign. If the plan is truly
ambiguous or wrong somewhere, choose the option that keeps AUTH_PROVIDER=local behaviour unchanged
and is the most secure, record the decision in the commit message, and continue. Stop and ask me
only if you are blocked.

How to work:
1. Do the phases in order (1 → 6). Before each phase, read the files listed under "Read first"
   for it, and match the surrounding code style: comment density, table-driven tests, error
   handling, naming.
2. After each phase, run `make lint` and `make test` from the repo root (and
   `make test-integration` after Phase 1). Fix every failure before you move on. Never skip,
   disable or weaken an existing test.
3. Commit each phase separately with a clear message ("auth: …", "frontend: …",
   "docs: …"), then push with `git push -u origin claude/happy-euler-yl439f`.
4. Pin exact versions for new dependencies (coreos/go-oidc/v3, golang.org/x/oauth2, the
   firebase npm package with `~`, and an exact Keycloak image tag). Run `make vuln` after adding
   the Go dependencies.
5. Never log or put in URLs: tokens, authorization codes, client secrets or error details.
6. Phase 5 needs Docker. If Docker is not available in your environment, still write every file,
   validate the shell script with shellcheck and the compose file with
   `docker compose -f docker-compose.yml -f docker-compose.oidc.yml config` if possible, and say
   clearly in your final report that the end-to-end Keycloak run was not executed.
7. Do not open a pull request unless I ask.

When you finish, report:
- each phase and its commit hash;
- the commands you ran and their results (paste failures verbatim if any remain);
- any deviation from the plan and why;
- what still needs manual verification (the GCP Identity Platform checklist and, if skipped, the
  Keycloak end-to-end run).
```
