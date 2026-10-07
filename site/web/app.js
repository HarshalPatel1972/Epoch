"use strict";

const $ = (s, el = document) => el.querySelector(s);
const $$ = (s, el = document) => [...el.querySelectorAll(s)];
const NS = "http://www.w3.org/2000/svg";
const reducedMotion = matchMedia("(prefers-reduced-motion: reduce)").matches;

// h builds DOM nodes; strings become text nodes, never HTML.
function h(tag, attrs = {}, ...kids) {
  const el = document.createElement(tag);
  for (const [k, v] of Object.entries(attrs)) {
    if (v == null || v === false) continue;
    if (k.startsWith("on")) el.addEventListener(k.slice(2), v);
    else if (k === "class") el.className = v;
    else el.setAttribute(k, v === true ? "" : v);
  }
  for (const kid of kids.flat()) if (kid != null && kid !== false) el.append(kid instanceof Node ? kid : String(kid));
  return el;
}
function svg(tag, attrs = {}) {
  const el = document.createElementNS(NS, tag);
  for (const [k, v] of Object.entries(attrs)) el.setAttribute(k, v);
  return el;
}

const money = (c, opts = {}) => {
  const v = Math.round(c) / 100;
  return (v < 0 ? "−" : opts.sign && v > 0 ? "+" : "") + "$" + Math.abs(v).toLocaleString("en-US", { maximumFractionDigits: opts.cents ? 2 : 0, minimumFractionDigits: opts.cents ? 2 : 0 });
};
const count = (n, opts = {}) => (n < 0 ? "−" : opts.sign && n > 0 ? "+" : "") + Math.abs(n).toLocaleString("en-US");
const fmtDate = (d, o = { day: "numeric", month: "short" }) => new Date(d).toLocaleDateString("en-GB", { timeZone: "UTC", ...o });

function toast(msg) {
  const t = $("#toast");
  t.textContent = msg;
  t.classList.add("on");
  clearTimeout(toast.timer);
  toast.timer = setTimeout(() => t.classList.remove("on"), 1600);
}

// ---------- copy buttons, nav ----------
for (const b of $$(".install")) {
  b.addEventListener("click", async () => {
    const text = b.dataset.copy || b.querySelector("code").textContent;
    try { await navigator.clipboard.writeText(text); toast("Copied to clipboard"); }
    catch { toast("Select and copy: " + text); }
  });
}
addEventListener("scroll", () => $("#nav").classList.toggle("scrolled", scrollY > 10), { passive: true });

const navLinks = $$(".nav nav a");
const sectionObserver = new IntersectionObserver(entries => {
  for (const e of entries) if (e.isIntersecting) navLinks.forEach(a => a.classList.toggle("active", a.getAttribute("href") === "#" + e.target.id));
}, { rootMargin: "-45% 0px -50% 0px" });
["story", "playground", "studio", "fit", "compare", "start"].forEach(id => sectionObserver.observe(document.getElementById(id)));

// reveal on scroll
const revealObserver = new IntersectionObserver(entries => {
  for (const e of entries) if (e.isIntersecting) { e.target.classList.add("in"); revealObserver.unobserve(e.target); }
}, { rootMargin: "0px 0px -10% 0px" });
$$(".section-head, .limit, .uc, .start-steps li, .tt, .code-card, .table-wrap, .goal, .studio-grid").forEach(el => { el.classList.add("reveal"); revealObserver.observe(el); });

// ---------- hero: commits flow along "what happened"; some fork onto "what would have happened" ----------
(function hero() {
  const g = $("#heroDots"), branch = $("#heroBranch");
  if (!g) return;
  const branchLen = branch.getTotalLength();
  const FORK_X = 380, SPEED = 0.09; // px per ms
  let dots = [], last = 0, spawnIn = 0, running = true, seed = 7;
  const rnd = () => (seed = (seed * 16807) % 2147483647) / 2147483647;

  function spawn(x0 = -10) {
    const rejected = rnd() < 0.14;
    const el = svg("circle", { r: 5, class: rejected ? "d-no" : "d-ok", cx: x0, cy: 70 });
    g.append(el);
    const d = { el, x: x0, rejected, twin: null, changes: rnd() < 0.45 };
    dots.push(d);
  }
  for (let x = 30; x < 1150; x += 70 + rnd() * 50) spawn(x);

  function frame(t) {
    if (!running) return;
    const dt = Math.min(48, t - (last || t));
    last = t;
    spawnIn -= dt;
    if (spawnIn <= 0) { spawn(); spawnIn = 520 + rnd() * 700; }
    for (const d of dots) {
      const prev = d.x;
      d.x += SPEED * dt;
      d.el.setAttribute("cx", d.x);
      // Crossing the fork: send a replayed twin down the branch.
      if (prev < FORK_X && d.x >= FORK_X && !d.twin) {
        const alt = d.changes ? (d.rejected ? "d-ok" : "d-alt") : d.rejected ? "d-no" : "d-ok";
        d.twin = { el: svg("circle", { r: 5, class: alt }), s: 0, changed: d.changes };
        if (d.changes) d.twin.el.setAttribute("filter", "url(#glow)");
        g.append(d.twin.el);
      }
      if (d.twin) {
        d.twin.s = Math.min(branchLen + 40, d.twin.s + SPEED * dt);
        const p = branch.getPointAtLength(Math.min(d.twin.s, branchLen));
        d.twin.el.setAttribute("cx", p.x + Math.max(0, d.twin.s - branchLen));
        d.twin.el.setAttribute("cy", p.y);
      }
    }
    dots = dots.filter(d => {
      if (d.x > 1240) { d.el.remove(); d.twin && d.twin.el.remove(); return false; }
      return true;
    });
    requestAnimationFrame(frame);
  }
  if (reducedMotion) {
    // A still frame: twins placed along the branch.
    dots.forEach(d => {
      if (d.x < FORK_X) return;
      const p = branch.getPointAtLength(Math.min(branchLen, (d.x - FORK_X) * 1.1));
      g.append(svg("circle", { r: 5, cx: p.x, cy: p.y, class: d.changes ? "d-alt" : "d-ok" }));
    });
    return;
  }
  new IntersectionObserver(([e]) => {
    running = e.isIntersecting;
    if (running) { last = 0; requestAnimationFrame(frame); }
  }).observe($(".hero-art"));
})();

