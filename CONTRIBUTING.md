# Contributing to Epoch

Thanks for helping. Bug reports, new storage backends, examples and documentation fixes are all welcome.

## Reporting a bug

Open an issue with:

- what you did (ideally a failing test or a short program),
- what you expected,
- what happened instead, including the full error,
- your Go version and which store you use.

Correctness bugs (a load returning the wrong state, a branch seeing history it should not, a replay reporting the wrong outcome) are the most important kind. They are treated as urgent.

## Development

Epoch v2 lives in `v2/` and is made of two Go modules:

| Directory | Module | Go |
|---|---|---|
| `v2/` | `github.com/HarshalPatel1972/epoch/v2` (core, `memstore`, `epochtest`, `epochhttp`, examples) | 1.22+ |
| `v2/sqlitestore/` | `github.com/HarshalPatel1972/epoch/v2/sqlitestore` | 1.25+ |

The core module has no third-party dependencies. Keep it that way: anything that needs one belongs in its own module, like `sqlitestore`.

```sh
cd v2
go vet ./... && go test -race ./...
cd sqlitestore && go vet ./... && go test -race ./...
```

Run the demo to check Studio changes by eye:

```sh
cd v2 && go run ./examples/shop -serve :8080
```

## Pull requests

- One change per pull request, with a test that fails without it.
- Behaviour that every store must share belongs in `epochtest`, so all stores are checked against it.
- Run `gofmt`, `go vet` and the tests for both modules before pushing. CI runs them on Linux, macOS and Windows.
- Exported identifiers need doc comments that say what they do and what callers can rely on.

## Writing a store

Implement `epoch.Store` and run the conformance suite from your tests:

```go
func TestConformance(t *testing.T) {
	epochtest.Run(t, func(t *testing.T) epoch.Store { return mystore.New(t) })
}

func BenchmarkMyStore(b *testing.B) {
	epochtest.Bench(b, func(b *testing.B) epoch.Store { return mystore.New(b) })
}
```

The suite covers the whole contract, including concurrency, time clamping and branch deletion. A store that passes it works with every Epoch feature. If you publish one, open an issue and it will be listed in the README.

## Code of conduct

Be kind and assume good intent. Harassment or personal attacks of any kind are not tolerated, and maintainers will remove comments, commits or contributors that cross that line.
