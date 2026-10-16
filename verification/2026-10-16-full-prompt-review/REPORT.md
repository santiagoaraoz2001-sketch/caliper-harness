# Full prompt review and completion correction — 2026-10-16

Owner scope: fix completion contradictions, review the entire prompt YAML, route generic Bash instructions to the actual available tools, preserve silent Continue, publish; no native rerun until the owner authorizes it.

## Review coverage

Read all 3,221 original YAML lines sequentially, not just search hits: every system role, template, decision instruction, preparation recipe, continuation, verification/repair string, discovery guide, help section, tool/parameter description, planning phase and reference document. Cross-checked the actual planning, claim, check, settlement, tool registry, help, style-sendback and prompt-loader owners. Also read all six legacy templates, the model-guide pointer, and Python's shared loader. `CATALOG-REVIEW.md` inventories all 467 final entries with their disposition. Search was a final cross-check of Bash references and call sites, not the review method.

## Findings and patches

| Finding | Correction | Evidence boundary |
|---|---|---|
| Running Task classified as stopped; STATUS offered recovery instead of review handoff | Separate running presentation phase, preserve STATUS as read-only navigation; stopped/cancelled retain explicit recovery | Regression fails on pre-fix code; current graph transition/receipt test passes |
| Generic navigation says answer only next question, competing with final-answer review | Neutral navigation receipt; running guidance explicitly says normal final answer, no tool call, independent review | Captured failure reproduced in a scripted two-task lifecycle |
| Worker COMPLETE wording suggests a nonexistent completion action | Consistent final-answer handoff in worker/main/plan/help/reference/progress text; actual paired-pass receipt carries the same cue | No new completion tool or state transition |
| Handoff asks for fresh check even when the current episode already passed | Reuse current pass unless later effects invalidate it; show check result and next action | Existing currentness/paired-episode tests remain; handoff fixture exercises explicit check then STATUS then final |
| Generic text prompt teaches shell calls during planning | Executable read-directory example; multiline syntax remains documented; current-tool condition explicit | Real emitted example is parsed, validated and executed against a temporary directory |
| Bash suggested for file inspection and declared verification; brief fields called Bash checks | Direct discovery to read/find_file/find_dir/grep, changes to edit/write, declared verification to check | Actual Bash tool retained for other commands when advertised; no tool removal |
| Check help implies ordinary coding needs both defaults; broad effects prose suggests inspection invalidates checks | Distinguish ordinary cmd from planned paired defaults and modifying effects from inspection | STATUS after passed checks still permits real review/handoff |
| Post-settlement style send-back tells model to edit immediately | Existing explicit rework/requeue/readiness/fresh claim before edits | Prompt corrected; no live style send-back qualification claimed |
| Repair direction described historical refusal as sufficient | Require unresolved PendingRepair, current schema and owner facts; clarify passed-check next action | Prompt-only improvement; actual D1 decision quality remains unmeasured |
| Help instruction uses ambiguous unquoted help(topic=help.edit) | Exact help(topic="edit") | Correlated refusal/next-request test updated and retained |
| Unloaded .tmpl files contradict YAML (word quota, one-call reviewer, stale roles) | Replace inactive copies with pointers to canonical YAML | Loader already ignores standalone .tmpl; no runtime capability removed |
| Guiding principles retain superseded draft-coding sentence | Match recorded selected-PAT drafting/claim boundary | No policy change |

Earlier uncommitted preparation fixes are included: stable preparation system prefix and native plan advertisement, exact current question at the conversation tail, arguments-only examples through the existing shared formatter, and successful plan receipts counted as preparation progress. The existing native D1 health diagnostic decoder patch is included so the published harness accepts its already-shipped sidecar response.

## Bash reference audit

