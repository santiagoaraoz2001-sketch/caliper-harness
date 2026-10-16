# Snake trace corrections — 2026-10-15

Owner authorized implementation and publication on main, Windows and Linux. Scope is the October15 supervision-fixed run's trace findings, not another game-development run.

## Work and acceptance

- Fixture isolation (parallel-safe, scripts/tests only): a fresh copied workspace owns its Git baseline; bare diff cannot resolve to a parent repository. Preserve historical fixtures. Demonstrate parent contamination before and isolated diff after.
- Recovery and instruction coherence (parallel-safe, product sources/prompts/tests): replace guessed decision server commands with selected-service recovery; describe already-applied work accurately through planning, claim, checks and review. Keep actual current check/reviewer requirements. Ground behavioral checks in assertions; do not forbid a shared suite that contains the relevant regression.
- Review and integration (sequential): inspect all changes, run targeted controls and build/vet/full tests; preserve negatives. Verify shared source equality across branches, merge main into platform branches while retaining platform files, then push all three.

No new limits, tools, forced reasoning removal, automatic task qualification, or deployment. No change to 50k output-age pruning or90% emergency compaction. Native model improvement is unproven until another matched run; do not claim this patch eliminates the112-minute workflow overhead.

## Original evidence and reporting corrections

Latest observed run repaired source and reached19/19 before formal task claim. It then spent~112minutes between last successful check and claim. A65032-character unrelated parent Git diff polluted context. Hardcoded8090 recovery disagreed with actual selected18103 relay. Both task acceptance/project commands were npm test; standalone diagnostics printed booleans, not executable assertions. Those are not proof the actual19-test suite was weak. D1 was operator-restarted. EOF does not establish OOM. Prompt100124 was below configured262144×90% trigger, so zero compactions alone is not a bug. Full raw audit retained locally at verification/2026-10-15-supervision-fixed-native/POSTHOC-TRACE-AUDIT.md.

## Validation

Independent review confirmed scoped corrections. Two fixture controls pass; preimage recovery/prompt controls fail as expected; corrected handoff and no-retry controls pass. Root broader gate passed30packages; obsolete pre-fix runner process was interrupted. Final agent package passed25.919s; complete final runner exercised all tests and failed only an obsolete exact-wording assertion (262.112s). That assertion was updated without removing state/action checks and passed independently. This is combined current-test coverage, not a single all-green whole-suite invocation. Build/vet/format/diff checks passed. No live-model performance claim.

## Test rubric for this correction

| Domain | Scope and result |
|---|---|
| F01 Setup/lifecycle | No live server change; fixture Git baseline tests pass |
| F02 Transport | Unchanged; no model inference in this validation |
| F03 Prompt interface | Existing-work/recovery consistency corrections; targeted regression passed |
| F04 Discovery | Dirty-parent diff reproduced; isolated fixture exposes only its own changed file |
| F05 File changes | Historical/source fixtures preserved; product edits limited to recovery and instructions |
| F06 Commands | Real Git fixture commands tested; no new execution restrictions |
| F07 Tool conversation | Existing mechanisms unchanged; unchanged-package suite passed |
| F08 Authorization | No policy change |
| F09 PAT | Component checks pre-claim artifact preservation, fresh checks, review and two-task handoff |
| F10 Graph/knowledge | Existing owner retained; no schema change |
| F11 Supervision | Wrong guessed-port recovery removed; learned judgment quality not retested |
| F12 Context | No pruning/compaction/reasoning policy change; fixture removes accidental parent diff source |
| F13 Sessions | Raw historical evidence retained |
| F14 Verification | Current paired checks/review remain required; no historical pass promoted |
| F15 User interaction | Recovery identifies /status as user action, not a model tool |
| F16 Feedback/evolution | Unexercised |
| F17 External retrieval | Unexercised |
| F18 Help/recovery | Main/worker/help/planning/checkpoint instructions aligned; targeted checks passed |

No claim of reduced Laguna latency or autonomous end-to-end success without a matched native rerun. Server exit cause remains undetermined; operator restart is not autonomous recovery.

## Discovered completion defect

The preimplemented-first-task control exposed a real cross-task collision: the run loop remembered automatic check attempts by revision integer alone. Different tasks can share that revision, causing the second task's required check to be skipped. Bind the existing attempt marker to TaskID, Attempt and Revision. Do not retry a failed/denied check within the same identity/revision. This directly supports pre-claim work followed by normal task verification; it adds no new policy or feature.

Independent reviewer confirmed cross-task identity fix and same-task failed/denied check no-retry controls. Python fixture tests pass. Native Windows/Linux runtime and a matched Laguna rerun remain unperformed.