// ---------- story: one sticky stage, scenes switched by the chapter in view ----------
(function story() {
  const scenes = Object.fromEntries($$(".scene").map(s => [s.dataset.scene, s]));
  const steps = $$(".step");
  let current = null;

  // Populate decorative dots once.
  const flow = $(".flow-dots"), forget = $(".forget-dots"), replay = $(".replay-dots");
  let seed = 3;
  const rnd = () => (seed = (seed * 16807) % 2147483647) / 2147483647;
  for (let i = 0; i < 46; i++) {
    const x = 40 + i * 11.6, rej = [9, 21, 33, 40].includes(i);
    const c = svg("circle", { cx: x, cy: 210, r: 4.5, class: rej ? "d-no" : "d-ok", style: `opacity:0;transition:opacity .3s ${i * 25}ms` });
    flow.append(c);
    const f = svg("circle", { cx: x, cy: 150, r: 4.5, class: rej ? "d-gone" : "d-ok" });
    if (rej) f.dataset.falls = "1";
    forget.append(f);
    replay.append(svg("circle", { cx: x, cy: 140, r: 4.5, class: rej ? "d-no" : "d-ok" }));
    if (x > 250) {
      const changed = rnd() < 0.6;
      replay.append(svg("circle", { cx: x, cy: 280, r: 4.5, class: changed ? "d-alt" : "d-ok", style: `opacity:0;transition:opacity .3s ${800 + i * 30}ms` }));
    }
  }

  function countUp(el) {
    const to = +el.dataset.to;
    if (reducedMotion) { el.textContent = to.toLocaleString("en-US"); return; }
    const t0 = performance.now(), dur = 1400;
    const tick = t => {
      const k = Math.min(1, (t - t0) / dur), e = 1 - Math.pow(1 - k, 3);
      el.textContent = Math.round(to * e).toLocaleString("en-US");
      if (k < 1) requestAnimationFrame(tick);
    };
    requestAnimationFrame(tick);
  }

  function show(name) {
    if (name === current) return;
    current = name;
    for (const [n, el] of Object.entries(scenes)) el.classList.toggle("on", n === name);
    steps.forEach(s => s.classList.toggle("on", s.dataset.scene === name));
    const scene = scenes[name];
    $$(".count", scene).forEach(countUp);
    $$(".rp-count", scene).forEach(el => {
      const from = +el.dataset.from, to = +el.dataset.to;
      const fmt = v => (v < 0 ? "−" : "") + Math.abs(v);
      clearInterval(el._t);
      clearTimeout(el._d);
      if (reducedMotion) { el.textContent = fmt(to); return; }
      el.textContent = fmt(from);
      let v = from;
      el._d = setTimeout(() => {
        el._t = setInterval(() => { v--; el.textContent = fmt(v); if (v <= to) clearInterval(el._t); }, 90);
      }, 1100);
    });
    if (name === "flow") $$("circle", flow).forEach(c => (c.style.opacity = 1));
    if (name === "forget") {
      $$("circle[data-falls]", forget).forEach((c, i) => {
        c.style.transition = "none"; c.style.transform = ""; c.style.opacity = 1;
        requestAnimationFrame(() => {
          c.style.transition = `transform 1.2s ${300 + i * 150}ms cubic-bezier(.5,0,.8,.4), opacity 1.2s ${300 + i * 150}ms`;
          c.style.transform = "translateY(160px)"; c.style.opacity = 0;
        });
      });
    }
    if (name === "replay") $$("circle[style]", replay).forEach(c => (c.style.opacity = 1));
  }

  const io = new IntersectionObserver(entries => {
    for (const e of entries) if (e.isIntersecting) show(e.target.dataset.scene);
  }, { rootMargin: "-48% 0px -48% 0px" });
  steps.forEach(s => io.observe(s));
  show("flow");
})();

// ---------- the engine: Epoch compiled to WebAssembly, in a worker ----------
const engine = (() => {
  let worker, seq = 0;
  const pending = new Map();
  let started = null;
  function start() {
    if (started) return started;
    worker = new Worker("worker.js");
    worker.onmessage = ({ data }) => {
      const p = pending.get(data.id);
      if (!p) return;
      pending.delete(data.id);
      data.ok ? p.resolve(data.data) : p.reject(new Error(data.error));
    };
    worker.onerror = e => { for (const p of pending.values()) p.reject(new Error(e.message || "engine failed to load")); pending.clear(); };
    started = call("init");
    return started;
  }
  function call(fn, arg) {
    return new Promise((resolve, reject) => {
      const id = ++seq;
      pending.set(id, { resolve, reject });
      worker.postMessage({ id, fn, arg });
    });
  }
  return { start, call: (fn, arg) => start().then(() => call(fn, arg)) };
})();

