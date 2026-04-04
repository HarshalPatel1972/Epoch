# Epoch

An HTTP API server where every endpoint is queryable at any point in its past — and where state can be forked into isolated what-if timelines, mutated independently, and diffed against the original.

```bash
# What did your inventory look like 3 months ago?
curl "http://localhost:8080/products?at=2024-12-01T00:00:00Z"

# What if you had cut prices in January — and how does it compare to today?
curl -X POST http://localhost:8080/timelines/fork \
  -d '{"name":"price-cut","forked_from":"2025-01-01T00:00:00Z"}'

curl -X PUT "http://localhost:8080/products/kb-001/price?timeline=price-cut" \
  -d '{"price": 89.99}'

curl "http://localhost:8080/diff?timeline=price-cut"
```

---

## The Core Idea

Most backends are amnesiac. They know what their state is right now. Ask them what it was last Tuesday and they shrug — that information is gone, overwritten, lost to the last `UPDATE` statement.

Epoch treats every mutation as an **immutable event** appended to a log. The current state of any resource is just the log replayed from the beginning. Point-in-time reads are not a feature bolted on top — they fall out naturally from the architecture.

On top of that, Epoch adds **timeline forking**: the ability to branch the state at any past moment, apply mutations to the branch in isolation, and compare what diverged. It's git branching, but for your API's runtime state.

---

## Demo

### Point-in-time reads

```bash
# Start the server with 6 months of seeded history
go run . -db ./data -seed

# Current product list
curl http://localhost:8080/products | jq '.products[].name'

# Product list as it existed 3 months ago
# (a product that was deleted is still here)
curl "http://localhost:8080/products?at=2024-12-01T00:00:00Z" | jq

# A deleted product — alive in the past
curl "http://localhost:8080/products/keyboard-001?at=2024-10-01T00:00:00Z"
# → 200 OK, full product state

# Same product today
curl "http://localhost:8080/products/keyboard-001"
# → 404 { "error": "product not found at the requested time" }

# MacBook price history
curl "http://localhost:8080/products/macbook-001?at=2024-10-01T00:00:00Z" | jq .price
# → 1999.99
curl "http://localhost:8080/products/macbook-001?at=2025-02-01T00:00:00Z" | jq .price
# → 1749.99
curl "http://localhost:8080/products/macbook-001" | jq .price
# → 1649.99
```

### Timeline forking

```bash
# Create a fork branching from 3 months ago
curl -X POST http://localhost:8080/timelines/fork -H "Content-Type: application/json" \
  -d '{
    "name": "q1-sale",
    "forked_from": "2025-01-01T00:00:00Z",
    "description": "What if we had run a Q1 clearance sale?"
  }'

# Apply mutations to the fork — main timeline is completely untouched
curl -X PUT "http://localhost:8080/products/macbook-001/price?timeline=q1-sale" \
  -H "Content-Type: application/json" -d '{"price": 1299.99}'

curl -X PUT "http://localhost:8080/products/monitor-001/stock?timeline=q1-sale" \
  -H "Content-Type: application/json" -d '{"delta": -30}'

# Query fork state — sees new prices
curl "http://localhost:8080/products/macbook-001?timeline=q1-sale" | jq .price
# → 1299.99

# Query main — unchanged
curl "http://localhost:8080/products/macbook-001" | jq .price
# → 1649.99

# What exactly diverged?
curl "http://localhost:8080/diff?timeline=q1-sale" | jq
```

### Diff output

```json
{
  "mode": "fork",
  "timeline": "q1-sale",
  "summary": {
    "products_changed": 2,
    "products_added": 0,
    "products_removed": 0
  },
  "changes": [
    {
      "aggregate_id": "macbook-001",
      "name": "MacBook Pro 14\"",
      "status": "changed",
      "fields": [
        { "field": "price", "from": 1649.99, "to": 1299.99 }
      ]
    },
    {
      "aggregate_id": "monitor-001",
      "name": "4K Monitor",
      "status": "changed",
      "fields": [
        { "field": "stock", "from": 45, "to": 15 }
      ]
    }
  ]
}
```

### Temporal diff on main

```bash
# What changed across the entire catalog between Q3 and Q4 2024?
curl "http://localhost:8080/diff?from=2024-10-01T00:00:00Z&to=2025-01-01T00:00:00Z" | jq
```

### Persistence

```bash
# Kill the server. Restart without -seed.
go run . -db ./data

# The entire event history survived. Queries work identically.
curl "http://localhost:8080/products?at=2024-12-01T00:00:00Z" | jq

# Note: forks are intentionally ephemeral — they are what-if scenarios,
# not permanent branches. They do not survive restarts.
```

---

## Architecture

```
 HTTP Request
      │
      ▼
 ┌─────────────┐
 │   Handler   │  extracts ?at= and ?timeline= from query params
 └──────┬──────┘
        │
        ▼
 ┌─────────────┐     timeline=q1-sale    ┌──────────────────┐
 │  Projector  │ ──────────────────────▶ │  ForkEventStore  │
 └──────┬──────┘                         │                  │
        │  (default)                     │  main events     │
        ▼                                │  up to fork pt   │
 ┌─────────────┐                         │       +          │
 │  BadgerDB   │ ◀───────────────────────│  fork overlay    │
 │ EventStore  │                         └──────────────────┘
 └──────┬──────┘
        │
        ▼
 ┌─────────────────────────────────────────────────┐
 │  Projector.Project(aggregateID, asOf time.Time) │
 │                                                 │
 │  1. Find nearest snapshot before asOf           │
 │  2. Load only events after snapshot, up to asOf │
 │  3. Replay: product.Apply(event) for each       │
 │  4. Return reconstructed Product                │
 └─────────────────────────────────────────────────┘
```

