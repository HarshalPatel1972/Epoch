# Epoch Benchmarks

Results of `go test -bench=. -benchmem ./aggregate/...` on a 12th Gen Intel(R) Core(TM) i3-1220P.

```text
goos: windows
goarch: amd64
pkg: github.com/HarshalPatel1972/epoch/aggregate
cpu: 12th Gen Intel(R) Core(TM) i3-1220P
BenchmarkProjectNoSnapshot100-12        	   10000	    111032 ns/op	   55721 B/op	     516 allocs/op
BenchmarkProjectNoSnapshot1000-12       	    1063	   1136669 ns/op	  469321 B/op	    5019 allocs/op
BenchmarkProjectWithSnapshot1000-12     	    8599	    147546 ns/op	  237289 B/op	      20 allocs/op
BenchmarkProjectWithSnapshot10000-12    	     328	   3344906 ns/op	 4521715 B/op	      28 allocs/op
PASS
ok  	github.com/HarshalPatel1972/epoch/aggregate	11.055s
```

### Analysis
- **Snapshot Efficiency**: Reconstructing a product with 1000 events is **~7.7x faster** with snapshots enabled (0.14ms vs 1.13ms).
- **Growth**: No-snapshot performance scales linearly with event count ($O(N)$), while snapshot-assisted performance scale primarily depends on snapshot frequency and store retrieval efficiency ($O(K)$ where $K$ is max events between snapshots).
- **Allocations**: Snapshots significantly reduce per-operation allocations by avoiding repetitive payload unmarshaling during replay.
