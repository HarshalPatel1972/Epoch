# Epoch website

The site at https://harshalpatel1972.github.io/Epoch/. It is plain HTML, CSS and JavaScript with no build step, apart from the playground engine: the real Epoch library and the shop example from `v2/examples/shop`, compiled to WebAssembly and run in a Web Worker.

| Path | What it is |
|---|---|
| `web/index.html`, `styles.css`, `app.js` | The page: story, playground, time travel, fit guide, comparison |
| `web/worker.js` | Loads the engine off the main thread |
| `playground/main.go` | The engine's browser API (`init`, `replay`, `stockAt`) |
| `web/epoch.wasm`, `web/wasm_exec.js` | Build outputs, not committed |

## Preview locally

From this directory:

```sh
GOOS=js GOARCH=wasm go build -trimpath -ldflags="-s -w" -o web/epoch.wasm ./playground
cp "$(go env GOROOT)/lib/wasm/wasm_exec.js" web/
python -m http.server 8790 --directory web
```

Then open http://localhost:8790. The page must be served over HTTP; opening the file directly cannot load the engine.

On Windows PowerShell, set the variables with `$env:GOOS="js"; $env:GOARCH="wasm"` before `go build`, and remove them afterwards.

## Deploy

`.github/workflows/pages.yml` builds the engine and publishes `web/` to GitHub Pages on every push to `main` that touches `site/` or `v2/`.

## Keeping the story honest

The numbers in the story chapters (2,068 orders, 48 turned away, −$43,416, the 20 May chair ripple) come from the playground's deterministic data. If you change `v2/examples/shop/shop/seed.go`, re-check them in the playground and update `index.html`.