// Start loading when the reader approaches the playground, or soon after load.
const loadNear = new IntersectionObserver(([e]) => { if (e.isIntersecting) { boot(); loadNear.disconnect(); } }, { rootMargin: "1200px 0px" });
loadNear.observe($("#playground"));
addEventListener("load", () => setTimeout(boot, 2500));

// ---------- playground ----------
const POLICY_KEYS = ["LoyaltyDiscount", "LoyaltyMonths", "BulkDiscount", "BulkQty", "Backorder"];
const ACTUAL = { LoyaltyDiscount: 0, LoyaltyMonths: 12, BulkDiscount: 10, BulkQty: 5, Backorder: 0 };
const PRESETS = {
  actual: { policy: { ...ACTUAL }, from: 0 },
  loyalty: { policy: { ...ACTUAL, LoyaltyDiscount: 15, LoyaltyMonths: 12 }, from: 0 },
  backorders: { policy: { ...ACTUAL, Backorder: 3 }, from: 0 },
  nodiscount: { policy: { ...ACTUAL, BulkDiscount: 0 }, from: 0 },
  generous: { policy: { ...ACTUAL, BulkDiscount: 20, BulkQty: 3 }, from: 3 },
};
const MONTHS = ["Jan", "Feb", "Mar", "Apr", "May", "Jun"];
const state = { policy: { ...ACTUAL }, from: 0, base: null, last: null, kind: "all", shown: 30, busy: false, queued: false };

const form = $("#rules");
const monthsEl = $("#months");
MONTHS.forEach((m, i) => monthsEl.append(h("button", { type: "button", role: "radio", "aria-checked": String(i === 0), "data-m": i, onclick: () => { state.from = i; syncControls(); schedule(); } }, m)));

function syncControls() {
  for (const k of POLICY_KEYS) {
    const input = form.elements[k];
    input.value = state.policy[k];
    const pct = ((input.value - input.min) / (input.max - input.min)) * 100;
    input.style.setProperty("--p", pct + "%");
    const out = $(`output[data-for=${k}]`);
    out.textContent = input.value + out.dataset.unit;
    out.classList.toggle("changed", +input.value !== ACTUAL[k]);
  }
  $$("button", monthsEl).forEach(b => b.setAttribute("aria-checked", String(+b.dataset.m === state.from)));
  const preset = Object.entries(PRESETS).find(([, p]) => p.from === state.from && POLICY_KEYS.every(k => p.policy[k] === state.policy[k]));
  $$(".presets button").forEach(b => b.classList.toggle("active", !!preset && b.dataset.preset === preset[0]));
}

for (const k of POLICY_KEYS) form.elements[k].addEventListener("input", e => { state.policy[k] = +e.target.value; syncControls(); schedule(); });
$$(".presets button").forEach(b => b.addEventListener("click", () => {
  const p = PRESETS[b.dataset.preset];
  state.policy = { ...p.policy }; state.from = p.from;
  syncControls(); run();
}));
form.addEventListener("submit", e => { e.preventDefault(); run(); });
addEventListener("keydown", e => {
  if (e.key.toLowerCase() === "r" && !e.metaKey && !e.ctrlKey && !/INPUT|TEXTAREA|SELECT/.test(document.activeElement.tagName || "") ) {
    const r = $("#playground").getBoundingClientRect();
    if (r.top < innerHeight && r.bottom > 0) run();
  }
});

let timer;
function schedule() { clearTimeout(timer); timer = setTimeout(run, 180); }

async function boot() {
  if (state.base || boot.started) return;
  boot.started = true;
  const status = $("#engineStatus");
  try {
    const t0 = performance.now();
    const base = await engine.start();
    state.base = base;
    status.className = "status ready";
    status.replaceChildren(h("span", { class: "spinner" }), " engine ready");
    status.title = `${base.commands.toLocaleString()} commands loaded`;
    $("#replayBtn").disabled = false;
    $("#speed").replaceChildren(`Engine loaded and six months of history generated in ${Math.round(performance.now() - t0)} ms, in your browser.`);
    renderChart(null);
    initTimeTravel(base);
    run();
  } catch (err) {
    status.textContent = "engine failed to load";
    $("#speed").textContent = "Couldn't start the engine in this browser: " + err.message;
  }
}

async function run() {
  if (!state.base) return;
  if (state.busy) { state.queued = true; return; }
  state.busy = true;
  const btn = $("#replayBtn");
  btn.textContent = "Replaying…";
  try {
    const from = state.from === 0 ? "" : `2026-${String(state.from + 1).padStart(2, "0")}-01`;
    const res = await engine.call("replay", { policy: state.policy, from });
    state.last = res; state.shown = 30;
    renderKPIs(res); renderChart(res); renderChanges(); renderSpeed(res);
  } catch (err) {
    $("#speed").textContent = "Replay failed: " + err.message;
  } finally {
    state.busy = false;
    btn.textContent = "Replay history";
    if (state.queued) { state.queued = false; run(); }
  }
}

