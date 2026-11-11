# Europe base-game catalog provenance

Ruleset: `europe-core-v1`, schema 1. Reviewed on 2026-11-10. This is the original Europe base game, without 1912/anniversary expansion tickets. The JSON contains factual board relationships, colors, lengths, locomotive counts, and ticket values; no source artwork or third-party implementation code is bundled.

## Sources and review

- Primary visual authority: the publisher's [English Europe rulebook](https://cdn.svc.asmodee.net/staging-daysofwonder/uploads/2024/07/7202-T2RE-Rules-EN.pdf), PDF spread 2, depicting the complete board. The original PDF was downloaded temporarily, rendered, and its board image inspected for every pair's color, multiplicity, length, tunnel border, and ferry symbols. PDF SHA-256: `4c1da380a3c396727404a523ac93ab40e2b3ca2f497c847964c19827425d586d`.
- Candidate city/route/ticket facts and physical ticket photograph: [leonsi7/ticket-to-ride-europe](https://github.com/leonsi7/ticket-to-ride-europe/tree/03c8f35ae4a80abf4caab4a9ec7737a15c37b150). CSVs provide 47 cities, 90 endpoint pairs, and 46 tickets. Every regular ticket was checked against the photographed cards; long ticket values and endpoints were checked against the base-game list and visible long cards. Route colors and multiplicity were transcribed from the publisher board rather than inferred from this CSV's `Colored` boolean.
- Independent endpoint/length cross-check: the author-maintained [ticket2ride graph source](https://github.com/ktsiligkiris/ticket2ride/blob/main/src/lib.rs), retrieved file SHA-256 `2e8564aefd40db43332fafb007ddcc507b407d7c8ed76f0668e08cfa6ac09dcc`. This source independently agrees on Berlin–Danzig length 4 and Palermo–Smyrna length 6; its endpoint/length representation does not supply colors or parallel multiplicity. Use the publisher board as authority in disagreements.

Reference checksums (original downloaded bytes):

| Reference at pinned candidate commit | SHA-256 |
| --- | --- |
| `cities.csv` | `a4340caa21f770c704bf0b003b993d9c41f99c7b3657a30b136be669d37e214a` |
| `routes.csv` | `75563066aea7a2e6c9540e29de0c3b7ddf58927d9ca51f6891706092f88fc8b4` |
| `destinations.csv` | `223ff7376b9646e090a1ad29f7b6e8699413d6a8af8ef4b14b38440f3dd05231` |
| `destinations.jpg` | `1810a56f2306b721508838a954880d7782d39e06c6d3c7a4d347a422100616a2` |

## Corrections and normalization

| Candidate data | Reviewed catalog |
| --- | --- |
| Berlin–Danzig length 3 | length **4**, gray ordinary route |
| Palermo–Athina length 6 | **Palermo–Smyrna**, gray ferry, two locomotives |
| Zürich–Marseille marked uncolored | **pink**, length 2 tunnel |
| Stockholm–Wien 10 points | **11** points, regular destination |
| One row per endpoint pair, no color identities | 101 distinct edges with actual colors and 11 explicit two-edge parallel groups |
| `Warzawa`, `Danzic`, `Khobenhaven` spellings | stable ASCII names/IDs `Warszawa`, `Danzig`, `Kobenhavn` |

IDs are lowercase ASCII city names; route IDs are sorted endpoint IDs plus `--1`/`--2`. Ticket IDs are sorted endpoint IDs. A parallel group is its common sorted endpoint pair. Identical gray routes remain distinct edges. Names `Munchen` and `Zurich` are ASCII display forms of the printed names, not extra cities.

Source kinds and locomotive requirements remain metadata for future rulesets. The active simplified rules treat tunnels/ferries as ordinary routes, preserving connectivity. Original ticket points remain unchanged except the corrected transcription above. No stations are represented.

`catalog-manifest.json` stores counts and the exact SHA-256 of `catalog.json`. The loader checks those plus graph invariants; regression tests pin the corrections, parallel identity, and key routes. Changing a ruleset-compatible manifest alone is not evidence that newly edited data was reviewed. Review factual changes and version the ruleset when semantics change.

The superseded split-file catalog (`cities.json`, `routes.json`, `tickets.json`, and `manifest.json`) has been retired. Its 100-route graph omitted the second Essen–Kobenhavn ferry edge. The reconciled source of truth is `catalog.json` plus `catalog-manifest.json`, with both ferry edges and all 101 routes. The HTTP endpoint and catalog helper use the same sanitized map projection; consumers should use that endpoint or the validated loader.
