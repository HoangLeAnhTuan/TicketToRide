# Ticket to Ride Europe backend workspace

This repository implements an authoritative Go backend for a simplified Europe ruleset.
Apply these project-local skills whenever designing, implementing, or reviewing the backend:

- [impeccable](.agents/skills/impeccable/SKILL.md): domain boundaries, deterministic Go, errors, and tests.
- [golang-concurrency-pro](.agents/skills/golang-concurrency-pro/SKILL.md): room actors, ownership, bounded queues, and lifecycle.
- [supabase-postgres-architect](.agents/skills/supabase-postgres-architect/SKILL.md): verified identity, private data, and durable snapshots.

These are locally authored standards matching the requested names; they are not claims of installing third-party packages.
Read [BACKEND_PLAN.md](BACKEND_PLAN.md) before implementation. It defines the protocol, ruleset deviations, schema, and phase gates.
The user authorized Phase 1 on 2026-11-10: module foundations, Supabase Auth middleware, and the Europe static graph loader.
The user subsequently authorized continuing backend work. Implement Phase 2 next; frontend files belong to other agents and must not be edited for backend tasks.
Later phase transitions follow the user's authorization in the conversation; do not infer a recurring approval requirement.

All gameplay and lobby mutations go through their room actor, including mutations originating in REST handlers.
Never serialize authoritative state into a client response. Project a separate recipient-specific DTO.
Never expose full snapshots through Supabase Data API, Realtime, logs, or debugging endpoints.
Keep credentials out of source control. Do not modify an external Supabase project merely to validate a plan.
