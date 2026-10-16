# Tool-call repair — 2026-10-15

Owner request: forgive opening calls across tools, let native D1 identify failed intended calls while deterministic code reconstructs them, then rerun the unchanged Snake harness test. No goals or unrelated features. Publication and platform equalization depend on successful live validation.

## Implementation contract

- Harmless opening navigation is declared by its owner, not inferred from a zero-parameter schema. Current owners are initial `plan` and `help`/schema. Unsupported incidental fields can be discarded with an explicit receipt. Supported fields keep their values and ordinary validation.
- A complete, unambiguous failed invocation can be offered to D1 with candidates assembled from currently available tools and the worker's actual arguments. D1 selects a candidate or leaves the call unrepaired. It cannot write arguments or borrow stale values.
- Reconstruction reuses existing aliases/coercions, then owner normalization/binding, authorization and execution. Missing required code, paths or commands remain missing. No repair after an execution failure or denied authorization.
- Native reply IDs/original bytes remain preserved. Effective-call and judgment receipts precede effects. Native array restrictions apply to repaired tool names before any call runs. Partial, ambiguous or mixed-protocol responses cannot be executed through repair.
- Reuse current D1 context admission and confidence handling. Include current failed call and candidate schemas as indispensable evidence; preserve relevant worker context and disclose any capacity-driven omission. D1's 32k context cannot contain an arbitrary 262k worker context in full.

## Work and evidence

1. Opening owner/helper: implemented in parallel; focused tool, planning and help tests pass. Runner integration pending.
2. D1 typed candidate selection: independent implementation lane; generic native/text integration in opening lane.
3. Root review: check candidate semantics, trace provenance, array preflight and normal authorization. Meaningful regression tests cover malformed, ambiguous, denied, unavailable and low-confidence cases.
4. Run Mac build/vet, affected suites and full suite. Record precise outcomes, not inferred passes.
5. Build isolated candidate; rerun exact Snake brief and baseline with Laguna and native D1. Preserve old attempt and archive any operator transition. Rubric distinguishes parser/repair, PAT, edits, checks, review, handoffs and D1; unexercised stages remain unexercised.
6. Publish/equalize only after successful test evidence, as requested.

Current original experiment remains unchanged while implementation proceeds. Neither operators nor subagents modify its game, graph, running binary or prompt catalog.

## Validation progress

- Component opening tests and runner repair/authorization tests pass. Full agent suite on finalized source: 24.450s; prompts: 0.859s. Build, vet and formatting pass.
- Native D1 GPU replay of actual captured repair request selected `read` for an unknown `inspect` call while preserving its supplied path, confidence 0.9241418199787566. This is a component inference result, not a Snake/PAT pass.
- The first restricted suite could not bind local test listeners. An early broad run also caught root's receipt catalog format mistake (`%s` instead of owned template fields); concurrent catalog editing caused embedded/disk mismatch in that run. Fixed the template fields, froze source, and launched a fresh complete suite. Earlier failed logs remain evidence; no live candidate was deployed from them.
- Review corrected text opening result classification and ensured JSON receipts retain existing data/effects. D1 candidates include current schemas, actual failed reply and continuation. Candidate labels are compact; argument payloads are not duplicated into option labels.
- Fresh same-task fixture prepared, exact brief and seven-file original baseline preserved; no new run submitted before final gate. Original run remains unchanged and has not edited files or entered PAT as of03:38UTC.

Final frozen full suite:31 tested packages pass,0fail; `internal/run`264.525s. Candidate launched and exact task submitted once. At03:45UTC fresh run active with Bash3/read6, D1 productive-discovery Continue0.933, no edits/PAT/check/review/handoff yet. Old experiment was normally cancelled only to transition to requested rerun; partial output preserved. No GitHub publication; success condition pending.

## Upstream framing recovery correction

Owner explicitly approved failure ordering: failed tool call → worker paused → native D1 selects intended valid reconstruction → deterministic reconstruction → normal authorization/execution → worker receives result and resumes. Parser interpretation itself stays unchanged.

The prior two reruns did not exercise repair because native framing errors ended the stream before runner repair. Exact drafts preserved:
- `plan><arg_key></arg_key><arg_value>{}</arg_value>`
- `plan<arg_key>argument_keys></arg_key><arg_value>{}</arg_value><arg_key>argument_values>`

Implementation: typed failure evidence travels from owned worker through client; only settled tool framing failures qualify, never unrelated network loss, output overflow or cancellation. Generic deterministic reconstruction retains authored argument data, current registry visibility and explicit harmless-opening ownership. D1 chooses; it cannot write missing source/commands. Original response and raw failed draft are recorded before effects. Existing array, binding, authorization, session and tool owners remain in control. Unreconstructable calls return feedback to the worker without executing the refused array. Tests must cover both captured drafts end-to-end and all registered parameter shapes, denied/cancelled/ambiguous cases, and real native D1 selection. Fresh workload only after checks; update owned worker runtime and verify loaded-module hash to avoid stale server code.

## October 16 correction: scoped repair and decision admission

An identifiable current tool stays the sole repair target. Missing semantic arguments return the owning tool's required-answer feedback; empty arguments do not redirect plan to check/find/help. Unknown tool names retain the existing candidate-selection route. No D1 selection invents missing arguments.

Decision requests carry configurable decision.max_input_tokens (default15,999) and the actual caller deadline. Native token counting covers context, instructions and choices. Capacity feedback uses the existing optional-evidence reduction and retains original history. Busy native inference reports unavailable without queueing; expired requests never begin, and late verdicts are discarded. Active GPU forwards cannot safely be preempted. This change does not establish why the previous D1 process exited.

PAT receipts now include the next question/schema from the same owner used for the next request, avoiding empty-call polling. Current test evidence is in verification/2026-10-16-pat-d1-fixes. The owner stopped Laguna; no restart without fresh approval.
