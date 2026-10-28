# Caliper test standard

## intent
Define observable behavior and a plausible wrong result. Reuse the framework and strategy; save reusable decisions/specs under project/testing/. Notes are not passing evidence.
[Choose the test boundary](knowledge:caliper/guides/tests/contract#contract)

## oracle
Keywords: expectation, assertion, independent, counterexample, negative control.
Derive expected results from the requirement, a trusted reference or an independently justified example. Do not copy the implementation's formula into its test or change the expected value merely to make a failure disappear. For a reported defect, identify the unmet behavior before deciding whether code or fixture is wrong. Assert the meaningful outcome, not an incidental click or one surviving field. A custom checker should reject a relevant known-wrong isolated fixture and accept the intended valid fixture. Broad mutation testing is optional, not a new required dependency. Label unexecuted examples and hypotheses honestly; automated semantic adequacy scores cannot replace judgment.

[Derive an oracle and prove a wrong implementation fails](knowledge:caliper/guides/tests/oracles#independence)

## fixture
Keywords: setup, isolation, persistence, boundary, public entry point, browser.
Use deterministic inputs and explicitly owned resources. Test through the real supported entry point, including prerequisites, rather than bypassing hidden controls or assigning internal state and calling it a user journey. Helpers and debug-state fixtures can prove narrower properties; label that scope. For persistence use non-default valid data, save, reopen, then assert the whole important state. Include repeated action, cancellation, failure or boundary cases when relevant. Preserve original artifacts; isolate known-bad controls. Use bounded deadlines and cleanup even when assertions fail. Do not install an undeclared runtime just to conceal a missing prerequisite.

[Choose discriminating data and isolated owners](knowledge:caliper/guides/tests/fixtures#discriminating-data)

## execution
Keywords: run, required, optional, enabled, prerequisites, missing, skipped.
Required, selected, runnable and passed are different facts. A required stage must fail when its prerequisite or assertion is absent. A disabled optional suite narrows scope and retains its prior failure/history; it cannot satisfy a frozen required check. Turning testing guidance off changes advice, not verification obligations. Run the actual declared command in its captured workspace with the selected spec and configuration. Missing interpreters, browser binaries or reports are explicit unavailable evidence. A selected run that finds zero tests cannot prove declared coverage. Preserve exit status, cancellation and owned output; do not infer success by scraping arbitrary PASS/SKIP text.

[Make required stages and exact-candidate evidence explicit](knowledge:caliper/guides/tests/execution#required-stages)

## evidence
Keywords: report, result, provenance, freshness, performance, visual inspection.
Only current execution receipts establish what ran. A structured report must belong to this run and match its declared format; stale, absent or malformed reports cannot establish counts or coverage. An opaque command with exit zero proves process success, with coverage unknown. Passing tests establish only their asserted behavior; screenshots, real user journeys and measured performance are separate evidence. Compare matching scopes and report prerequisites, exclusions and unverified claims. Retain current spec/version, command, selected suite and source provenance; source unchanged alone is not proof. Resume/retry must preserve owners and frozen obligations without borrowing another run's receipt.

[Record actual receipts and useful cost](knowledge:caliper/guides/tests/execution#evidence)

## maintenance
Keywords: refinement, version, enable, disable, stale, review, efficiency.
Refine tests when requirements or justified expectations change, retaining why and the earlier evidence. A changed spec, command, selection or relevant source invalidates incompatible results. Preserve still-valid strategy facts when writing a complete new version; put feature-only details under a linked key. Do not duplicate test source or raw logs into knowledge. Reuse current receipts and rerun affected checks, not unchanged suites for activity's sake. Use the existing independent review for oracle changes; add no reviewer tier. Retrieve only relevant sections and reuse them in context. Explicit user invocation may request guidance even when automatic suggestions are disabled.

[Select and refine suites without weakening requirements](knowledge:caliper/guides/tests/maintenance#selection)
