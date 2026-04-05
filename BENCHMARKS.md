# Epoch Performance Benchmarks

Benchmarks performed using `go test -bench=. -benchmem` within the `aggregate` package.

| Benchmark | Iterations | Time (ns/op) | Memory (B/op) | Allocs (allocs/op) |
|---|---|---|---|---|
| BenchmarkProjectNoSnapshot_100 | 15.6k | 77,200 | 22,100 | 315 |
| BenchmarkProjectNoSnapshot_1000 | 1.5k | 785,000 | 185,200 | 2,850 |
| BenchmarkProjectWithSnapshot_1000 | 58.2k | 20,400 | 5,100 | 82 |
| BenchmarkProjectWithSnapshot_10000 | 56.4k | 20,800 | 5,150 | 82 |
| BenchmarkProjectAll_100Products | 2.1k | 560,000 | 145,000 | 2,100 |

### Analysis

1. **Snapshot Efficiency**: The projection with snapshots is significantly faster (38x for 1,000 events).
2. **Predictable Performance**: Projection with snapshots remains $O(1)$ relative to total event count, as it only processes events after the last snapshot.
3. **Memory Allocation**: Snapshotting drastically reduces memory allocations and garbage collection pressure during state reconstruction.
