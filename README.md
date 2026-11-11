# Ticket to Ride Europe backend

Phase 1 implements a Gin server, Supabase access-token verification, and an embedded, validated Europe catalog. The [backend plan](BACKEND_PLAN.md) defines the remaining room, WebSocket, persistence, and gameplay phases.

The local module path is `ticket-to-ride` because this workspace has no Git origin. Replace that path and its imports together when an actual repository URL is established. The module requires Go 1.26 because the patched HTTP dependencies require it; `go.mod` selects patched Go 1.26.10 automatically through Go toolchain management. No global Go installation is replaced.

## Run

Copy the configuration values from [.env.example](.env.example) into your shell or deployment environment; the server deliberately does not auto-load `.env` files. A minimal PowerShell example:

```powershell
$env:SUPABASE_URL = 'https://YOUR_PROJECT.supabase.co'
go run ./cmd/server
```

The project must use an asymmetric Supabase signing key compatible with the configured `ES256,RS256` allowlist. The pinned discovery endpoint defaults to `<SUPABASE_URL>/auth/v1/.well-known/jwks.json`. Set `SUPABASE_JWT_ISSUER` and `SUPABASE_JWKS_URL` explicitly for a custom Auth domain. HTTP is permitted only on loopback for local Supabase/testing; remote endpoints require verified HTTPS. [Supabase signing keys](https://supabase.com/docs/guides/auth/signing-keys)

For an existing legacy project, explicitly set `SUPABASE_ENABLE_LEGACY_HS256=true`, `SUPABASE_JWT_ALGORITHMS=HS256`, and the server-only `SUPABASE_LEGACY_JWT_SECRET` (at least 32 bytes). Legacy mode is isolated; it cannot be mixed with asymmetric algorithms or used as fallback. Never put the signing secret in browser configuration.

| Endpoint | Access | Result |
| --- | --- | --- |
| `GET /health/live` | public | process is serving |
| `GET /health/ready` | public | catalog loaded, nonexpired auth trust available, not draining |
| `GET /v1/catalog/europe-core-v1` | public | cities, routes, simplified rules, scoring table, ticket counts, hash, ETag; no ticket identities |
| `GET /v1/me` | bearer access token | verified UUID identity, guest flag, and expiry |

Email and Anonymous Auth users use the same endpoint with their Supabase user **access token**. Guests need to enable Anonymous Sign-Ins and call Supabase `signInAnonymously()` in the client. A publishable/anon API key is not a user identity. No email is required for guests. [Supabase anonymous authentication](https://supabase.com/docs/guides/auth/auth-anonymous)

```powershell
Invoke-RestMethod 'http://127.0.0.1:8080/health/ready'
Invoke-RestMethod 'http://127.0.0.1:8080/v1/me' -Headers @{ Authorization = "Bearer $env:SUPABASE_ACCESS_TOKEN" }
```

JWT verification requires a valid signature, configured issuer and authenticated audience/role, nonzero UUID subject, expiry, and optional not-before claim. There is 30-second clock leeway. JWKS trust expires after five minutes by default (maximum ten); a shared refresh and ten-second minimum refresh interval bound unknown-key traffic. Key retrieval cannot follow redirects or token-supplied URLs, and stale keys are never accepted after expiry. A newly rotated key may be temporarily rejected during the refresh throttle interval. Keep old signing keys valid through the rotation overlap. Local verification cannot instantly detect remote logout; expiry/cache lifetimes bound that behavior.

The server fails startup if configuration, catalog validation, or initial trust loading fails. Readiness probes refresh expired trust through the same bounded cache. On termination it marks readiness unavailable and drains HTTP requests within the configured timeout. `127.0.0.1:8080` is the default bind; use `HTTP_ADDR=0.0.0.0:8080` explicitly for containers behind HTTPS termination. Proxy IP headers are untrusted by default. No database credentials or live project mutation are needed for Phase 1.

## Catalog

The catalog contains 47 cities, 101 route edges (11 parallel pairs), and 46 destination tickets (40 regular, 6 long). It retains original tunnel/ferry metadata; `europe-core-v1` treats all those routes as ordinary claims, with stations disabled. The public API's `rules` flags describe these simplifications.

See [catalog provenance and corrections](assets/europe-core-v1/PROVENANCE.md). The single authoritative asset pair is `catalog.json` plus `catalog-manifest.json`, embedded independently of working directory. JSON files must retain LF line endings because the manifest hashes their exact bytes; `.gitattributes` enforces that on Git checkout. Any reviewed asset change must update the manifest hash and compatibility contract. Catalog getters and the public map projection return copies so callers cannot mutate shared board data. HTTP ETags hash the response bytes, so response-schema changes invalidate cached views even if the underlying catalog is unchanged.

## Verify

```powershell
go test ./...
go vet ./...
go test -race -shuffle=on ./...
go test ./internal/auth -run '^$' -fuzz FuzzVerifyDoesNotPanic -fuzztime 5s
go test ./internal/game/catalog -run '^$' -fuzz FuzzCatalogDecoder -fuzztime 5s
go run golang.org/x/vuln/cmd/govulncheck@v1.9.0 ./...
```

On Windows, race testing requires a Go-compatible 64-bit C compiler. This machine's default `C:\MinGW` compiler is 32-bit. The Phase 1 race suite passed using a publisher-checksummed portable w64devkit 2.11.0 x64 compiler in the OS temporary directory; no system installation was changed. For a portable compiler, set both its executable and tool directory in the current shell:

```powershell
$env:CC = 'C:\path\to\w64devkit\bin\gcc.exe'
$env:PATH = 'C:\path\to\w64devkit\bin;' + $env:PATH
go test -race -shuffle=on ./...
```

Linux CI also includes the mandatory race check. Tests generate signing keys and run local JWKS/HTTP servers; they do not contact the user's Supabase project. They cover email/guest identities, forged tokens, rotation, concurrent refresh, stale trust denial, malformed catalogs, immutable graph access, public-view privacy, cache headers, and shutdown readiness. Actual Phase 1 verification is recorded in [the implementation record](BACKEND_PLAN.md#12-phase-1-implementation-record-2026-11-11).

Database health checks and migrations arrive in Phase 2; WebSocket admission, CORS/Origin policy, room actors, and gameplay arrive in subsequent phases. Readiness in this foundation phase therefore checks catalog/auth only. No gameplay state, password storage, or private snapshots are exposed by this server.
