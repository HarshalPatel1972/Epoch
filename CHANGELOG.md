# Changelog

## v2.0.0 (unreleased)

Epoch v2 is a rewrite. v1 was a demo HTTP server for one hard-coded product type; v2 is a general-purpose Go library that records commands as well as events, so history can be branched and re-run under new business rules. The v1 code has been removed from the repository and is preserved at the `v1-final` tag.

### Added

- `Model[S]`: business logic as pure `Decide` and `Evolve` functions, with typed commands and events, optimistic concurrency with automatic retries, recorded rejections, idempotent command IDs, recorded facts, and schema-keyed snapshots.
- Time travel with `AsOf` and `AtSeq` for entities, logs and projections.
- Branches: `Fork` creates durable, nestable, copy-on-write branches.
- `Replay`: re-decides recorded commands with new rules on a branch and reports every changed outcome. `ReplayReport` rebuilds the report from stored data.
- `Projection[V]`, `Compare` and a structural JSON `Diff`.
- Stores: `memstore` and `sqlitestore` (pure Go). `epochtest` is a conformance suite and benchmark set for store authors.
- `epochhttp`: a JSON API and the embedded Epoch Studio web UI.
- `examples/shop`: a guided tour with six months of generated history.

### Fixed in v1 before the rewrite

- Forks read main-timeline snapshots taken after the fork point and dropped their own writes once an entity had ten or more events.
- Point-in-time reads from BadgerDB decoded an entity's entire history instead of only the events after the snapshot.
