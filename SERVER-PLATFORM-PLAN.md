# Server/model usability and platform conversion

Owner request: 2026-10-15. Implementation started; no completion claims. This is the current scope, superseding the earlier feature freeze for these six requests. No goals, downloads, commits or live service replacement. Owner approved ordinary chat models through a validated adapter.

## Design

Extend existing serve recipes, probes and process owner. One server inventory and lifecycle implementation serves independent worker and judge selections. Distinguish installed executable, responding endpoint, catalog model, resident model and selected role. An OpenAI model listing alone never establishes residency or typed judge compatibility. User-selected custom recipes use argument arrays, not generated shell strings. Discover existing models; do not download them.

## Ordered work and acceptance

- [ ] S1: server inventory and discovery. Identify installed executables and configured/conventional endpoints; preserve observed identity and diagnostics. Avoid blind port scans or launching during discovery. Verify missing executable, endpoint collision, unreachable server and healthy catalog with isolated fixtures.
- [ ] S2: model lifecycle and custom servers. Reuse existing recipes and native lifecycle adapters; allow explicit model argument substitution for custom servers, validate paths, retain external-process ownership. Catalog/select/load/unload must have distinct actions and receipts. Add installed GGUF discovery alongside existing local MLX/HF and Ollama roots. Verify paths with spaces, no implicit downloads, failed load and release.
- [ ] S3: shared worker/judge selection and UI. Expose server then model selection for each role using shared inventory. Preserve requested versus active identity. Native decision protocol remains explicit; owner approved ordinary chat models through a validated adapter. A normal chat endpoint must not silently masquerade as the typed decision service. Verify role isolation, save/reopen, active-run stability and owned/external activation.
- [ ] S4: Windows conversion branch codex/windows-conversion. Isolate OS-specific process/shell/hardware/paths, add Windows launch/build packaging and Kuzu DLL handling. Compile checks are not native Windows qualification. Real Windows build, launch, subprocess cancellation, Kuzu persistence and local-server exercise remain required.
- [ ] S5: Linux conversion branch codex/linux-conversion. Reuse Linux hardware support, add relocatable build/install/launch paths and Kuzu shared-library handling. Verify native Linux startup, graph persistence, child cleanup and server exercise on an actual Linux host before qualification.
- [ ] S6: integration and capability rubric. Run relevant regression tests plus Mac build/vet and real existing server discovery. Exercise select/load/unload on owned test services without disturbing unrelated servers. Carry platform evidence separately; no cross-build-as-runtime claims.

S1/S2/S3 share configuration/TUI seams and implement sequentially. Platform audit and independent server audit run in parallel with disjoint evidence files. Platform implementation branches share a common base; do not fork the whole product into duplicate implementations. Explicitly record native-host prerequisites instead of claiming untested ports complete.

## Initial verified state

Clean Git main at b5f61fc, now a real repository (older untracked history is stale). Existing serve recipes already support custom argv; Status already probes configured/conventional servers and loads Ollama/owned MLX. Decision uses separate /judge protocol with Laya/System One adapters. Current fixed-parser Laguna case ended on advisory D1 timeout after Task claim, before edits; this is a separate outstanding defect, not a server-portability result.

## Refinements register

- R1: generic OpenAI /models is catalog evidence, not residency; expose unsupported lifecycle explicitly.
- R2: worker chat and decision typed-choice contracts differ; model/server parity must preserve judgment integrity.
- R3: platform branches need actual native Kuzu libraries/toolchains; Mac-only success cannot qualify Windows/Linux.
- R4: discovery must respect custom cache/model directories and not confuse checkpoint format with runtime compatibility.

## Turn log

2026-10-15: read current owners/config/UI; checked clean Git/current HEAD; launched independent server and platform audits. No live service mutation. Research uses upstream Gufo docs: https://github.com/gufo-org/gufo/blob/main/docs/SERVER.md and CLI. Plan is execution scope, all acceptance unchecked.

2026-10-15 implementation wave: created codex/windows-conversion at ../Caliper-Windows and codex/linux-conversion at ../Caliper-Linux, both from b5f61fc; no commits. Windows executor owns process/hardware portability in its isolated checkout. Main server executor owns config/serve/Status/Settings/startup, chat-judge executor owns decision/prompts; disjoint files. Root owns docs/review/tests. Linux packaging follows the next free executor slot. Generic judge selection uses explicit openai runtime; its confidence is model-reported and must not be described as calibrated native probability. No native device/health is fabricated for a generic server.

## Current implementation status (end of first wave)

All six requests are mapped. The source implementation is started and validated to the levels below; unchecked acceptance is deliberate, not a completed release.

| Work | Delivered | Remaining qualification/work |
|---|---|---|
| S1 detection | Known installed launcher discovery; configured/conventional endpoint probes; visible identity/capability distinction | Arbitrary vendor app/config discovery is not implemented; add provider discovery only from documented conventions |
| S2 lifecycle/custom | Existing Ollama/owned MLX retained; GGUF/HF discovery; custom argv model binding; external endpoint entries; per-recipe candidates | Real Gufo and other custom native load/unload; custom entry uses YAML; generic catalog cannot prove residency |
| S3 judge | Shared catalog judge selection, settings, generic typed-verdict adapter, confidence provenance | Native local candidates now stage through Status and existing activation; ordinary judge quality requires nontrivial evaluation |
| S4 Windows | Separate branch/worktree; hardware probe, owned Job Object implementation/tests, developer build/launch scripts | Actual Windows compiler/CGO/DLL/runtime/cancellation/filesystem/TUI tests; PowerShell scripts unexecuted here |
| S5 Linux | Separate branch/worktree; native developer build/launch scripts, dependency diagnostics | Native Linux arm64 Kuzu persistence passed; full Go build, relocation, TUI/server tests remain |
| S6 verification | Full Mac Go suite31 packages pass, build/vet/format pass, actual TUI selection/save, real MLX GPU adapter smoke | Broad live lifecycle and judgment-quality matrix; Windows/Linux host details requested |

