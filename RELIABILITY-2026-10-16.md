# PAT and native D1 reliability corrections — 2026-10-16

The owner stopped the Laguna Snake diagnostic and prohibited another Laguna run until explicitly authorized. The owned controller recorded normal shutdown; that process exit is not task success. Source, session and wire evidence remain in the local verification folders.

## What failed and changed

**D1:** macOS killed PID46540 under compressor-space pressure, reporting 143944 MB. The failing request had 15755 native tokens, below the configured15999 ceiling. The native checkpoint already uses inference mode. Its full-chain MPS attention creates quadratic temporary storage (one fp32 score tensor at that shape is31.8GB). Exact attribution of the entire OS-reported peak to a single operator was not captured.

The correction tiles queries using the native checkpoint's existing CHUNK, retaining all keys/values and absolute causal masking. Each tile settles before the next. The hook is local to the native hybrid module, applies only to MPS non-windowed attention, and leaves other devices/modes unchanged. No new input limit, model change, prompt change, CPU fallback or restart policy. Bundle file lists include the new module.

**Premature handoff:** Task A was claimed and its paired checks passed18/18; the model focused Task B before review/settlement. That changed the available tool view while A remained assigned, hiding edit/check and stranding completion. The Runner now preserves active execution focus when inspecting another node. The result explains the requested page is inspection, A remains claimed, and a normal final answer leads to existing independent review before handoff. Unclaimed/settled navigation is unchanged. No automatic claim or inferred verification.

**Planning:** complete every Task brief for the current Approach before readiness/claim. `more:false` finishes subtask drafting and asks for later review criteria; it never starts execution or review. Drafted and qualified Task states have separate model-facing guidance. Existing graph owners handle next Task, residual work, next Approach and Plan closure; qualification counts alone do not establish semantic coverage.

## Validation scope

- The handoff regression failed before the fix; afterward A checks/reviews/qualifies and B stays queued at attempt0. Active/same/unclaimed/settled focus cases pass.
- Three attention tests pass, including full15755-token shape interception: sixteen calls, at most1024 queries each, all15755 keys retained.
- MPS bf16 tensor equivalence at257/2049 tokens: maximum difference0/4.77e-7.
- Actual small native D1 judgment passed on mps:0:0.247s,7.31GB driver memory, hook installed. This is runtime evidence, not decision-quality qualification.
-47 decision Python regressions pass. Worker continuation16 and server6 tests pass; framing10 pass/10 optional captured-Gemma cases skipped. A first sandbox invocation could not access Metal; isolated unsandboxed component runs passed. No Laguna model was loaded by these tests.
- Full captured15755-token D1 replay remains unverified: automatic approval rejected it because it could repeat severe memory exhaustion. No workaround/retry was used.
- Full Go run: all packages passed except one stale help-text golden expectation; its exact wording was corrected and the tools package rerun passed. Build/vet/format passed. Windows amd64 components and process test executable cross-compiled; Linux amd64/arm64 components cross-compiled and launcher syntax passed. These do not establish native Windows/Linux application execution.
- Shared fixes are synchronized to main, codex/windows-conversion and codex/linux-conversion, preserving platform-specific files and the newer remote README changes. The independent windows WIP branch is untouched.

## Capability rubric for this repair validation

Component passes below do not promote the stopped native run to success.

| ID | Function | Result | Observation |
|---|---|---|---|
| F01 | Setup/model lifecycle | Partial | D1 OS kill confirmed; small corrected GPU judgment passes; full-size replay unverified. |
| F02 | Model transport | Component | Existing transport/cancellation tests included; no new Laguna run. |
| F03 | Prompt interface | Component | Planning and focus guidance aligned with actual execution authority. |
| F04 | Source discovery | Component | Inspection remains available while execution stays bound to active Task. |
| F05 | File changes | Component | Claim-bound coding regression retained; native workload not repeated. |
| F06 | Command execution | Component | Fixture checks execute real local commands. |
| F07 | Tool invocation | Component | Recorded malformed-call recovery and registry tests included. |
| F08 | Authorization | Component | Existing approval rules retained; focus grants no new authority. |
| F09 | PAT/orchestration | Component | Multi-Task/Approach fixtures and premature-focus regression pass. |
| F10 | Graph/state | Component | Existing owners and explicit partial effects; no parallel ledger. |
| F11 | Progress supervision | Partial | Small native D1 works; large-input advice quality/latency unverified. |
| F12 | Working context | — | No new long-context worker experiment. |
| F13 | Sessions | Component | Existing continuation/session tests; stopped evidence preserved. |
| F14 | Verification/completion | Component | A qualifies in regression with independent reviewer double; no live reviewer PASS claimed. |
| F15 | User interaction | — | No fresh end-to-end TUI qualification. |
| F16 | Feedback/evolution | — | Not exercised by this repair. |
| F17 | External retrieval | — | Not exercised by this repair. |
| F18 | Guidance/recovery | Component | Inspection-versus-handoff receipt supplies the existing legal completion route. |

Detailed local evidence: verification/2026-10-16-d1-crash-investigation, verification/2026-10-16-pat-handoff-fix, verification/2026-10-16-managed-pat-pipeline. Raw model traces and local session stores are not published.
