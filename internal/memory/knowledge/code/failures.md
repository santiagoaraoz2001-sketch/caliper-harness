# Failure handling without accidental success

## Errors
Return enough structured context to identify the failed operation, owner and recoverable next action. Avoid dumping an entire manual for one invalid field. Aggregate independent validation errors when the user can fix them together, but never execute a rejected partial call.

Distinguish invalid input, denied authority, unavailable prerequisite, transient transport failure and product failure. They require different responses. A wrapper that skips required work and returns success hides the real state. Preserve the original error and meaningful evidence when adding context.

[Report required-stage absence honestly](knowledge:caliper/guides/tests/execution#required-stages)

## Retry
Retry only when the operation and failure make retry meaningful. Preserve operation identity and committed effects. Use bounded policy at the actual owner; do not add several independent retry loops that multiply work. An explicit user retry may start a new attempt, but it must not silently borrow the old attempt's verification.

For generation continuation, distinguish an output boundary from a completed response, cancellation or transport loss. Preserve the exact retained content and protocol identity; do not turn a mechanical continuation into a new user instruction. An incomplete tool payload must not execute. A complete effect must not repeat because later output continues.

## Cancellation
Cancellation is a request until the owner acknowledges a settled state. Propagate it to owned resources, wait for bounded cleanup and report any remaining owner. Preserve completed effects and recoverable input. Keep external services alive unless the task explicitly owns their lifetime.

A background service needs an explicit lifetime beyond a short-lived shell call. Record its process identity and endpoint; cleanup only that owned tree. Verify platform behavior rather than assuming one shell's process semantics apply everywhere.

[Create a deterministic cancellation test](knowledge:caliper/guides/tests/state#concurrency)

## Recovery
Prefer restoring a coherent known state to layering compensating guesses. On restart, load the committed record, validate its ownership and reconstruct pending work from durable facts. Keep historical failures visible. A successful later attempt does not erase an earlier defect or prove every recovery path.
