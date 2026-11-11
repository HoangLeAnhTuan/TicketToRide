---
name: golang-concurrency-pro
description: Build and review single-writer Go room actors, safe WebSocket pumps, bounded asynchronous persistence, synchronization, and cancellation for this multiplayer backend.
---

# Go concurrency for rooms

Use [the architecture specification](../../../BACKEND_PLAN.md) for ownership and recovery semantics.

- Exactly one actor goroutine owns a room's mutable lobby/game state. REST, WebSocket, timer, persistence, and scoring work submit typed messages to that actor.
- Use `sync.RWMutex` only for short Hub registry/cache access. Never hold a Hub lock while sending a channel message, waiting for I/O, or invoking a callback.
- One read pump and one write pump per Gorilla connection; route application writes, ping frames, and close sequencing through the write pump.
- Never share actor-owned slices, maps, pointers, or mutable RNGs with workers. Deep-copy/encode immutable jobs and per-user DTOs before handoff.
- Bound inboxes, outgoing queues, worker concurrency, and pending requests. Reject overload or disconnect slow peers; never spawn a goroutine per failed send.
- Keep one candidate state-changing command in flight per room while awaiting asynchronous database persistence. Keep processing transport/timer/control messages; publish authoritative success only after commit.
- Include connection generation in disconnect events; an old socket cannot disconnect a replacement. Include state version/job ID in worker completions; reject stale results.
- Assign each channel a single closing owner. Cancel workers, stop timers, drain/abort by deadline, close sockets, and wait for tracked goroutines during shutdown.
- Test reconnect/detach races, concurrent commands, shutdown, backpressure, stale callbacks, and goroutine cleanup under the race detector.
