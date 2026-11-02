# v0.2.4

This release removes decision request size ceilings, makes readiness approvals snapshot-bound, and strengthens review evidence.

## Behavior changes

- Caliper-owned byte ceilings on decision requests, chat and thinking traffic are removed. One token admission remains. A provider endpoint with its own request limit is handled by chunked judgments over the complete brief, and size failures now name the provider limit instead of reporting the server unreachable.
- Readiness approvals are bound to the exact judged snapshot and brief. The former short execute deadline that could overlap human approval and mint repeated approval dialogs is gone; an admission by the user is recorded with the judge confidence that produced it.
- Full judgment audit retention is the default. Judgment inputs, protocols and outcomes remain available for later inspection instead of being summarized away.
- Reviewer instructions carry a test-provenance contract: a passing suite is evidence only after the reviewer confirms the assertions exercise the paths the game or program actually runs, and compares test changes against frozen acceptance.
- Model-visible prompts follow one clarity style: plain direct words, short sentences, one name for one thing, no metaphors, and obsolete facts removed during edits. The prompt set was audited for contradictions under the same rule.
- Local development and audit documents are retained on disk but excluded from version control; only user-facing guides and release notes under `docs/` are public.

## Compatibility and limits

Known limits carried into v0.2.5:

- A very long session can stop with "compaction summary does not reduce estimated retained context": repeated summaries did not reduce the measured prompt because the retained set is dominated by content the summary step does not retire. The stop is safe (context intact, work preserved) but terminal for that run.
- Readiness re-evaluates collective coherence at each claim. An unchanged brief near the admission threshold can therefore ask the user more than once across a long run; identical snapshots are not yet reused.
- After compaction, PAT supervision may keep requesting knowledge updates that already exist unchanged, because its input does not yet show the current knowledge state.

Model uptake, aesthetic quality and arbitrary generated-code correctness remain measured outcomes, not guarantees inferred from these changes. Platform builds, installer preservation, native checks and npm package integrity have separate release evidence; skipped or unavailable checks are not passes.
