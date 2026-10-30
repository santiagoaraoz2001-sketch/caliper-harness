# Designing a test around a real obligation

## Contract
State the observable promise and its owner before choosing a test framework. Name the actor, starting state, action, expected result and evidence source. "The cache works" is too broad. "After a successful update, the next read through the public service returns the new value, including after reopening storage" identifies a behavior and a boundary.

Separate product requirements from implementation choices. A test that asserts the current helper's internal sequence may preserve an accidental design while allowing the actual user behavior to break. Test the narrowest stable boundary that can prove the promise. Add a wider integration test when composition introduces risk that the narrow test cannot detect.

Record what would count as failure and which plausible wrong implementations the test must reject. For a save operation, examples include saving the old value, reporting success before persistence, writing to another workspace, or returning a cached value after reopening. Choose representative negatives according to risk rather than generating every imaginable combination.

[Choose an independent source of expected truth](knowledge:caliper/guides/tests/oracles#independence)
[Choose state and fixtures that expose the defect](knowledge:caliper/guides/tests/fixtures#discriminating-data)
[Prove a representative failure without contaminating controls](knowledge:caliper/guides/tests/oracles#negative-controls)

## Scope and proportionality
Use a pure test for deterministic calculation, an integration test for storage or service boundaries, and an interaction test for a user-visible path. A browser test is unnecessary for a pure formatter; a private handler test cannot prove a button is wired to it.

Distinguish required acceptance from optional exploration. Critical behavior should have explicit gates; low-risk formatting changes may need inspection rather than a new permanent test. Tests impose maintenance and execution costs. Keep a test when it catches a meaningful class of failure that other evidence does not cover.

Reuse the user's requirement or accepted task contract. Ask only about a material unresolved expectation. Use the existing framework; a small task does not need a PAT workflow just to test it.

Define the environment and unavailable dependencies before running. A required stage that cannot execute is unverified, not passed. A successful process exit cannot override an absent assertion or a skipped required stage.

[Make required-stage execution explicit](knowledge:caliper/guides/tests/execution#required-stages)

## Test design record
For a nontrivial feature, keep a concise record containing the requirement, boundary, oracle, fixtures, checks, required/optional status, and run command. Link the implementation and latest relevant evidence rather than copying logs into the strategy. Explain how to reproduce a failure. Update the record when the requirement changes, not for every temporary debugging experiment.

Save reusable project policy under project/testing/strategy: relevant test levels, normal commands, fixture policy, prerequisites and exclusions. Give an independently reusable specification its own project/testing/ key and link the responsible code. These notes describe intent; they grant no execution authority and are not passing receipts.

The record should help the next contributor choose and interpret the test. If it merely restates "write unit and integration tests," it adds no value.

[Follow a worked persistence and cancellation example](knowledge:caliper/guides/tests/lifecycle-example#contract)
