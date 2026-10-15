# Future features

Parked work lives here, ignored by git (see `.gitignore`: everything under
`future/` except this manifest). Each entry records what was built or mapped,
why it waits, and how to resurrect it. Nothing here is imported, built, or
tested by the active tree.

## NPU decision runtime (`future/npu/`) — retired 2026-10-06

**What:** the Laya decision model served from the Apple Neural Engine via
laya-apple 1.6.3: strict `npu` device, ANE-first `auto` (MLX spill only past
the ANE buckets), user-selectable checkpoint, loud artifact/shape errors.
Parked files are the last working sidecar, its tests, and its requirements.

**Why it waits:** the torch GPU path is faster solo on the M3 Ultra (MLX
8.4ms vs ANE 11ms at L64), simpler (one stack), and sporadic gate judgments
don't contend with generation. The ANE path was functionally proven (correct
picks, parity within gate, 100%-ANE/0-transition plan, 30× over CPU) but its
silicon proof never landed: `ane_power` read exactly 0 across 96 samples over
three runs while the GPU meter breathed. Either the M3 Ultra sampler is dead
or E5 ignores compute units. Also, AMD 395 (T4.4) needs an XDNA stack anyway —
laya-apple is Apple-only — so NPU work restarts there regardless.

**Resurrect:** `pip install 'laya-apple[ane]==1.6.3'`; build artifacts in a
one-shot torch-2.7 jail (`python3 -m venv /tmp/laya-build`, install
`laya-apple[ane,convert]==1.6.3`, `laya-apple artifacts build
laya-typed-decisions`, `laya-apple calibrate laya-typed-decisions`); restore
the parked sidecar over `python/decision/`; close the meter question first
(idle-baseline vs sustained-ANE vs MLX-GPU power runs, or Xcode Instruments
Core ML trace, or an upstream laya-apple issue with the 96-zero evidence).

## AMD NPU (T4.4) — deferred until 395 hardware arrives

Decision runtime + `hw` detection for the Ryzen AI 395 NPU. Needs XDNA stack
research (no laya-apple equivalent assumed). Revisit with hardware in hand.

## Engine web search (Phase 8) — mapped, deferred

Full search for code help online. Options: (a) auto-launched headless Chrome
via DevTools, available whenever the harness is online; (b) SearXNG bundled
with first-install launch tasks. Either way needs programmatic HTML→text or
markdown cleanup that preserves code readability. Fetch-only (T1.4) covers
direct URLs until then.

## Subagent session files — deferred (T3.4)

Per-subagent session transcripts. Parent launch record suffices for now;
revisit when debugging fan-outs needs child-level replay.
