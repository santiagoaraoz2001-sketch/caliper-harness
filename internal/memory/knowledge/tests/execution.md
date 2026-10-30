# Running and interpreting tests honestly

## Required stages
Declare which stages are required for the current claim. Each stage needs an executed/failed/skipped/unavailable outcome, not only an aggregate exit code. A missing module, absent test discovery, missing browser or unavailable fixture cannot become a pass because the wrapper exits zero.

For required stages, fail the aggregate qualification when the stage did not execute. Optional checks may be skipped with a reason, but the report must preserve that scope. Test this behavior with an exit-zero wrapper that emits a required-stage SKIP. A missing-module test that exits nonzero covers a different failure.

[See why a green wrapper can be false evidence](knowledge:caliper/guides/tests/oracle-examples#required-stage)

## Exact candidate
Tie evidence to the tested source and executable. Freeze or hash the relevant inputs, record the command and environment, and verify they did not change during the gate. A historical pass on another build is context, not a current pass. If source changes after a gate, rerun the affected checks and any required integration gate.

Run independent checks in parallel only when they do not share mutable fixtures, owners or services. Keep stateful lifecycle phases serial. Do not launch duplicate suites because a progress file is stale; verify the actual process and receipt.

## Evidence
Keep the test's intent, actual executed command, stage outcomes, meaningful assertions, exit status and relevant artifact paths. Preserve failed evidence before repairs. Record uncertainty directly: coverage unknown, visual inspection unavailable, or provider behavior untested.

Before cleaning up temporary probes, preserve their exact source, baseline, commands and decisive results in reviewable evidence outside the product files. Give the reviewer the recorded references or accessible paths. A statement that a deleted script passed cannot replace its assertions and receipts. Plan verification around the reviewer's available read and bound-check tools; do not require an unavailable command or discard evidence that the frozen acceptance needs.

Separate observed output from derived expectation. A manually calculated example is useful but remains expected and unexecuted until run. A passing check proves what its assertions cover; it does not verify adjacent features. Summarize routine success and retain detailed raw logs outside the repository.

[Maintain tests without weakening their contracts](knowledge:caliper/guides/tests/maintenance#repair)
[Distinguish a measured cause from a plausible explanation](knowledge:caliper/guides/tests/execution#causal-claims)

## Causal claims
Capture the failing command, its actual working directory, relevant input bytes, executable/version and error before explaining the cause. A homemade normalizer is not evidence of a platform API's behavior. For a path claim, call the actual API with the observed arguments in the relevant environment. For a runner-version claim, inspect or execute that runner; do not promote a remembered version assumption into an observation.

Change one suspected cause in an isolated fixture and retain the original negative. If several variables changed together, state the confounding factors and the limited inference. For example, a new prompt prefix and a different server cache slot can both accompany slow prefill; a cache-hit control with the same slot is needed before attributing all delay to one cause.

Documentation is an artifact, not a test receipt. Mark an example expected/unexecuted until the real command or public consumer ran. Distinguish test construction, execution, durable recorded result and actual product application. A browser boot or absence of logged errors does not prove a gameplay action or visible interaction occurred; exercise and observe the claimed interaction.

[Derive and challenge the expected result](knowledge:caliper/guides/tests/oracles#interpreting-failures)
[Check a public interface](knowledge:caliper/guides/tests/interface#public-path)

## Cost
Measure the work that matters: elapsed time, requests, input/output volume, cached volume where available, setup/cleanup and recovery effort. Repeating unchanged checks adds little confidence. Repeat when new source, failure or unresolved uncertainty changes the question.

If a reviewer revisits the same files, ask what new hypothesis or missing evidence justifies the read. Use existing receipts and current hashes to distinguish new information from repetition. Avoid solving wasted review work by adding another reviewing model or an arbitrary step cap.
