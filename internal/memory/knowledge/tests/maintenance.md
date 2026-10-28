# Keeping a test suite useful

## Selection
Group tests by behavior and execution cost. Keep a fast deterministic set for common changes, integration checks for boundaries, and platform/provider checks for capabilities that fakes cannot establish. Selection should explain what it covers; a short name alone is not evidence.

Optional suites may be disabled deliberately, with their status visible. Disabling a required acceptance suite must invalidate the associated qualification rather than silently green the run. Preserve the suite definition and previous evidence when changing selection. A test-management interface should expose intent, boundary, oracle, command and latest scope before asking a user to turn it on or off.

[Design the test record before the toggle](knowledge:caliper/guides/tests/contract#test-design-record)

## Repair
When a test fails, preserve its original output and inspect the contract, fixture and product path. Change the expectation only when the requirement or independent derivation supports it. Do not copy the current output into the expected value merely to make the suite green.

When implementation changes internally but behavior does not, remove brittle assertions on incidental call order or private structures. Replace them with a stable boundary assertion and demonstrate that a plausible defect still fails. If two tests cover the same contract, consolidate their fixtures or cases without deleting unique negative coverage.

[Use negative controls to protect test value](knowledge:caliper/guides/tests/oracles#negative-controls)

## Flakiness
Reproduce with the recorded seed, environment and timing evidence. Look for shared mutable state, uncontrolled clocks, process ownership leaks, nondeterministic ordering and external dependencies. Replace arbitrary sleeps with observable synchronization where possible. Increasing retries can hide failures while increasing cost; use retries only when the contract explicitly includes transient behavior and report the attempts.

A quarantined test remains a gap with an owner and reason. Do not count it as passed. Reintroduce it after a deterministic regression demonstrates the repaired failure mode.

## Retention
Keep evidence that supports a current decision, important historical failure or reproduction. Avoid storing enormous generated logs as source. Preserve identities and original artifacts when pruning copies; retention tests must protect active and explicitly selected sessions.

Periodically ask which tests reject a real defect, which are redundant, and which requirements remain untested. Test count, line count and coverage percentage are diagnostic measures, not quality verdicts.