function renderKPIs(r) {
  const defs = { revenue: money, orders: count, turned_away: count, discounts: money };
  for (const [k, f] of Object.entries(defs)) {
    const a = r.a[k], b = r.b[k], d = b - a;
    const v = $(`[data-k=${k}]`);
    v.textContent = d === 0 ? "no change" : f(d, { sign: true });
    // For turned-away orders and discounts, lower is better for the shop.
    const good = k === "turned_away" || k === "discounts" ? d < 0 : d > 0;
    v.className = "v " + (d === 0 ? "same" : good ? "up" : "down");
    $(`[data-s=${k}]`).textContent = `${f(a)} → ${f(b)}`;
  }
}

function renderSpeed(r) {
  const changed = r.diverged;
  $("#speed").replaceChildren(
    h("span", { class: "bolt" }, "⚡"),
    "Replayed ", h("b", {}, r.commits.toLocaleString()), " commands in ", h("b", {}, r.ms.toFixed(0) + " ms"),
    ` · ${changed.toLocaleString()} decision${changed === 1 ? "" : "s"} changed · in your browser, nothing sent anywhere`);
}

function cumulative(arr) { let s = 0; return arr.map(v => (s += v)); }

function renderChart(r) {
  const el = $("#chart"), W = 800, H = 280, PAD = 8;
  el.replaceChildren();
  const base = state.base;
  const A = cumulative(base.daily.revenue);
  const B = r ? cumulative(r.daily_b.revenue) : null;
  const max = Math.max(...A, ...(B || [0])) * 1.04;
  const x = i => (i / (A.length - 1)) * W;
  const y = v => H - PAD - (v / max) * (H - PAD * 2);
  for (let i = 1; i <= 3; i++) el.append(svg("line", { x1: 0, x2: W, y1: (H / 4) * i, y2: (H / 4) * i, class: "grid-line" }));
  const path = arr => arr.map((v, i) => `${i ? "L" : "M"}${x(i).toFixed(1)},${y(v).toFixed(1)}`).join("");
  if (B) {
    const area = path(A) + B.map((v, i) => `L${x(B.length - 1 - i).toFixed(1)},${y(B[B.length - 1 - i]).toFixed(1)}`).join("") + "Z";
    const up = B[B.length - 1] >= A[A.length - 1];
    el.append(svg("path", { d: area, class: "gap-area", fill: up ? "#6cc6a6" : "#f0616d" }));
    if (state.from > 0) {
      const day = Math.round((Date.UTC(2026, state.from, 1) - Date.UTC(2026, 0, 1)) / 864e5);
      el.append(svg("line", { x1: x(day), x2: x(day), y1: 0, y2: H, class: "from-line" }));
    }
  }
  el.append(svg("path", { d: path(A), class: "line-a" }));
  if (B) {
    const lb = svg("path", { d: path(B), class: "line-b" });
    el.append(lb);
    if (!reducedMotion) {
      const len = 2000;
      lb.style.strokeDasharray = len; lb.style.strokeDashoffset = len;
      lb.getBoundingClientRect();
      lb.style.transition = "stroke-dashoffset .9s cubic-bezier(.4,0,.2,1)";
      lb.style.strokeDashoffset = 0;
    }
  }
  renderDiff(A, B, x);
  const cursor = svg("line", { y1: 0, y2: H, class: "cursor-line", visibility: "hidden" });
  el.append(cursor);
  $("#axis").replaceChildren(...MONTHS.map(m => h("span", {}, m)), h("span", {}, "Jul"));

  const tip = $("#chartTip");
  el.onmousemove = e => {
    const rect = el.getBoundingClientRect();
    const i = Math.max(0, Math.min(A.length - 1, Math.round(((e.clientX - rect.left) / rect.width) * (A.length - 1))));
    const px = (x(i) / W) * rect.width;
    cursor.setAttribute("x1", x(i)); cursor.setAttribute("x2", x(i)); cursor.setAttribute("visibility", "visible");
    const date = new Date(Date.UTC(2026, 0, 1 + i));
    tip.hidden = false;
    tip.style.left = px + 18 + "px";
    tip.style.top = (y(A[i]) / H) * rect.height + 40 + "px";
    tip.replaceChildren(h("b", {}, fmtDate(date, { day: "numeric", month: "long" })), h("br"),
      h("span", { style: "color:var(--past)" }, "actual  " + money(A[i])),
      ...(B ? [h("br"), h("span", { style: "color:var(--alt)" }, "replay  " + money(B[i])), h("br"), h("span", {}, "diff    " + money(B[i] - A[i], { sign: true }))] : []));
  };
  el.onmouseleave = () => { tip.hidden = true; cursor.setAttribute("visibility", "hidden"); };
}

// The gap between the two lines, on its own scale, so small effects show.
function renderDiff(A, B, x) {
  const el = $("#diffChart"), W = 800, H = 70, mid = H / 2;
  el.replaceChildren(svg("line", { x1: 0, x2: W, y1: mid, y2: mid, class: "grid-line" }));
  if (!B) { $("#diffNow").textContent = ""; return; }
  const D = B.map((v, i) => v - A[i]);
  const m = Math.max(1, ...D.map(Math.abs));
  const y = v => mid - (v / m) * (mid - 4);
  const pts = D.map((v, i) => `${x(i).toFixed(1)},${y(v).toFixed(1)}`);
  const end = D[D.length - 1];
  el.append(svg("path", { d: `M0,${mid} L${pts.join(" L")} L${W},${mid} Z`, fill: end >= 0 ? "#6cc6a6" : "#f0616d", opacity: ".22" }));
  el.append(svg("path", { d: "M" + pts.join(" L"), class: "line-b" }));
  const now = $("#diffNow");
  now.textContent = money(end, { sign: true });
  now.style.color = end > 0 ? "var(--good)" : end < 0 ? "var(--bad)" : "var(--muted)";
}

