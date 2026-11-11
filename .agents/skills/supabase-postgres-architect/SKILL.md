---
name: supabase-postgres-architect
description: Design and review Supabase user JWT verification, private PostgreSQL schema and pgx access, transactional room persistence, and asynchronous JSONB snapshots for this game backend.
---

# Supabase and PostgreSQL architecture

Use [the architecture specification](../../../BACKEND_PLAN.md) for complete SQL and recovery contracts.

- Verify Supabase access-token signatures with a configured issuer/JWKS and algorithm allowlist; check `exp`, `nbf` when present, audience, UUID `sub`, and authenticated user role. Never trust client user IDs.
- Anonymous Auth users have real UUID identities and authenticated-role JWTs. The public `anon` API key is not a guest identity. Do not require email for guests.
- Cache JWKS with bounded expiry/refresh and unknown-key throttling. Pin the configured JWKS URL; reject token-controlled key URLs and algorithm confusion. Legacy HMAC, if enabled, is an explicit separate configuration.
- Keep browser tokens and room passwords out of URLs and logs. Use short-lived, single-use WebSocket admission credentials bound to user, room, and token expiry.
- Use salted bcrypt for room passwords, rate-limit verification, and never reveal hashes. Treat admission grants as short-lived credentials that cannot override membership or room phase.
- Use parameterized pgx/v5 statements, bounded connection pools/timeouts, and short transactions. Do not assume a direct PostgreSQL connection inherits a user's JWT/RLS context.
- Enable RLS and revoke client grants on authoritative tables. Backend-only policies/credentials must be explicit; never make full JSONB snapshots readable to members.
- Atomically persist room metadata, roster, full snapshot, and command receipt. Version snapshots by every mutation, not only turns; reject stale writes and fence old room owners.
- Persist deck order, discard, pending decisions, final-round counters, and idempotency receipts. Publish success after durable commit; explicitly handle ambiguous commit results before retrying.
- Test constraints, client denial, rollback, out-of-order writes, restart recovery, and credential rotation against isolated infrastructure.
