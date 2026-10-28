# Building an oracle that can disagree with the code

## Independence
An oracle is the reason an expected result is correct. Use a requirement, independently derived example, trusted reference, invariant or observed external contract. Calling the production helper to generate both expected and actual values tests self-consistency, not correctness.

For calculations, derive small cases by hand and include boundary cases. For serialization, assert a non-default round trip through separate writer and reader paths, while also checking the external format where it is part of the contract. For geometry, select points whose inside/outside status is obvious from the specification, including direction and boundary convention. Do not copy the implementation's inequality into the test and call it an independent oracle.

[Inspect concrete weak and strong oracle examples](knowledge:caliper/guides/tests/oracle-examples#geometry)
[Derive clearance from the complete swept envelope](knowledge:caliper/guides/tests/clearance#swept-envelope)
[Use state that cannot pass by returning defaults](knowledge:caliper/guides/tests/fixtures#discriminating-data)

## Invariants and transformations

[Work through an accounting invariant and retry](knowledge:caliper/guides/tests/accounting#same-operation-retry)
When exact outputs are numerous, assert properties that constrain correctness. A transfer should preserve the total while changing the correct accounts. Sorting should preserve the multiset and satisfy the intended ordering. A reversible encoding should restore the original data, but round-trip agreement alone can conceal two matching bugs; include a known external sample.

Metamorphic tests compare related inputs: translating all geometric points by the same offset should preserve distances; adding an irrelevant record should not change another workspace's result. State the assumptions under which the relation holds. A plausible-sounding relation that the product does not promise creates false failures.

## Negative controls
Show that the test rejects a plausible defect. Temporarily supply a deliberately wrong implementation in an isolated fixture or use an existing injection seam: omit persistence, return the default, duplicate an effect, ignore cancellation, or mark a missing checker as successful. Keep the control bounded and restore exact state. Do not mutate the benchmark product to manufacture evidence.

If the wrong implementation still passes, improve the fixture, boundary or assertion. Merely adding more assertions around the same faulty expected value does not strengthen the oracle. A failing control proves sensitivity to that defect, not universal adequacy.

[See a constant-output false positive](knowledge:caliper/guides/tests/oracle-examples#constant-output)
[Use lifecycle controls for ownership failures](knowledge:caliper/guides/tests/lifecycle-example#negative-controls)

## Interpreting failures
A mismatch can come from product code, the test's premise, fixture setup or environment. Inspect the exact action and state before changing an assertion. Preserve the original failing evidence. If the expectation was wrong, derive and document the corrected expectation from the contract; do not simply replace it with the current output.
