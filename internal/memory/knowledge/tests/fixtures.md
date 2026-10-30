# Fixtures that expose defects without creating noise

## Discriminating data
Choose the smallest input that distinguishes correct behavior from likely wrong behavior. Use non-default valid values for persistence, different values across isolated owners, and asymmetric cases for ordering or coordinate transformations. A round trip of zero, empty strings and default settings can pass when nothing is saved.

For selection tests, provide at least two distinguishable candidates and verify the intended one. For optional fields, distinguish absent, explicit empty and nonempty where the contract does. For Unicode, include characters that exercise the actual transport boundary rather than only ASCII with a decorative label.

For randomized data, preserve the seed and failing input. Check that the generator actually reaches the targeted condition; thousands of collision tests with no approaching objects do not test collision handling. Track the relevant condition's occurrence or add deterministic cases that guarantee it.

[Derive a geometry fixture independently](knowledge:caliper/guides/tests/oracle-examples#geometry)

## Isolation and ownership
Use a temporary project, store and configuration when tests mutate settings or state. Keep original files and service identities outside the fixture. Record the exact baseline and restore it, including absent files and optional fields. Restoring equivalent-looking values is insufficient when byte-level compatibility or startup parsing is the subject.

Start only services owned by the fixture. Capture their process identity, endpoint and descendants; shut down that owned tree without touching unrelated servers. Use an allocated port or explicit collision handling. A broad process-name kill turns a test into a destructive environment change.

Keep fixtures local and deterministic where the real remote service is not the subject. A local fake may prove protocol handling, but it does not prove live-provider compatibility. Record those as separate gates.

[Test ownership across lifecycle transitions](knowledge:caliper/guides/tests/state#ownership)
[Reset each negative control to a passing baseline](knowledge:caliper/guides/tests/oracles#isolated-control-example)

## Realism without excess
Represent the boundary that matters. A storage test should reopen actual storage; a UI wiring test should enter through the visible control; a request-format test should inspect the actual serialized request. Large production datasets add little when a three-record fixture exposes the same defect.

Avoid broad shared fixtures whose unrelated defaults silently satisfy the test. Make important preconditions explicit near the assertion. Clean up in a failure-safe path and preserve useful diagnostic artifacts when a failure occurs. Raw traces should stay in the designated evidence area rather than bloating the repository.

[Decide which evidence must survive the run](knowledge:caliper/guides/tests/execution#evidence)
