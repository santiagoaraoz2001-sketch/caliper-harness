# v0.2.4

This release removes decision request size ceilings, makes readiness approvals snapshot-bound, and strengthens review evidence. It was exercised by a from-scratch end-to-end benchmark build (Space Transporter): four of five planned tasks qualified, and the run stopped in the final task on context retention, stated plainly under Compatibility and limits. That stop does not change the shipped behavior below; its fixes are the v0.2.5 focus.

## Behavior changes

- Caliper-owned byte ceilings on decision requests, chat and thinking traffic are removed. One token admission remains. A provider endpoint with its own request limit is handled by chunked judgments over the complete brief, and size failures now name the provider limit instead of reporting the server unreachable.
- Readiness approvals are bound to the exact judged snapshot and brief. The former short execute deadline that could overlap human approval and mint repeated approval dialogs is gone; an admission by the user is recorded with the judge confidence that produced it.
- Full judgment audit retention is the default. Judgment inputs, protocols and outcomes remain available for later inspection instead of being summarized away.
- Reviewer instructions carry a test-provenance contract: a passing suite is evidence only after the reviewer confirms the assertions exercise the paths the game or program actually runs, and compares test changes against frozen acceptance.
- Model-visible prompts follow one clarity style: plain direct words, short sentences, one name for one thing, no metaphors, and obsolete facts removed during edits. The prompt set was audited for contradictions under the same rule.
- Local development and audit documents are retained on disk but excluded from version control; only user-facing guides and release notes under `docs/` are public.

## Benchmark evidence

- The fresh Space Transporter run (15.2 hours, local model) qualified the shell, contracts/content, piloting/physics, and progression tasks. Progression first failed review with a green suite: two perks had no consumer on the running-game path. Remediation threaded the flown stats into payout and rescue, consumed the sensor range stat in the minimap, and added regressions that drive the real completion and rescue functions; fault injections confirm the new tests fail when those paths break.
- No size refusal occurred anywhere in the run at ~235k measured input tokens. Snapshot-bound approvals never looped; each human admission is recorded with its judge confidence.

## Compatibility and limits

Known limits carried into v0.2.5, each traced in the benchmark archive:

- A very long session can stop with "compaction summary does not reduce estimated retained context": repeated summaries did not reduce the measured prompt because the retained set is dominated by content the summary step does not retire. The stop is safe (context intact, work preserved) but terminal for that run.
- Readiness re-evaluates collective coherence at each claim. An unchanged brief near the admission threshold can therefore ask the user more than once across a long run; identical snapshots are not yet reused.
- After compaction, PAT supervision may keep requesting knowledge updates that already exist unchanged, because its input does not yet show the current knowledge state.

Model uptake, aesthetic quality and arbitrary generated-code correctness remain measured outcomes, not guarantees inferred from these changes. Platform builds, installer preservation, native checks and npm package integrity have separate release evidence; skipped or unavailable checks are not passes.