No generic role or planning instruction teaches a Bash invocation. Remaining text references have specific owners: `help.bash` documents the actual callable tool; `guidance.bash.missing_shell` diagnoses its real executable dependency; `tools.bash.max_bytes/max_lines` are real configuration keys shared by output tools. Those names were retained rather than claiming nonexistent configuration or removing command capability. Legacy source paths/comments identify code owners only.

## Validation and preserved negatives

- `before.log`: new running-state regression fails: actual phase stopped.
- `focused.log`: first focused attempt exposed two test setup issues: rendering a parameterized template without owning values, and the longer scripted lifecycle triggering its unrelated 32k fixture compaction path. Corrected the test to inspect catalog text and use the actual diagnostic run's 256k fixture window; no production limit changed.
- `focused-corrected.log`: run, agent, prompts and tools selections pass, including real Kuzu/claim/check/reviewer invocation/handoff with scripted models, silent Continue, and low-confidence silence.
- `full-suite.log`: full local package run retained, including old test assertions tied to the removed Bash example, old help syntax, and mandatory repeated fresh-check wording. Tests updated to assert the corrected behavior; production code unchanged.
- `agent-final.log`: complete agent package passes after test corrections.
- `prompts-plan-final.log`: complete prompts and plan packages pass, including canonical-template loading after removal of inactive copies.
- `python-prompts.log`: five shared Go/Python catalog tests pass.
- Build/vet/format/diff checks pass. Final suite summary is appended at closeout.

This is component evidence. Review verdicts/model replies in lifecycle tests are explicit doubles; actual graph mutations, filesystem operations, shell checks and independent-review invocation are real. No native Laguna or D1 inference ran. No performance or native completion improvement is claimed.

## Capability rubric — this change's focused component verification

| Function | Result | Observation |
|---|---|---|
| F01 Setup/model lifecycle | LIMITED | Shared catalog loading and native D1 health-response decoder tested; no residency test |
| F02 Transport | LIMITED | Local fixture wire requests validated; no model inference |
| F03 Prompt interface | PASS | Full catalog reviewed; role/template consistency and emitted examples tested |
| F04 Discovery | LIMITED | Actual read-directory example executes |
| F05 File changes | LIMITED | Existing-artifact preservation and second artifact write in lifecycle fixture |
| F06 Commands | PASS | Actual declared checks execute in fixture workspace |
| F07 Tool conversation | PASS | Correlated check/STATUS receipts reach next request; valid calls preserved |
| F08 Authorization | — | No new live authorization exercise |
| F09 Orchestration | PASS | Two real claims and guarded task/approach/plan handoffs under scripted model |
| F10 Graph/knowledge | LIMITED | Actual Kuzu lifecycle and current help references; no native retrieval assessment |
| F11 Decision supervision | LIMITED | Continue and confidence below 0.5 remain silent; new wording's judgment quality untested |
| F12 Working context | LIMITED | Stable preparation prefix/schema regression; no native cache measurement |
| F13 Sessions | LIMITED | Existing continuation/resume regressions; no new live session campaign |
| F14 Verification/completion | PASS | Actual paired checks and independent reviewer invocation; synthetic PASS, not native judgment quality |
| F15 User interaction | — | No live TUI run |
| F16 Evolution | — | Outside scope |
| F17 External retrieval | — | Outside scope |
| F18 Guidance/recovery | PASS | Current phase/help/schema guidance agrees in tested paths |

Outcome: demonstrated instruction contradictions corrected with existing owners; no new limits, parser behavior, completion tools or supervision messages. Remaining uncertainty: model response to corrected instructions, D1 semantic progress choices and real cache performance require the next authorized native run. The old run remains stopped, with evidence preserved.

## Final validation

Final gate: 31 Go packages with tests pass across the full run and corrected complete agent/prompts reruns; generated/no-test packages excluded. Full run package PASS295.740s. The first aggregate command remains recorded as failed on obsolete assertions, not relabelled green. No production changes after that suite started. Build/vet/gofmt/diff clean; Python prompt-loader5 tests PASS. Native model rerun remains unperformed by owner instruction.
