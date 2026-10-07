// Runs the Epoch engine (compiled to WebAssembly) off the main thread, so
// seeding six months of history never blocks scrolling or animation.
importScripts("wasm_exec.js");

const ready = (async () => {
  const go = new Go();
  const res = await fetch("epoch.wasm");
  const { instance } = "instantiateStreaming" in WebAssembly && res.headers.get("content-type") === "application/wasm"
    ? await WebAssembly.instantiateStreaming(res, go.importObject)
    : await WebAssembly.instantiate(await res.arrayBuffer(), go.importObject);
  go.run(instance);
  // go.run returns once main blocks; the API is registered synchronously.
  return self.epochPlayground;
})();

self.onmessage = async ({ data: { id, fn, arg } }) => {
  try {
    const api = await ready;
    const t = performance.now();
    const out = JSON.parse(api[fn](arg == null ? "" : typeof arg === "string" ? arg : JSON.stringify(arg)));
    out.wall = performance.now() - t;
    self.postMessage({ id, ...out });
  } catch (err) {
    self.postMessage({ id, ok: false, error: String(err && err.message || err) });
  }
};
