# Caliper code and architecture standard

## scope
Inspect project constraints and existing owners. Reuse or simplify before adding an abstraction, dependency or service.
[Choose architecture and trace ownership](knowledge:caliper/guides/code/boundaries#ownership)

## boundaries
Keywords: responsibility, interface, coupling, dependency, data flow.
Keep each responsibility and source of truth identifiable. Trace input through the owning function/service to its result. Respect existing interfaces and dependency direction. Update a relevant project/architecture/<name> entry for meaningful decisions, with code paths and symbols; link existing feature entries. Read deeper documents only when the change reaches that boundary. Documentation is a map to source, not a duplicate implementation.

[Trace a worked feature through its owner and consumer](knowledge:caliper/guides/code/architecture-example#data-flow)
[Build a section-to-topic-to-example project graph](knowledge:caliper/guides/code/knowledge#scoped-deep-dives)

## state
Keywords: invariants, transitions, accounting, idempotency, persistence, concurrency.
Make valid states and transitions explicit at their owning boundary, including alternate callers. Validate and reconstruct saved state before committing it; a failed load must not partially mutate the live state. Preserve meaningful quantities, relationships and clock/deadline semantics across reload. Test a non-default valid state through save/load, including purchased or configured values, not only a fresh object's surviving fields. Define restart/recovery policy explicitly; repeated actions must not duplicate rewards or silently reset costs. Keep shared state and buffered-event cursors under identifiable owners; truncation must not strand consumers. Use existing transactions/locks where concurrency can race. Test effects through normal entry points, not only internal helper calls.

[Design transitions, durability and repeated effects](knowledge:caliper/guides/code/state#transitions)

## failures
Keywords: errors, retries, timeout, backoff, backpressure, cancellation, cleanup.
Propagate failures with useful context; do not return success after skipping required work. Bound retries and work queues; retry only recoverable failures without duplicating effects. Give subprocesses/resources an owner and a cleanup path. Check the actual platform, executable, directory and process lifetime. A background service needs an explicit lifetime outside a short-lived tool call.

[Handle errors, retries and cancellation at the owner](knowledge:caliper/guides/code/failures#errors)

## trust
Keywords: validation, security, secrets, authorization, injection, paths.
Validate inputs at trust boundaries, authorize access to the actual resource, parameterize queries and escape for the destination context. Keep secrets out of client bundles and logs. Resolve paths within the allowed project. Apply network, authentication and storage safeguards relevant to this product; do not add unused security infrastructure.

[Validate at the actual authority and resource boundary](knowledge:caliper/guides/code/trust#authority)

## evidence
Keywords: tests, verification, performance, migrations, observability.
Check observable behavior through the actual public entry point and existing callers. For test design, distinguishing negative controls and required-stage completeness, retrieve the relevant oracle/execution sections of caliper/standards/tests; keep project-specific decisions in project/testing/strategy. Source-text matching is not a parser or runtime proof. Report excluded scripts and optional stages separately from a green aggregate. Label derived example outputs as expected and unexecuted unless that exact example was run; nearby tests do not prove it was observed. Distinguish development tooling from product dependencies. Name the measured scope before optimizing: synchronous call time is not whole-frame cost. A changed sample does not prove its cause; isolate the suspected factor or label the explanation a hypothesis. Avoid repeated unchanged rendering work. Test old saved data and reopen behavior when formats change. Keep diagnostics bounded. Record evidence and limits in the affected feature entry; source freshness alone proves no behavior.

[Measure useful work and diagnose cost](knowledge:caliper/guides/code/performance#measurement)
