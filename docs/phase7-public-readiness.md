# Phase 7: Public readiness

Goal: be able to put Agentic AI in front of people we do not know. This file is the
plan, what has been done, what has been proven, and what is still open. It is
written to be checked against the code, not to reassure.

## Strategy

Release in stages, with **code execution last**. Running strangers' code is the
riskiest part of the product, and the Docker sandbox (Phase 6) is one layer, not a
public-grade boundary. A first public release therefore has workspaces, chat, agents
and memory, and keeps `CLOUD_EXECUTION_ENABLED` off. Production mode enforces this:
turning execution on also requires `ALLOW_PUBLIC_CODE_EXECUTION=true`, so it cannot
happen by accident.

| Phase | Content | Exit criterion |
|---|---|---|
| **7** | Hardening and deployment foundations (this file) | A staging stack on a real domain passes the checks below |
| 8 | Product and operations: plans and quotas, abuse tooling, legal pages, support | Terms, privacy, data deletion and a way to suspend an account exist |
| 9 | Closed beta without code execution, load test, outside security review | No open high findings |
| 10 | Code execution for the public: stronger isolation, object storage, quotas | See "Before public code execution" |
| 11 | General release: runbooks, on-call, rollback | A rehearsed incident |

## What Phase 7 changed

### Found by auditing, fixed here
| Finding | Fix |
|---|---|
| CORS hard-coded to `localhost`, with credentials always allowed | Allowlist from `CORS_ALLOWED_ORIGINS`; `*` and non-origins are dropped; credentials only for listed origins; preflight from another origin gets 403 |
| **SSRF**: an AI provider's base URL only had its scheme checked, so any signed-in user could make the server call internal addresses (cloud metadata, the database, other services) via "test provider", chat and the generator | New `internal/safehttp` client checks the address **at connection time** (stops DNS names that resolve internally, DNS rebinding and redirects), ignores proxy settings, refuses https to http redirects. `validBaseURL` also rejects credentials in the URL and, in production, plain http, `localhost` and internal address literals. Local models (Ollama) still work in development |
| No rate limiting on sign-in, registration, imports, chat | Per-address limits before sign-in, per-user limits after, tighter limits on chat, imports and execution |
| No protection against password guessing | Lockout: 5 failures lock an address+email pair for 15 minutes; 50 across all addresses lock the account; a locked pair is refused before the password is checked |
| `POST /admin/login` answered "not an admin account" **before** checking the password, which told anyone which emails exist and are ordinary users | Password first, role second; the failure answer is identical for unknown and known accounts |
| Gin trusted every proxy by default, so `X-Forwarded-For` could be forged to dodge limits | Trusted proxies are explicit (`TRUSTED_PROXIES`, none by default) |
| Logs printed raw paths and query strings (which can carry tokens) | JSON access log of route patterns only; request IDs; never headers, bodies or queries |
| The database connection was always `sslmode=disable` | `DB_SSLMODE`; production requires `require` or stricter, or an explicit `ALLOW_PLAINTEXT_DB=true` for a database on the same private network |
| No request size limits, no server timeouts, no graceful stop | 8 MiB JSON / 64 MiB upload cap, header/read/idle timeouts, graceful shutdown on SIGTERM |
| No readiness signal | `GET /api/ready` checks the database (`/api/health` stays a liveness check) |
| No deployment files | `deploy/Dockerfile.api`, `agentic-ai-fe/Dockerfile`, `deploy/compose.staging.yml`, Caddy for automatic TLS |
| The sandbox's "is this address internal" list missed carrier-grade NAT and IPv6 forms that embed IPv4 | `BlockedIP` now covers 100.64.0.0/10, 198.18.0.0/15, 240.0.0.0/4, NAT64, 6to4 and others |

### Production mode
`APP_ENV=production` makes the server refuse to start unless: both JWT secrets are
random, at least 32 characters and different; `PASSWORD_RESET_PEPPER` and
`AI_ENCRYPTION_KEY` are set; `CORS_ALLOWED_ORIGINS` lists https origins;
the database connection uses TLS (or the explicit private-network opt-in). It also
sets Gin to release mode and sends HSTS on TLS requests. The messages name the
variables, never their values.