const KIND = {
  now_accepted: ["pill ok", "now accepted"],
  now_rejected: ["pill no", "now refused"],
  events_changed: ["pill re", "re-priced"],
};
$$("#changeTabs button").forEach(b => b.addEventListener("click", () => {
  state.kind = b.dataset.kind; state.shown = 30;
  $$("#changeTabs button").forEach(x => x.setAttribute("aria-selected", String(x === b)));
  renderChanges();
}));
$("#moreChanges").addEventListener("click", () => { state.shown += 60; renderChanges(); });

function renderChanges() {
  const r = state.last, list = $("#changes");
  if (!r) return;
  const counts = { all: r.diverged, now_accepted: r.now_accepted, now_rejected: r.now_rejected, events_changed: r.events_changed };
  $$("#changeTabs button").forEach(b => ($("span", b).textContent = counts[b.dataset.kind].toLocaleString()));
  const items = r.changes.filter(c => state.kind === "all" || c.kind === state.kind);
  if (!items.length) {
    list.replaceChildren(h("li", { class: "empty" }, r.diverged ? "None of this kind." : "Every decision came out the same. Try a bigger change, or an earlier start month."));
    $("#moreChanges").hidden = true;
    return;
  }
  list.replaceChildren(...items.slice(0, state.shown).map((c, i) => {
    const [cls, label] = KIND[c.kind];
    return h("li", { style: `animation-delay:${Math.min(i, 12) * 25}ms` },
      h("span", { class: "when" }, fmtDate(c.time, { day: "numeric", month: "short", hour: "2-digit", minute: "2-digit", hour12: false })),
      h("span", { class: "what" }, h("b", {}, c.product), " ", h("span", { class: cls }, label),
        h("span", { class: "was" }, c.was), h("span", { class: "now" }, c.now)),
      h("span", { class: "delta " + (c.delta > 0 ? "pos" : c.delta < 0 ? "neg" : "") }, c.delta ? money(c.delta, { sign: true, cents: true }) : "±0"));
  }));
  const capped = r.changes.length < r.diverged && state.kind === "all";
  $("#moreChanges").hidden = state.shown >= items.length;
  if (capped && state.shown >= items.length) list.append(h("li", { class: "empty" }, `Showing the first ${r.changes.length} of ${r.diverged.toLocaleString()} changes.`));
}

syncControls();

// ---------- time travel ----------
let ttScale = {};
async function initTimeTravel(base) {
  const spark = $("#ttSpark"), slider = $("#ttSlider");
  const maxDay = Math.max(...base.daily.accepted.map((a, i) => a + base.daily.rejected[i]));
  spark.replaceChildren(...base.daily.accepted.map((a, i) => {
    const r = base.daily.rejected[i], tot = a + r;
    const bar = h("i", { class: r ? "r" : "", style: `height:${(tot / maxDay) * 100}%;--r:${tot ? (r / tot) * 100 : 0}%` });
    return bar;
  }));
  // Scale each product's bar to the most stock it ever held.
  const samples = await Promise.all([15, 45, 75, 105, 135, 165, 180].map(d => engine.call("stockAt", dayISO(d))));
  for (const rows of samples) for (const p of rows) ttScale[p.id] = Math.max(ttScale[p.id] || 1, p.stock);
  slider.disabled = false;
  let pending = false;
  slider.addEventListener("input", () => {
    if (pending) return;
    pending = true;
    requestAnimationFrame(async () => { pending = false; await showDay(+slider.value); });
  });
  await showDay(+slider.value);
}
function dayISO(d) { return new Date(Date.UTC(2026, 0, 1 + d, 23, 59, 59)).toISOString(); }
async function showDay(d) {
  $("#ttDate").textContent = fmtDate(Date.UTC(2026, 0, 1 + d), { weekday: "long", day: "numeric", month: "long", year: "numeric" });
  $$("#ttSpark i").forEach((b, i) => b.classList.toggle("past", i <= d));
  const rows = await engine.call("stockAt", dayISO(d));
  $("#ttGrid").replaceChildren(...rows.map(p => {
    const low = p.stock <= 2;
    return h("div", { class: "tt-card" },
      h("div", { class: "n" }, p.name),
      h("div", { class: "st" + (low ? " low" : "") }, p.stock, h("span", { style: "font-size:14px;color:var(--dim);font-family:var(--sans)" }, " in stock")),
      h("div", { class: "bar" }, h("i", { class: low ? "low" : "", style: `width:${Math.max(0, Math.min(100, (p.stock / ttScale[p.id]) * 100))}%` })),
      h("div", { class: "pr" }, money(p.price, { cents: true }) + " · after commit #" + p.seq));
  }));
}

