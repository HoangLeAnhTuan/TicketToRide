---
name: impeccable
description: Design, implement, and review idiomatic Go domain boundaries, deterministic state transitions, explicit errors, and invariant tests for this authoritative board-game backend.
---

# Impeccable Go backend

Use [the architecture specification](../../../BACKEND_PLAN.md) as the project contract.

- Keep `internal/game` independent of Gin, WebSockets, PostgreSQL, and authentication libraries. Define interfaces at the consuming boundary; prefer concrete types elsewhere.
- Inject time, IDs, and randomness. Use stable seat/route ordering; map iteration must never decide turns, hosts, payments, or scores.
- Validate the complete command before mutation; apply to an isolated candidate and discard it on any failure. Rejected commands must preserve every domain invariant.
- Return typed domain errors with stable public codes; wrap operational errors with `%w`. Never ignore errors or expose SQL, secrets, or panic details to clients.
- Treat phases, pending choices, final-round counters, and idempotency receipts as persisted state. Keep wire DTOs separate from authoritative structs.
- Use `gofmt`, narrow package responsibilities, contexts at I/O boundaries, and structured `slog` logging with redaction.
- Test meaningful behavior: conservation of 110 train cards, 45 trains per player, double-route legality, hidden information, final-round boundaries, and snapshot round trips.
- Use table-driven tests and fuzzing for parsers/state transitions; avoid tests that only repeat implementation details. Run `go test ./...`, `go vet ./...`, and `go test -race ./...` when applicable.
- State measured guarantees and limits. Race-free design is a requirement; a race-test pass alone is not proof of every execution.
