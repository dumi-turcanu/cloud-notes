# Security

This document describes how Cloud Notes protects its users and data, what it deliberately does
not do, and what the deployment must add. The application handles credentials and private notes,
so it should be deployed following Zero-Trust principles: HTTPS only, no public path to the backend
or database, secrets from a secret manager and least-privilege identities.

## Authentication design

Three sign-in modes share one session layer (`AUTH_PROVIDER`, details in
[authentication.md](authentication.md)): built-in e-mail and password (`local`, the default and the
subject of the table below), an OpenID Connect provider such as AWS Cognito or Azure Entra External ID
(`oidc`), and GCP Identity Platform (`identity-platform`). In every mode the browser holds only the
session cookie below: no token that JavaScript can read is ever stored.

With an external provider the application keeps no passwords; credentials, MFA and account recovery
are the provider's. The application keys users on `(issuer, subject)` and **never links an external
identity to an existing account by e-mail** (a clash is refused with `account_conflict`), verifies the
ID token (signature, issuer, audience, expiry, nonce; Identity Platform also `auth_time` ≤ 5 minutes,
tenant and sign-in method), ignores an e-mail address the provider marks unverified, never logs or
redirects with tokens, codes, secrets or provider error text, and rate-limits the callback. Who may
create an account is decided in the provider, not by `AUTH_SIGNUP_MODE`.

| Aspect | Design |
| --- | --- |
| E-mail | Trimmed, lower-cased, validated, at most 254 characters, unique. |
| Password policy | 12–128 characters, must not equal the e-mail address. No composition rules (length matters more). |
| Password hashing | **Argon2id**, PHC string `$argon2id$v=19$m=19456,t=2,p=1$<salt>$<hash>`: 19 MiB memory, 2 iterations, 1 lane, 16-byte random salt, 32-byte key. Parameters are read from each stored hash and a hash with outdated parameters is re-hashed after a successful login. Comparison is constant-time. At most 4 hash operations run concurrently per instance, which bounds memory use. |
| User enumeration | Login answers `invalid_credentials` for an unknown e-mail and a wrong password alike, and verifies against a dummy hash for unknown users so the response time does not reveal which e-mails exist. Registration necessarily reveals taken e-mails (`email_taken`) when sign-up is enabled. |
| Sessions | Server-side, in PostgreSQL. The cookie holds 32 random bytes (`crypto/rand`, base64url); the database stores only the **SHA-256 of the token**, so a database leak does not yield usable session cookies. Logging in rotates the session (an existing session on the request is revoked first). Absolute lifetime `SESSION_TTL` (default 12 h) and idle timeout `SESSION_IDLE_TIMEOUT` (default 1 h); expired rows are removed by a background job every 15 minutes. |
| Cookie | `__Host-cn_session` (the `__Host-` prefix forces `Secure`, `Path=/` and no `Domain`), `HttpOnly`, `Secure`, `SameSite=Strict`, `Max-Age` equal to the remaining lifetime. Plain-HTTP development (`SESSION_COOKIE_SECURE=false`) uses the name `cn_session` without `Secure`. |
| Cookie-less sign-in state | In `oidc` mode a short-lived `__Host-cn_oidc` cookie (`HttpOnly`, `SameSite=Lax`, 10 minutes, single use) carries `state`, `nonce` and the PKCE verifier between the redirect and the callback. |
| Authorization | Every note query is scoped to the signed-in user; other users' notes answer `404`. Public sharing exposes only title, content and update time of explicitly published notes, addressed by an unguessable UUID. |
| Caching | `/api/auth/*` and all authenticated responses carry `Cache-Control: no-store`. |

### Account lockout and rate limiting

Two independent layers protect the login against guessing:

1. **Account lockout (stored in PostgreSQL, effective across all replicas).** After
   `AUTH_MAX_FAILED_LOGINS` consecutive failures (default 5) the account is locked for
   `AUTH_LOCKOUT_DURATION` (default 15 minutes). While locked the API answers `429 too_many_attempts`
   with a `Retry-After` header and does not check the password. A successful login resets the counter.
   A side effect: someone who knows an e-mail address can lock that account temporarily. This
   trade-off is accepted; the WAF layer below limits how often it can be repeated.
