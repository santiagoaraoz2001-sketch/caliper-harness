# v0.2.5

This release fixes the three traced v0.2.4 limits (retained-context compaction, readiness re-asking, PAT knowledge visibility) and the further defects two fresh end-to-end benchmark runs exposed. It was exercised by two from-scratch Space Transporter benchmark builds; both stopped before the final tasks, stated plainly under Benchmark evidence. Those stops do not change the shipped behavior below.

## Behavior changes

- Retained context: consumed failure/evidence bodies are age-stubbed past the prune threshold (latest failures/evidence kept verbatim, digests retained), so compaction can reduce retained context on long runs instead of stopping with "compaction summary does not reduce estimated retained context".
- Readiness: an admitted brief is reused while it is unchanged (`readiness-reused` is recorded; no second judge call, no second user ask). Pause and explicit re-ready still re-gate. The generic GraphQL request deadline no longer bounds permission gates: readiness judging and human approval run under the caller's context, so a slow judge or a human taking minutes cannot be killed by a 30-second transport budget. `tools.graphql.timeout_seconds` is retained for configuration compatibility and no longer enforced.
- Acceptance commands: omitting `acceptance_command` binds the task check command as the behavioral acceptance (one command, one source) instead of forcing a second restated copy that can silently diverge. When the frozen acceptance fails in the same episode where the configured suite passes, the outcome now names the disagreement and the acceptance command itself as suspect.
- PAT/progress supervision: knowledge bodies are stubbed (key + digest) before entries are dropped under capacity, so supervision no longer requests knowledge that already exists. Advisory packets learn the smallest size the judge actually refused and are reduced before their first admission attempt; latest-settled output bodies are stubbed last (digest retained) instead of failing the judgment outright.
- Review integrity: a frozen check that writes artifacts (screenshots, coverage) no longer makes its own review impossible. The reviewer snapshots project contents before its check episode, restores them exactly afterward, and discloses the restored paths; only an inability to observe or restore is a role violation.
- Lifecycle and files: draft plans and approaches may retire directly (cleanup of never-executed graphs); the write tool creates missing parent directories inside the workspace root.

## Benchmark evidence

- Candidate37 (2.3 hours): Task 1's product passed its tests but settled failed because the frozen acceptance `node --test tests/` cannot start on Node 24/Windows; the repair then died on the GraphQL readiness deadline (0/3 GraphQL readiness attempts run-wide). Prune stubbing and readiness reuse both fired live. Benchmark user rejected delivery (nothing playable).
- Candidate38 (6.7 hours, same fixes plus this release's): Tasks 1–2 qualified on attempt 1; three compactions succeeded mid-task; one user approval and one user ask total; progress-input refusals fell from 152 to 1. Task 3 (rendering) passed host verification (browser suite 49/49) but its independent review was aborted when the reviewer's own check wrote screenshots — the defect fixed above, after the run. The run stopped there; later tasks were never attempted. Benchmark user rejected delivery (promising unfinished work, not a complete playable game). Parent-side reproduction at closure: `npm run check` PASS (157 tests), browser verification 49/49.
- The reviewer-artifact fix is covered by maintained tests (`TestReviewerCheckArtifactsRestoredNotViolation`, `TestProjectSnapshotRestoreUndoesCheckWrites`) but has not yet been exercised by a full benchmark run.

## Compatibility and limits

- From-scratch benchmark workspaces are not isolated from sibling benchmark directories: candidate38's model read the prior attempt's notes and mirrored its design. Treat cross-run design reuse as possible whenever prior workspaces remain on disk.
- A cancelled task with blank brief fields still gates readiness validation.
- Advisory (PAT) judgments still frequently end in failed-confidence picks; they remain advisory only and cost judge calls.
- Platform builds, installer preservation, native checks and npm package integrity have separate release evidence; skipped or unavailable checks are not passes.