// ---------- code ----------
const CODE = {
  model: `<span class="com">// Your business logic: two pure functions.</span>
<span class="kw">var</span> Products = &amp;epoch.<span class="ty">Model</span>[<span class="ty">Product</span>]{
	Name:     <span class="str">"product"</span>,
	Commands: []<span class="kw">any</span>{<span class="ty">PlaceOrder</span>{}, <span class="ty">Restock</span>{}},
	Events:   []<span class="kw">any</span>{<span class="ty">OrderPlaced</span>{}, <span class="ty">Restocked</span>{}},

	<span class="fn">Evolve</span>: <span class="kw">func</span>(p <span class="ty">Product</span>, e <span class="kw">any</span>) <span class="ty">Product</span> {
		<span class="kw">switch</span> e := e.(<span class="kw">type</span>) {
		<span class="kw">case</span> <span class="ty">OrderPlaced</span>:
			p.Stock -= e.Qty
		<span class="kw">case</span> <span class="ty">Restocked</span>:
			p.Stock += e.Qty
		}
		<span class="kw">return</span> p
	},

	<span class="fn">Decide</span>: <span class="kw">func</span>(p <span class="ty">Product</span>, c <span class="kw">any</span>, now time.<span class="ty">Time</span>) ([]<span class="kw">any</span>, <span class="kw">error</span>) {
		<span class="kw">switch</span> c := c.(<span class="kw">type</span>) {
		<span class="kw">case</span> <span class="ty">PlaceOrder</span>:
			<span class="kw">if</span> p.Stock &lt; c.Qty {
				<span class="kw">return</span> <span class="kw">nil</span>, ErrOutOfStock <span class="com">// refused, and recorded</span>
			}
			<span class="kw">return</span> []<span class="kw">any</span>{<span class="ty">OrderPlaced</span>{Qty: c.Qty, Total: <span class="fn">price</span>(p, c)}}, <span class="kw">nil</span>
		<span class="kw">case</span> <span class="ty">Restock</span>:
			<span class="kw">return</span> []<span class="kw">any</span>{<span class="ty">Restocked</span>(c)}, <span class="kw">nil</span>
		}
		<span class="kw">return</span> <span class="kw">nil</span>, ErrUnknownCommand
	},
}`,
  handle: `store, _ := sqlitestore.<span class="fn">Open</span>(<span class="str">"shop.db"</span>)

<span class="com">// Use it like any service. Every call is recorded.</span>
res, err := Products.<span class="fn">Handle</span>(ctx, store, <span class="str">"chair"</span>, <span class="ty">PlaceOrder</span>{Qty: <span class="num">10</span>})
<span class="kw">if</span> errors.<span class="fn">Is</span>(err, epoch.ErrRejected) {
	<span class="com">// Refused. The refusal is in the log too, so a replay can</span>
	<span class="com">// find out whether new rules would have accepted it.</span>
}
fmt.<span class="fn">Println</span>(res.State.Value.Stock)

<span class="com">// Idempotent retries, branches and backdated imports are options:</span>
Products.<span class="fn">Handle</span>(ctx, store, <span class="str">"chair"</span>, cmd,
	epoch.<span class="fn">CommandID</span>(<span class="str">"order-8812"</span>), epoch.<span class="fn">On</span>(<span class="str">"staging"</span>))`,
  travel: `<span class="com">// Any entity, at any moment.</span>
then, _ := Products.<span class="fn">Load</span>(ctx, store, <span class="str">"chair"</span>, epoch.<span class="fn">AsOf</span>(march1))
fmt.<span class="fn">Println</span>(then.Value.Stock)

<span class="com">// Any report, at any moment: projections fold the log.</span>
q1, _ := Sales.<span class="fn">Get</span>(ctx, store, epoch.<span class="fn">AsOf</span>(endOfQ1))

<span class="com">// Or branch the past and try something by hand.</span>
epoch.<span class="fn">Fork</span>(ctx, store, <span class="str">"what-if"</span>, epoch.<span class="ty">ForkOptions</span>{At: march1})
Products.<span class="fn">Handle</span>(ctx, store, <span class="str">"chair"</span>, <span class="ty">Restock</span>{Qty: <span class="num">50</span>}, epoch.<span class="fn">On</span>(<span class="str">"what-if"</span>))`,
  replay: `<span class="com">// Same model, new rule.</span>
withLoyalty := *Products
withLoyalty.Decide = loyaltyPricing

report, _ := epoch.<span class="fn">Replay</span>(ctx, store, epoch.<span class="ty">ReplayOptions</span>{
	Name:   <span class="str">"loyalty-15"</span>,
	From:   jan1,
	Models: []epoch.<span class="ty">AnyModel</span>{&amp;withLoyalty},
})
fmt.<span class="fn">Println</span>(report.EventsChanged, <span class="str">"orders re-priced"</span>)
fmt.<span class="fn">Println</span>(report.NowAccepted, <span class="str">"refused orders would have gone through"</span>)

<span class="com">// Diff any read model between what happened and what would have.</span>
cmp, _ := epoch.<span class="fn">Compare</span>(ctx, store, Sales, epoch.Main, <span class="str">"loyalty-15"</span>)
<span class="kw">for</span> _, c := <span class="kw">range</span> cmp.Changes {
	fmt.<span class="fn">Println</span>(c.Path, c.From, <span class="str">"→"</span>, c.To)  <span class="com">// revenue 54474570 → 50132930</span>
}`,
};
function showCode(key) {
  $("#codeBlock").innerHTML = CODE[key]; // static, authored above
  $$("#codeTabs button").forEach(b => b.setAttribute("aria-selected", String(b.dataset.code === key)));
}
$$("#codeTabs button").forEach(b => b.addEventListener("click", () => showCode(b.dataset.code)));
showCode("model");

