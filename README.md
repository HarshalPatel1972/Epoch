# Epoch — Phase 2
## Time-Traveling API Server with Timeline Forking

Epoch is an event-sourced inventory API that allows you to query historical states and branch them into alternate timelines ("what-if" scenarios).

### Key Features (Phase 1 & 2)
- **BadgerDB Persistence**: Events and snapshots survive restarts.
- **Point-in-Time Queries**: `GET /products?at=2025-01-15T10:00:00Z` reconstructs history on the fly.
- **Timeline Forking**: Branch the state at any past moment, mutate it independently, and compare results.
- **Diff Engine**: Temporal diffs on main timeline or Fork vs. Main state comparison.
- **Snapshot Support**: Automatically snapshots aggregates every 10 events to ensure fast reconstruction.
- **Go standard library**: Minimum dependencies (Go 1.22+ required).

---

### Running the Server

1. **Persistent Mode (BadgerDB)**
   ```bash
   go run main.go -db ./data -seed
   ```

2. **In-Memory Mode**
   ```bash
   go run main.go -seed
   ```

---

### Demo Sequence: Timeline Forking

**1. Create a Fork from 3 months ago**
*"What would happen if we cut prices 3 months back?"*
```bash
curl -X POST http://localhost:8080/timelines/fork \
  -H "Content-Type: application/json" \
  -d '{
    "name": "sale-sim",
    "forked_from": "2026-01-01T00:00:00Z",
    "description": "20% price drop simulation"
  }'
```

**2. List the timelines**
```bash
curl http://localhost:8080/timelines
```

**3. Modify the Fork only**
Change the price of a product on the `sale-sim` timeline.
```bash
curl -X PUT "http://localhost:8080/products/<id>/price?timeline=sale-sim" \
  -H "Content-Type: application/json" \
  -d '{"price": 1599.99}'
```

**4. Compare Fork vs. Main**
See exactly what is different between your simulation and the real current state.
```bash
curl "http://localhost:8080/diff?timeline=sale-sim"
```

**5. Temporal Diff on Main**
See what changed on the main timeline over the last 3 months.
```bash
curl "http://localhost:8080/diff?from=2026-01-01T00:00:00Z"
```

---

### Phase 2 Architecture additions
- **BadgerDB**: LSM-tree KV store for efficient event log storage.
- **Overlay Storage**: `ForkEventStore` presented a unified view by merging a read-only main segment with a fork-specific memory overlay.
- **Reflection-free Diffing**: High-performance state comparison for auditing and simulations.

### Technology Stack
- **Language**: Go 1.22+
- **Persistence**: BadgerDB v4
- **ID Generation**: google/uuid
- **Routing**: net/http (Standard Library)