2. **Per-IP rate limit (in memory, per instance, best effort).** `AUTH_RATE_LIMIT_PER_MINUTE`
   requests per client IP and minute (default 10) on login and register, answered with
   `429 too_many_requests` and `Retry-After: 60`. With several replicas the effective limit is the
   value times the number of replicas. The client IP depends on `TRUSTED_PROXY_HOPS`
   ([deployment guide](deployment-guide.md#client-ip-and-trusted_proxy_hops)); a wrong value either
   lets clients spoof their address (too high) or puts them in a shared bucket (too low).

The built-in limits are a safety net, not a replacement for edge rate limiting.

### CSRF protection

The whole `/api` handler is wrapped in Go's `http.CrossOriginProtection`. It rejects
state-changing requests (anything except `GET`, `HEAD`, `OPTIONS`) that a browser marks as
cross-site: it uses the `Sec-Fetch-Site` header and falls back to comparing the `Origin` header with
`Host`; requests carrying neither header (non-browser clients such as `curl`) pass. Rejections are
answered with `403 forbidden` (JSON). Together with the `SameSite=Strict` cookie and the absence
of CORS this blocks cross-site request forgery. `CSRF_TRUSTED_ORIGINS` adds origins that may
legitimately send `Origin`-only requests — set it to your public origin when the backend sits
behind proxies (the `Host` it sees is internal).

### Sign-up

`AUTH_SIGNUP_MODE` only applies to `AUTH_PROVIDER=local`; with an external provider restrict
sign-up in the provider (see [authentication.md](authentication.md)). `AUTH_SIGNUP_MODE=disabled` is the default. Use `invite` (a secret code of at least 16 characters,
compared in constant time) or create users with `cloud-notes-api create-user`. `open` is for local
development only.

### Browser-side protections

The frontend container sends a strict `Content-Security-Policy` (`default-src 'self'`; no inline
scripts or styles, no third-party hosts, `frame-ancestors 'none'`), `X-Content-Type-Options: nosniff`,
`Referrer-Policy`, `Permissions-Policy`, `Cross-Origin-Opener-Policy` and
`Strict-Transport-Security`. Note text is rendered as plain text; the SPA never injects HTML.

### Logging

Passwords, cookies, session tokens, invite codes and request bodies are never logged. Authentication
events are logged with the user id (not the e-mail); every request has a `request_id`. Treat logs as
sensitive anyway (client IPs, user ids).

## Threat model summary

| Threat | Mitigation in the application | What the deployment must provide |
| --- | --- | --- |
| Credential stuffing / brute force | Argon2id, lockout, per-IP limit, generic login errors (`local`); the provider's protections plus a per-IP limit on the callback (`oidc`, `identity-platform`) | WAF rate-limiting rules on `/api/auth/*`; alarms on `auth_login_attempts_total` |
| Account takeover through a matching e-mail at an identity provider | Users keyed on `(issuer, subject)`; no linking by e-mail, a clash is `account_conflict`; unverified e-mail ignored | Restrict who can sign up in the provider; require verified e-mail there |
| Login CSRF / authorization-code injection (`oidc`) | PKCE S256, `state` bound to the browser by a cookie, `nonce`, single-use state | HTTPS and exact redirect URIs registered at the provider |
| Replay of a captured Identity Platform token | `auth_time` ≤ 5 minutes, expiry, audience and issuer checks | Restrict the Web API key by HTTP referrer |
| Stolen database / backup | Only password hashes and SHA-256 session hashes are stored | Encryption at rest, access control, backup protection |
| Session theft | `HttpOnly`, `Secure`, `SameSite=Strict`, `__Host-` cookie, short idle timeout, CSP | HTTPS everywhere, HSTS at the edge |
| CSRF | `CrossOriginProtection`, `SameSite=Strict`, no CORS | Do not add permissive CORS headers at the edge |
| XSS | Plain-text rendering, strict CSP | Do not weaken the CSP headers |
| SQL injection | Parameterised queries only | — |
| Access to other users' notes | Queries always filtered by the session's user | — |
| Unauthenticated use of the app | Closed sign-up, all note routes require a session | Keep sign-up `disabled` or `invite`; lock down the origin |
| Direct access to origin or backend | None (network-level concern) | Private subnets, security groups, internal ingress, origin lock-down |
| Secrets in code or logs | `*_FILE` support, no secrets in logs or images | Secret manager, no secrets in IaC state or pipeline output |
| Vulnerable dependencies / images | Minimal distroless and nginx-unprivileged images, non-root, read-only file system compatible | Image and dependency scanning, blocking deployments with fixable HIGH/CRITICAL findings |
| Denial of service | Request size limits (256 KiB API, 1 MiB nginx), server timeouts, bounded hashing concurrency | CDN/WAF protection, autoscaling, connection limits on the database |
| Public sharing abuse | Off by default (`FEATURE_PUBLIC_SHARING`); shares are unlisted UUID links | Decide consciously before enabling; anyone with the link can read the note |

## Known limitations

These are deliberate scope decisions; the application does not provide the following.

- **No e-mail verification** in `local` mode. Addresses are not confirmed; any syntactically valid
  address is accepted. (With an external provider, verification and MFA are the provider's job; Identity
  Platform password sign-ups are not verified unless you add that to the project.)
- **No password reset or change** in `local` mode. A forgotten password is replaced by an operator (for a
  new account via `create-user`). There is no self-service flow, no e-mail sending. (Identity Platform
  mode has "Forgot password?" through the provider; Cognito and Entra offer recovery on their pages.)
- **No multi-factor authentication** in `local` mode; enable it in the identity provider otherwise.
- **No user management UI or API.** No user deletion, no session listing or "sign out everywhere".
- **Per-instance rate limiter.** Limits are not shared between replicas, and a restart clears them.
- **Lockout can be abused** to lock a known user out temporarily (see above).
- **No audit trail** beyond application logs. Soft-deleted notes are kept until you purge them.
- **Public share links are bearer links**, not access controlled, and unpublishing does not recall
  copies already made.
- The local Docker Compose stack uses plain HTTP, open sign-up and a throw-away database
  password. It is not a model for deployment.

## Hardening checklist

For anyone deploying the application to a shared or public environment.

- [ ] Sign-up is `disabled` (or `invite` with a secret code); `SESSION_COOKIE_SECURE` is `true`. With
      `AUTH_PROVIDER=oidc` or `identity-platform`, self-service sign-up is restricted in the identity provider
      and `OIDC_CLIENT_SECRET` comes from the secret manager.
- [ ] HTTPS only; the edge redirects HTTP to HTTPS and sends HSTS.
- [ ] **WAF rate limiting** on `/api/auth/*` (and a general request rate limit) at the edge.
- [ ] Frontend origin reachable only from the edge; backend and database have no public address.
- [ ] Security groups / firewall rules / NetworkPolicies allow only the flows listed in the
      [deployment guide](deployment-guide.md#networking); the metrics port `9090` is not routed to the edge.
- [ ] Database: TLS enforced, `DB_SSLMODE=verify-full` with the provider CA, encryption at rest,
      automated backups with tested restore, no public IP.
- [ ] **Least-privilege database roles:** a *migration owner* (DDL rights) used only by the
      `migrate` job and a *runtime user* with `SELECT`, `INSERT`, `UPDATE`, `DELETE` on the application's
      tables (no DDL, not a superuser). Run the backend with `DB_MIGRATE_ON_START=false` and the runtime user.
- [ ] Secrets in the cloud secret manager, injected at runtime (env or `*_FILE`); none in Git, images, IaC
      state, or pipeline logs. **Rotate** the database password, invite code and the bootstrap password
      on a schedule and after any suspected exposure (restart the backend to pick up changes; existing
      sessions stay valid because they do not depend on these secrets).
- [ ] Workloads use cloud identities (IAM roles, service accounts, managed identities) with
      least-privilege policies; no static cloud keys in containers; no wildcard admin policies.
- [ ] Containers run as non-root with a read-only root file system, dropped capabilities and no
      privilege escalation (on Kubernetes via the pod `securityContext`; replicate them on serverless platforms).
- [ ] **Image scanning** (OS packages and dependencies) in CI; block images with fixable HIGH/CRITICAL
      findings; scan the repository's source and IaC; tag images immutably; pin base images by
      digest if you can; consider signing images and generating SBOMs.
- [ ] CI uses keyless OIDC federation to the cloud and third-party CI actions pinned to a commit SHA.
- [ ] Logs from both containers go to central logging with restricted access and a retention
      period; alerts on 5xx rates, login failures (`auth_login_attempts_total`) and `/readyz` failures.
- [ ] Dependencies are updated regularly (Dependabot or Renovate) and the pipeline fails on known
      vulnerabilities.
- [ ] `FEATURE_PUBLIC_SHARING` stays `false` unless public links are a conscious requirement.