// ---------- is it for you? ----------
const GOALS = [
  { id: "backtest", ico: "↺", title: "Test a rule change on real past traffic", q: "“What would the new pricing, limit or threshold have done?”", fit: true,
    head: "This is exactly what Epoch is for.",
    body: ["Record commands with Epoch, keep your decision logic in Decide, and replay any period under a new rule. You get every changed decision, and a diff of any report.",
      "Works best when decisions depend on state that evolves: stock, balances, quotas, limits."] },
  { id: "why", ico: "?", title: "Explain why a decision was made", q: "“Why was this order refused in March?”", fit: true,
    head: "Epoch is a good fit.",
    body: ["Every command, its outcome and the reason for any refusal are kept, and you can load the exact state the decision saw with AsOf.",
      "If you only need an audit trail of data changes, without the decisions, a temporal table may be enough."] },
  { id: "rows", ico: "◷", title: "See what a row looked like last month", q: "“What was this customer's address on 1 May?”", fit: false,
    head: "You probably don't need Epoch.",
    body: ["System-versioned temporal tables keep every past version of a row and query it with FOR SYSTEM_TIME AS OF. They're built into SQL Server, MariaDB and Db2, and available for Postgres through extensions."],
    tools: ["SQL temporal tables", "XTDB", "Datomic"] },
  { id: "git", ico: "⑂", title: "Branch and merge my database like Git", q: "“Give every pull request its own copy of the data.”", fit: false,
    head: "Look at a database built for branching.",
    body: ["Epoch branches the history of your business decisions, not arbitrary tables, and it doesn't merge branches. For Git-style branching of a whole database, use a tool built for it."],
    tools: ["Dolt", "Neon", "PlanetScale"] },
  { id: "bus", ico: "⇶", title: "Share events between many services", q: "“A durable log for our whole platform.”", fit: false,
    head: "Use an event store or a log broker.",
    body: ["Epoch is a library for one application's decision logic. A platform-wide event backbone needs replication, subscriptions and consumer groups."],
    tools: ["EventStoreDB (Kurrent)", "Kafka", "NATS JetStream"] },
  { id: "bi", ico: "∿", title: "Analyse trends in historical data", q: "“Revenue by region, by week, for three years.”", fit: false,
    head: "That's a job for your analytics stack.",
    body: ["Aggregations over large history belong in a warehouse or an analytical database. Epoch's projections suit operational read models, not heavy analytics."],
    tools: ["DuckDB", "ClickHouse", "BigQuery"] },
];
const goalsEl = $("#goals");
GOALS.forEach(g => goalsEl.append(h("button", { class: "goal", type: "button", role: "radio", "aria-checked": "false", "data-g": g.id, onclick: () => pickGoal(g.id) },
  h("span", { class: "ico", "aria-hidden": "true" }, g.ico), h("b", {}, g.title), h("span", { class: "q" }, g.q))));
function pickGoal(id) {
  const g = GOALS.find(x => x.id === id);
  $$(".goal").forEach(b => b.setAttribute("aria-checked", String(b.dataset.g === id)));
  $("#verdict").replaceChildren(h("div", { class: "verdict-card " + (g.fit ? "yes" : "no") },
    h("div", { class: "badge", "aria-hidden": "true" }, g.fit ? "✓" : "→"),
    h("div", {}, h("h3", {}, g.head), ...g.body.map(p => h("p", {}, p)),
      g.tools ? h("div", { class: "tools" }, ...g.tools.map(t => h("span", {}, t))) : h("p", {}, h("a", { href: "#playground" }, "See it on real data in the playground ↑")))));
}

const USE_CASES = [
  ["Pricing & promotions", "What if free shipping had started at $50 instead of $75?", "Re-price every past order; see margin and volume."],
  ["Credit & lending", "What if the minimum score had been 640 last quarter?", "Which applications flip, and what exposure follows."],
  ["Fraud & risk", "How many good customers would the new threshold have blocked?", "Count false positives on real traffic before shipping."],
  ["Inventory & fulfilment", "Would allowing backorders have recovered sales, or cost new ones?", "Stock effects ripple through every later order."],
  ["Quotas & rate limits", "Which customers would the new plan limits have throttled?", "Replay API usage under new tiers."],
  ["Eligibility & claims", "Which claims would the revised policy have approved?", "Compare outcomes case by case, with reasons."],
];
$("#useCases").append(...USE_CASES.map(([tag, q, s]) => h("div", { class: "uc reveal" }, h("span", { class: "tag" }, tag), h("p", {}, q), h("small", {}, s))));
$$("#useCases .uc").forEach(el => revealObserver.observe(el));