### Event log (BadgerDB)

Events are stored with lexicographically sortable keys:

```
events:<aggregateID>:<version_10_digits>   →  JSON Event
snapshots:<aggregateID>:<unix_nano_20_digits>  →  JSON Snapshot
```

Zero-padding ensures version `10` never sorts before version `2`. This lets `LoadBefore` use a single forward range scan — no filtering pass needed.

### Snapshot compaction

Without snapshots, reconstructing a resource at time T means replaying every event since it was created. For a product with 10,000 events, that's 10,000 `Apply()` calls per read.

Every 10 events, Epoch serializes the current aggregate state as a snapshot. Reconstruction then becomes: find the nearest snapshot before T, load only the delta events, replay the delta. At most 9 events are ever replayed regardless of total history depth.

This is the same mechanism as Kafka log compaction and Postgres WAL checkpoints.

### ForkEventStore

A fork is not a copy of the event log. It is a thin overlay:

```
ForkEventStore {
    main:     BadgerEventStore  (read-only, events up to fork point)
    overlay:  MemoryEventStore  (fork-specific events only)
    forkedFrom: time.Time
}
```

`Load(aggregateID)` merges both streams ordered by `OccurredAt`. The Projector sees a single unified event stream and has no awareness that it is operating on a fork. The isolation is entirely in the store layer.

---

## Getting Started

**Requirements:** Go 1.22+

```bash
git clone https://github.com/HarshalPatel1972/epoch
cd epoch
go mod download

# In-memory mode (no persistence, fast for development)
go run . -seed

# Persistent mode
go run . -db ./data -seed

# Run tests
go test ./...

# Benchmarks
go test -bench=. -benchmem ./aggregate/...
```

---

## API Reference

### Products

| Method | Path | Query params | Description |
|--------|------|-------------|-------------|
| `POST` | `/products` | — | Create a product |
| `GET` | `/products` | `at`, `timeline`, `category` | List products |
| `GET` | `/products/:id` | `at`, `timeline` | Get one product |
| `PUT` | `/products/:id/price` | `timeline` | Update price |
| `PUT` | `/products/:id/stock` | `timeline` | Adjust stock by delta |
| `DELETE` | `/products/:id` | `timeline` | Soft delete |

### Timelines

| Method | Path | Description |
|--------|------|-------------|
| `POST` | `/timelines/fork` | Create a fork |
| `GET` | `/timelines` | List all forks |
| `DELETE` | `/timelines/:name` | Discard a fork |

### Diff

| Method | Path | Query params | Description |
|--------|------|-------------|-------------|
| `GET` | `/diff` | `from`, `to`, `aggregate_id` | Temporal diff on main |
| `GET` | `/diff` | `timeline`, `aggregate_id` | Fork vs main diff |

### Meta

| Method | Path | Description |
|--------|------|-------------|
| `GET` | `/events` | Raw event log (`?aggregate_id=` to filter) |
| `GET` | `/health` | Status, event count, aggregate count |

### Query params

- `at` — RFC3339 timestamp. Absent means current state.
- `timeline` — fork name. Absent means main timeline.
- `from`, `to` — RFC3339 timestamps for temporal diff range.

### Error shape

```json
{ "error": "human readable message", "code": "INVALID_AT_PARAM" }
```

---

## Concepts

**Event sourcing.** Instead of storing current state and overwriting it on every mutation, store every mutation as an immutable event. Current state is derived by replaying the log. Used in production at Stripe (ledger), Confluent, Axon Framework, and event-driven financial systems generally.

**CQRS.** Commands (writes) and Queries (reads) operate on different models. Writes append events. Reads project state from events. Epoch's Projector is the read model.

**Aggregate.** An entity whose state is fully determined by its event history. In Epoch, each `Product` is an aggregate — its current state is `fold(Apply, events)`.

**Snapshot compaction.** A periodic serialization of aggregate state that acts as a checkpoint, bounding the number of events that must be replayed for any read. Same concept as Postgres WAL checkpoints and Kafka log compaction.

**Vector clock.** Events are ordered by `OccurredAt` timestamp and per-aggregate `Version`. When fork events are merged with main events, `mergeEventSlices` uses both fields to produce a deterministic total order.

---

## Roadmap

**Phase 3**
- `GET /stream/events` — WebSocket tail of the live event log, filterable by aggregate ID and event type
- Fork persistence to BadgerDB — forks survive restarts
- `POST /timelines/:name/merge` — merge a fork's overlay events back into the main timeline with conflict detection

**Phase 4**
- Multi-node replication via Raft consensus on the event log
- `GET /diff` response in JSON Patch format (RFC 6902)
- gRPC endpoint for high-throughput event ingestion

---

## Stack

Go 1.22 · BadgerDB v4 · `net/http` stdlib · `github.com/google/uuid`

No web framework. No ORM. The only external dependency beyond UUID generation is the storage engine.
