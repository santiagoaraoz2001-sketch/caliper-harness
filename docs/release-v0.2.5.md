# v0.2.5

This release fixes the three traced v0.2.4 limits (retained-context compaction, readiness re-asking, PAT knowledge visibility) and further traced defects in acceptance handling, advisory capacity, review integrity, and file lifecycle.

## Behavior changes

- Retained context: consumed failure/evidence bodies are age-stubbed past the prune threshold (latest failures/evidence kept verbatim, digests retained), so compaction can reduce retained context on long runs instead of stopping with "compaction summary does not reduce estimated retained context".
- Readiness: an admitted brief is reused while it is unchanged (`readiness-reused` is recorded; no second judge call, no second user ask). Pause and explicit re-ready still re-gate. The generic GraphQL request deadline no longer bounds permission gates: readiness judging and human approval run under the caller's context, so a slow judge or a human taking minutes cannot be killed by a 30-second transport budget. `tools.graphql.timeout_seconds` is retained for configuration compatibility and no longer enforced.
- Acceptance commands: omitting `acceptance_command` binds the task check command as the behavioral acceptance (one command, one source) instead of forcing a second restated copy that can silently diverge. When the frozen acceptance fails in the same episode where the configured suite passes, the outcome now names the disagreement and the acceptance command itself as suspect.
- PAT/progress supervision: knowledge bodies are stubbed (key + digest) before entries are dropped under capacity, so supervision no longer requests knowledge that already exists. Advisory packets learn the smallest size the judge actually refused and are reduced before their first admission attempt; latest-settled output bodies are stubbed last (digest retained) instead of failing the judgment outright.
- Review integrity: a frozen check that writes artifacts (screenshots, coverage) no longer makes its own review impossible. The reviewer snapshots project contents before its check episode, restores them exactly afterward, and discloses the restored paths; only an inability to observe or restore is a role violation.
- Lifecycle and files: draft plans and approaches may retire directly (cleanup of never-executed graphs); the write tool creates missing parent directories inside the workspace root.

## Compatibility and limits

- A workspace is not isolated from sibling directories on disk. Treat reuse of material from neighboring directories as possible whenever prior work remains on disk.
- A cancelled task with blank brief fields still gates readiness validation.
- Advisory (PAT) judgments still frequently end in failed-confidence picks; they remain advisory only and cost judge calls.
- Platform builds, installer preservation, native checks and npm package integrity have separate release evidence; skipped or unavailable checks are not passes.