// ---------- compare ----------
const TOOLS = ["Epoch", "SQL temporal tables", "Dolt", "Datomic", "XTDB", "EventStoreDB"];
const ROWS = [
  ["See any past state", "Read data as of a moment", ["y", "y", "y", "y", "y", ["p", "replay a stream"]]],
  ["Branch history and write to it", "Isolated what-if timelines", ["y", "n", "y", ["p", "d/with: speculative, in memory"], "n", "n"]],
  ["Re-run past decisions with new logic", "Not just rebuild a view of old data", ["y", "n", "n", "n", "n", ["p", "rebuilds read models, not decisions"]]],
  ["Keeps refused requests", "So new rules can accept them", ["y", "n", "n", "n", "n", ["p", "if you record them as events"]]],
  ["Diff outcomes between timelines", "Structural diff of any read model", ["y", "n", ["y", "dolt diff"], "n", "n", "n"]],
];
const KINDS = ["Go library, on your storage", "Built into SQL Server, MariaDB, Db2", "MySQL-compatible database", "Database (JVM)", "Database (SQL)", "Event store database"];
(function compareTable() {
  const t = $("#cmpTable");
  t.append(h("thead", {}, h("tr", {}, h("th", {}, ""), ...TOOLS.map((n, i) => h("th", { class: i === 0 ? "epoch" : "" }, n)))));
  const mark = v => {
    const [k, note] = Array.isArray(v) ? v : [v];
    const sym = { y: ["y", "✓", "yes"], p: ["p", "◐", "partly"], n: ["n", "—", "no"] }[k];
    return [h("span", { class: sym[0], "aria-label": sym[2] }, sym[1]), note ? h("span", { class: "note" }, note) : null];
  };
  t.append(h("tbody", {},
    ...ROWS.map(([title, sub, vals]) => h("tr", {}, h("td", {}, title, h("small", {}, sub)), ...vals.map((v, i) => h("td", { class: i === 0 ? "epoch" : "" }, mark(v))))),
    h("tr", { class: "kind" }, h("td", {}, "What it is"), ...KINDS.map((k, i) => h("td", { class: i === 0 ? "epoch" : "" }, k)))));
})();


// ---------- hero terminal: types the real tour output ----------
(function terminal() {
  const el = $("#term");
  if (!el) return;
  // Trimmed from `go run ./examples/shop` to fit; numbers are as printed.
  const OUT = [
    ["q", "Epoch tour: an online shop, 1 Jan to 30 Jun 2026"],
    ["", "Recorded 2,280 commands: 2,068 orders placed, 48 turned away."],
    ["", ""],
    ["q", "3. What if customers of 12+ months had had 15% off all year?"],
    ["", "   Replayed 2,280 commands on branch \"loyalty\" in 13ms."],
    ["", "   1,477 orders turned out differently: 1,477 re-priced."],
    ["", ""],
    ["d", "                           actual       loyalty    difference"],
    ["", "   orders                   2,068         2,068             ·"],
    ["", "   turned away                 48            48             ·"],
    ["pos", "   discounts given     $24,617.30    $68,033.70   +$43,416.40"],
    ["neg", "   revenue            $544,745.70   $501,329.30   -$43,416.40"],
    ["", ""],
    ["", "   First change: kb-01 on 1 Jan 09:33"],
    ["d", "     was: order of 2 at $129.00, $0.00 off, total $258.00"],
    ["", "     now: order of 2 at $129.00, $38.70 off, total $219.30"],
  ];
  const CMD = "go run ./examples/shop";
  const prompt = () => h("span", { class: "p" }, "$ ");
  const cursor = h("span", { class: "cur" });
  function finalFrame() {
    el.replaceChildren(prompt(), CMD + "\n", ...OUT.map(([c, t]) => h("span", { class: c }, t + "\n")), prompt(), cursor);
  }
  if (reducedMotion) { finalFrame(); return; }
  let started = false;
  async function play() {
    if (started) return;
    started = true;
    const wait = ms => new Promise(r => setTimeout(r, ms));
    el.replaceChildren(prompt(), cursor);
    await wait(600);
    for (const ch of CMD) { cursor.before(ch); await wait(38 + Math.random() * 40); }
    cursor.before("\n");
    await wait(450);
    for (const [c, t] of OUT) {
      cursor.before(h("span", { class: c }, t + "\n"));
      await wait(t ? 90 : 160);
    }
    cursor.before(prompt());
  }
  new IntersectionObserver(([e], io) => { if (e.isIntersecting) { play(); io.disconnect(); } }).observe(el);
})();

// ---------- studio showcase ----------
(function studio() {
  const SHOTS = {
    overview: ["img/studio-overview.png", "branch=main&tab=views", "Epoch Studio showing the sales read model with an activity chart and branch list"],
    replay: ["img/studio-replay.png", "branch=backorders&tab=report", "Epoch Studio replay report listing orders whose outcome changed"],
    compare: ["img/studio-compare.png", "branch=loyalty&tab=compare", "Epoch Studio comparing revenue between the main and loyalty branches"],
    entity: ["img/studio-entity.png", "branch=backorders&tab=entity", "Epoch Studio showing the ergonomic chair's history on the backorders branch"],
  };
  const img = $("#studioShot"), url = $("#studioUrl");
  // Warm the cache so switching tabs is instant.
  const prefetch = new IntersectionObserver(([e], io) => {
    if (!e.isIntersecting) return;
    Object.values(SHOTS).forEach(([src]) => { const i = new Image(); i.src = src; });
    io.disconnect();
  }, { rootMargin: "600px 0px" });
  prefetch.observe(img);
  $$("#studioTabs button").forEach(b => b.addEventListener("click", () => {
    const [src, hash, alt] = SHOTS[b.dataset.shot];
    $$("#studioTabs button").forEach(x => x.setAttribute("aria-selected", String(x === b)));
    url.textContent = "localhost:8080/epoch/#" + hash;
    img.classList.add("swap");
    $("#studioLink").href = src;
    setTimeout(() => { img.src = src; img.alt = alt; img.onload = () => img.classList.remove("swap"); }, 150);
  }));
})();
