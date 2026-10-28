# Worked lifecycle test: editable settings with durable ownership

## Contract
A user edits two settings, previews the draft, then either cancels or commits. Committed settings survive reopening. An outdated editor cannot overwrite a newer commit. Another workspace is independent. This is a small stateful feature with several distinct obligations; one happy-path "save succeeded" test cannot cover them.

The oracle is the requested transition contract and known input values. The test uses actual storage and public read/write paths. It does not inspect only an internal map.

[Identify the right boundary and evidence](knowledge:caliper/guides/tests/contract#contract)

## Sequence
Create two isolated workspaces with different non-default settings. Open workspace A at version v1. Change the draft and cancel; read again and verify v1 remains. Reopen the editor, change both fields and commit; capture v2 and close all owners.

Reopen storage and the public consumer. Verify both v2 values affect the consumer, not merely the stored row. Verify workspace B remains unchanged. Submit an update based on v1 and assert a conflict with no partial change. Read again to establish the winning state.

For cancellation during save, hold the operation at a documented pre-commit boundary. Request cancellation, release the boundary and wait for settlement. Assert the contract's chosen outcome. If cancellation arrives after commit, report the committed result rather than claiming rollback that did not occur.

## Negative controls
Run bounded variants that omit the write, ignore one field, reuse workspace A's scope for B, accept the stale version, or publish success before commit. Each should fail the corresponding assertion. Use a controlled adapter or fixture, not a mutation of the user's real project.

For concurrent save, let two owners read v2, synchronize their writes and assert exactly one succeeds if the contract requires compare-and-swap. Verify the rejected write leaves no partial fields or orphaned references. A timeout alone does not prove rejection.

[Use controlled synchronization for races](knowledge:caliper/guides/tests/state#concurrency)

## Evidence and cleanup
Record input versions, owner identities, commit/conflict results, reopened values and consumer observations. Keep submission, persistence and application separate. Restore or remove only fixture-owned state and services. If cleanup fails, retain the owned identity and report it explicitly.

This example generalizes to configuration, graph decisions, workflow state and session recovery. Adapt the states and commit rules to the actual feature; do not copy a cancellation policy the product never promised.