Refinements completed: distinguish metadata candidate from native compatibility; preserve exact custom argv; same local model under each matching server; generic judge bypasses native sidecar start/health; label self-reported confidence; generic catalog Enter selects worker instead of offering unsupported load. No policy/default changes to timeouts, output budgets, scan budgets or coding execution.

Live model evidence: intended SmolLM2-135M returned prose and was correctly refused. Incidental Granite3B selected by test-operator error returned a valid verdict. Both are preserved, source/prompt unchanged, owned test servers reaped. Neither arithmetic smoke establishes authorization/readiness/progress judgment quality. MLX accepting response_format does not prove constrained JSON output.

Wave 2 refinements R5/R6 implemented and reviewed: Decisions records and summaries retain confidence provenance; native typed judge candidates reuse local discovery and existing staged activation, without inventing a model-list API. R7: private Python/runtime packaging on Linux/Windows remains a separate installation gate; current scripts produce dependent developer folders.

No production installation replaced; current source candidate is /tmp/caliper-server-platform. Branch changes remain uncommitted. Shared changes are copied into both platform worktrees after review so the branches do not lose new server/judge support.

2026-10-15 wave 2: parallel executors added native judge candidate staging and confidence provenance display; root reviews and tests combined candidate. Existing Linux arm64 Docker image passed actual Kuzu create/insert/close/reopen/query, but lacks Go and ripgrep, so this is library evidence only. Isolated owned Mac MLX lifecycle test underway. No installed app replacement, goal, commit or model download.

Wave 2 validation: real owned Mac MLX load/readiness/stop passed with exact cached checkpoint, GPU device and independent PID/endpoint settlement. Native judge catalog regressions and stale activation race control pass. Build/vet/format and shared Windows/Linux package cross-builds pass. Final full suite passed: 31 packages, zero failures; no inference-quality or full native platform qualification inferred.

Wave 3 parallel scope: server executor owns recipe/discovery/Status code and tests; platform executor owns branch scripts/docs; judge evaluator owns evidence only. Audit practical detection→launch→role selection and repair demonstrated disconnects, inspect cached native prerequisites without downloads, and run six preregistered Granite3B GPU judge cases without prompt tuning. Root reviews and verifies combined changes.

Wave 3 findings: R8 server root versus /v1 API-base mismatch can lose recipe/owned-process identity and bind the wrong role model; fixing with stable recipe endpoint identity. R9 worker selection must roll back live derived fields when saving fails. R10 ordinary-chat judge omitted the existing params.max_output_tokens: Granite diagnostics yielded2/6 complete expected verdicts and4/6 truncated at MLX default512. Wire the existing configured allowance (current omitted-config default16384) across production chat-judge construction sites; no new default/sampler/timeout. Repeat the same preregistered cases once after tests. R11 Windows example template override broke relocation; corrected omission uses existing executable-relative lookup, native PowerShell still unrun. Installed Gufo/llama launcher choices are being connected to existing Status using the configured endpoint; no guessed ports or auto-start.

Wave 3 matched diagnostic: existing output allowance fixed delivery from2/6 to6/6;5/6 choices match preregistered expectations. Every request changed only max_tokens from absent to16384. Remaining R12: Granite judged eight repeated unchanged inspections as Continue0.95 because reads succeeded. This is observed progress-judgment failure; instruction ambiguity is a supported hypothesis, not proof of general model incapability. No prompt/model change or retry; do not qualify this judge for general supervision from these six cases. Owned GPU test process reaped and endpoint absence confirmed.

R13 discovered by actual wave3 TUI startup: with default watcher enabled in this large repository, startup exhausts descriptors before session sweep and Bubble Tea kqueue creation. Previouswave test explicitly disabled watcher; current shell allowance and host usage do not establish a lowered limit. Samebinary watcher-on/off and small-project controls underway. Proposed minimal repair: release failed watcher resources on actual descriptor exhaustion and return its error through existing WatchErr display, preserving UI operation without new caps/ignore rules. This is not a claimed TUI pass yet.

Wave 3 closeout: reviewed watcher resource-unwind fix passes real descriptor-exhaustion and repeated-close controls (root watch suite1.805s). Actual same-checkout TUI opens, displays Watcher off and exits0, with unchanged watch configuration; startup no longer fails creating the terminal reader. Large-tree watch coverage and dynamic post-start exhaustion are not qualified. Final Mac build/vet/format/diff checks pass. Root combined decision/serve/CLI/config tests pass and final TUI package39.260s passes; full31-package gate remains the prior wave2 result.

R14 observed catalog usability gap: Hugging Face datasets cache entries appear as invalid model candidates in the real Status view. They do not become compatible models; discovery filtering remains a follow-up. Shared-endpoint different-model start arbitration, generic progress-judge quality, real Gufo/llama inference and full native Windows/Linux application tests remain open.
