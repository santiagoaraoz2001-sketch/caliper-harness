# Caliper Harness — capability rubric

This is the standard report for each test run or live experiment. It evaluates product functions, not Go function counts. The objective is a small local model completing correct coding work through the harness without hidden operator rescue.

## Status and evidence

**PASS** = the declared assertion was observed. **FAIL** = it was exercised and failed. **LIMITED** = a useful part was observed but the declared capability was not fully exercised. **—** = not exercised in this run. **BLOCKED** = a prerequisite prevented a valid test. **UNIMPLEMENTED** = a planned function does not exist yet.

Always give the evidence level: **component**, **real CLI/TUI**, or **native model**. A scripted model/component pass does not establish native capability. A past result remains attached to its original candidate; it does not become a current pass after edits. No percentage score is computed from untested rows or package counts.

## MECE function map

Every capability has one owning row. Rows separate stages and responsibilities; the same observation is not credited or counted as a failure in several rows. Downstream consequences can be recorded without treating them as additional root causes.

| ID | Function | Owns | Pass condition for its declared scope |
|---|---|---|---|
| F01 | Setup and model lifecycle | Install/assets/dependencies, config validation/save, hardware, discover/select/load/unload, sidecar identity/device | Supported setup runs; saved choices persist; selected models are genuinely resident/ready and external workloads are preserved. |
| F02 | Model transport | Request parameters, inference route, streaming, usage, cancellation/inactivity/output bounds | Actual wire/runtime agree with requested settings; complete replies arrive; interruption settles with honest partial evidence. |
| F03 | Prompt interface | System/role construction, editable YAML, tool descriptions, help injection frames, user/Behavior/profile loading | Only applicable instructions reach the correct role; edited YAML takes effect; current facts remain factual; prompt overhead is measured. |
| F04 | Source discovery | File/directory navigation, read, content search and source locators | The model finds and reads the responsible source with useful locators and truthful bounded results. |
| F05 | File changes | Write/edit, current hash/range-read admission, exact bytes/newlines, edit previews | Requested changes affect the intended source; stale/unread edits refuse; receipts match actual file bytes. |
| F06 | Command execution | Bash process execution, output capture, timeout, exit status, process settlement | The exact approved command runs in the right workspace; output/exit/effects and stopped children are recorded accurately. |
| F07 | Tool conversation | Native/text parsing, validation, call/result correlation, serial arrays, refusal repair and continuation | Valid calls execute once and receive associated results; malformed/partial calls have no invented effects; next requests receive current feedback. |
| F08 | Authorization and user decisions | YOLO/all/risky policy, judged/free lists, approvals/denials, approval context and Ask answer association | Every effect follows the selected policy; judgment/approval applies to actual arguments; answers bind to the current question; denial and uncertainty never grant authority. |
| F09 | Work orchestration | Plan→Approach→Task briefs/lifecycle, readiness/claim, role transitions, task comments, concurrency, stop/requeue | The model prepares and executes real bound work legally; workers/roles and partial outcomes settle; metadata is not mistaken for execution. |
| F10 | Persistent graph and knowledge | Kuzu ownership/durability, public GraphQL typing/composition/limits, product facts, memory/recall/references | Acknowledged data survives reopen; actual IDs/partial commits are disclosed; useful authored facts can be retrieved without fabricating verification. |
| F11 | Decision supervision | Progress/bookkeeping/product alerts, ordinary/thinking loops, decision confidence/admission and remediation guidance | Correct triggers use relevant facts; Continue and supervisory confidence below 0.5 send no message; other valid directions nudge without stopping or discarding reasoning. Model confidence cannot manufacture execution or success; reasoning length alone is not failure. |
| F12 | Working context | Occupancy/admission, pruning/compaction/relevance, factual preservation, protected system instructions | Necessary intent/source/failure facts survive cleanup; stale material retires; actual counts and estimates are distinguished within limits. |
| F13 | Sessions | Event log, bounded snapshots, resume reconstruction, explicit-use retention | Save/reopen/resume preserves real calls, outcomes, task identity and failures; retained history does not restore invalid authority. |
| F14 | Verification and completion | Frozen behavioral assertion, paired project check, independent review, qualification and truthful final result | The assertion distinguishes the requested behavior; actual current checks and independent source-based review support the final outcome. |
| F15 | User interaction | TUI navigation/layout, current Ask display/input routing, composer/quotes, status/settings/decisions/files/diffs/workflow/session views | The user can understand and operate the real views at supported sizes; displayed facts and references match actual state. |
| F16 | Feedback and evolution | Planned profile/evolve analysis and versioned user/Behavior document updates | Real supported feedback changes the intended documents and subsequent request; stale/cancelled proposals preserve current contents. |
| F17 | External retrieval | Supplied-URL fetch, preserved code/attribution, planned installed-Chrome web search | Live reference content and resolvable search URLs arrive faithfully within bounds; external content never gains execution authority. |
| F18 | Guidance and recovery | Model/user help, tool recipes, actionable failure recovery, planned error wiki and usable release guide | The model/user can find the needed guidance and take a legal useful next action; explanations and published claims match actual behavior. |

Boundaries: F01 decides/stores runtime settings; F02 proves their effect. F03 assembles instructions; F12 preserves working context; F18 evaluates whether guidance is findable/useful. F06 records command execution; F14 judges verification relevance and completion. F09 owns business lifecycle; F10 owns data/API durability. F11 decides enforcement; F08 decides permission and associates user decisions with questions. F15 evaluates interaction/display, while underlying values stay with their functional owner. Web search belongs to F17, even though its implementation is Phase 8. Evolution document writes belong to F16, rather than ordinary source edits in F05. Functionality/5k experiments exercise these functions and the engineering constraints; they do not create a duplicate function score.

## Report after every run

Record **run ID/time; candidate/source and prompt version; task/oracle; evidence level; model/runtime/device; effective sampler/context/output and budgets; operator interventions; raw evidence**. Compare against a named baseline only when inputs and changed factors are explicit. Say “unknown” when the run did not measure something.

Show the complete eighteen-row table with `Function | Result | One concrete observation`. Untested rows stay visible as —; do not carry historical green cells into the new run. For combined runs, show separate result columns rather than average away a failure.

Then give only:

1. **Task outcome:** requested behavior correct/incorrect/unmeasured, actual checks, qualification, and preserved work.
2. **Change:** which rows improved, regressed or remain unmeasured against the named baseline.
3. **Cause and next action:** one primary row per failure, evidence for proven causes, explicitly labelled hypotheses, and the next discriminating fix/test or owner question.

An infrastructure failure is BLOCKED with its cause; it is not a model failure. A model's inability to complete is a FAIL only in the capability it actually exercised. No test-only rescue, hidden hints, changed oracle or empty selected test may earn a pass.

## Engineering constraints, reported separately

Build/vet/format and unit/race checks, handwritten code/dependencies/readability, startup memory/latency, model-facing overhead, preservation of user work and the Mac/small-model restriction remain gates from guidingprinciples.md. They support capability; they are not extra function scores. Run required checks once for a settled candidate, preserve negatives, and do not expand the system to improve the report.