### Rate limits (per minute, burst)
| Route | Limit | Key |
|---|---|---|
| every request | 600 (120) | address |
| `/auth/login`, `/auth/google` | 10 (10) | address |
| `/auth/register` | 3 (5) | address |
| `/auth/refresh` | 60 (20) | address |
| `/auth/forgot-password/*` | 10 (10) | address |
| `/admin/login` | 5 (5) | address |
| all signed-in routes | 300 (60) | user |
| chat, task messages, generator | 30 (10) | user |
| imports, workspace create/export | 10 (5) | user |
| execution and workspace tools | 20 (10) | user |

An IPv6 address is limited by its /64. Limits are in memory, per process.

## Proven (and how)

- `internal/httpguard`: unit tests for every piece. Nine **mutations** (allow any
  origin, accept `*`, lock threshold off by one, success not clearing, loose request
  IDs, logging the query, HSTS always, body limit off, uncapped token bucket) were
  each caught by a test; one survived at first (the early body-size refusal) and the
  test was tightened until it caught it.
- Real server, run from a compiled binary against a throwaway database: refuses to
  start in production with unsafe settings (all six problems listed); `DB_SSLMODE=require`
  against a server without TLS fails to connect; CORS allowed/denied/preflight;
  12 wrong logins give `401 x5` then `429`; a 9 MiB body gets `413`; a forged
  `X-Forwarded-For` does not reset a limit; the access log contains no query string.
- Real containers: the API image runs as a non-root user with a read-only filesystem
  and all capabilities dropped; the web image runs as non-root nginx, serves the app,
  falls back to `index.html` for deep links, caches hashed assets, returns 404 for
  missing assets, and its build fails without `VITE_API_BASE_URL`.
- The whole staging stack (`compose.staging.yml`, domain `localhost`) was brought up
  and checked: TLS and HSTS through Caddy, CORS through the proxy, `/api/ready`, the
  API and the database are not reachable from the host, a forged `X-Forwarded-For` from
  outside is ignored. The stack was then removed.

## Running it

```
cp deploy/staging.env.example deploy/.env.staging      # fill in; the file is git-ignored
docker compose -f deploy/compose.staging.yml --env-file deploy/.env.staging up -d --build
```

- The compose file expects the frontend repository next to this one
  (`../agentic-ai-fe`). Only the proxy publishes ports (80, 443).
- **The execution worker is not in the compose file**, on purpose: it needs the Docker
  daemon, and giving a container the Docker socket is giving it root on the host. Run it
  on the host as its own service, and only if code execution is enabled.
- Behind any other proxy or load balancer, set `TRUSTED_PROXIES` to its address, or every
  request looks like it comes from the proxy and the per-address limits apply to everyone
  at once.
- On Docker Desktop the API sees the Docker gateway (`172.28.0.1`) as the client address,
  because of its NAT. On a Linux host it sees the real client.
- Run several API instances only after the limits and the lockout move to a shared
  store (Redis); each instance now counts on its own.

## Still open in Phase 7

| Item | Why it matters |
|---|---|
| Email verification and CAPTCHA at registration | Registration is rate limited but not verified; accounts can still be made in bulk with throwaway addresses. Needs a Turnstile/hCaptcha key and a user-model change |
| Versioned migrations (the schema is created by `AutoMigrate`) | Safe schema changes on real data; includes a rollback story |
| Backups and a **tested** restore | Scripts and a restore test exist (`docs/backup-restore.md`); still needed: run them by cron on the server, copy dumps off it, and a home for uploaded files |
| Error tracking, metrics and alerts | Logs exist, nothing watches them |
| Content-Security-Policy for the web app | The app loads Google and Clerk scripts; a policy must be written and tried in a real browser. The nginx config says so and omits it for now |
| CD pipeline (CI exists) | Builds and tests run on GitHub, nothing deploys |
| Session review | Access/refresh token lifetimes, revocation on password change, one place to check |
| Secrets management beyond an env file | Fine for staging, not for production |
| A shared store for limits (Redis) | Required before a second API instance |
| Not yet looked at | Upload handling for skill ZIPs beyond size, Google login edge cases, logs in `handlers` that still use `log.Printf` with endpoints and provider diagnostics |

## Before public code execution (Phase 10)

The Phase 6 document lists what was proven and what was not. In short: the sandbox has
only been run on Docker Desktop; it needs a Linux host test, real disk quotas, a
stronger boundary (gVisor, Firecracker or a managed sandbox) on a dedicated worker
machine, egress firewalling, object storage instead of a local disk, per-user CPU time
budgets, and abuse detection (mining, scanning). Until then
`ALLOW_PUBLIC_CODE_EXECUTION` stays unset.
