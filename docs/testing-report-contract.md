# Declared test reports

A command exit and test completeness are different facts. With no report format, `check` preserves process success and reports coverage unknown. It does not scan arbitrary PASS or SKIP text.

For a project checker that includes a required browser stage:

```yaml
coding:
  check_command: node scripts/check.mjs
  report_format: caliper-test-report-v1
  expected_tests: [unit, browser]
```

The admitted check receives a fresh `CALIPER_TEST_RUN_ID` in its child environment. The checker must derive counts from actual runner results and emit exactly one JSON object in the captured output:

```json
{"format":"caliper-test-report-v1","run_id":"<process.env.CALIPER_TEST_RUN_ID>","stages":[{"id":"unit","status":"passed","discovered":12,"executed":12,"failed":0,"skipped":0},{"id":"browser","status":"skipped","discovered":1,"executed":0,"failed":0,"skipped":1}]}
```

This example remains unverified even if the wrapper exits zero: required browser execution was skipped. Required IDs are frozen with the check/attempt. Every required ID must be present and passed with nonzero executed count; duplicate IDs, contradictory/overflowing counts, malformed/truncated/multiple reports and a missing or old run ID are refused. Additional skipped stages outside `expected_tests` remain visible without failing the declared required scope. Any reported failure remains unqualified. A failing or cancelled process cannot qualify even after emitting a complete passing report.

Keep captured output limited to the report; the existing Bash tool combines stdout and stderr. Store verbose diagnostics separately when necessary. The host reads captured output only, never an old report file. Copying a previous report fails the execution-ID check. Rewriting a stale report with the new ID is dishonest reporting, not evidence of execution: this mechanism cannot prove the semantic quality or honesty of authored tests.

The contract above applies to the configured suite, not the separate behavioral acceptance command. Required acceptance stages must be included in the explicitly declared suite report; an opaque acceptance exit is not inferred stage coverage.

## Stage prerequisites and expected observations

Use `caliper-test-report-v2` when a stage needs declared prerequisites or specific
observations. It retains the v1 envelope, execution ID and count rules:

```yaml
coding:
  check_command: node scripts/check.mjs
  report_format: caliper-test-report-v2
  expected_tests: [unit, browser]
  stages:
    - id: browser
      prerequisites:
        - kind: path
          value: node_modules/playwright
      evidence:
        - id: visible-filter
          expected: Active filter leaves item A visible and hides completed item B.
```

Each stage requirement names an ID in `expected_tests`. The checker runs the
stages and emits their actual observations; the harness does not sequence them.
For example, after both stages execute:

```json
{"format":"caliper-test-report-v2","run_id":"<process.env.CALIPER_TEST_RUN_ID>","stages":[{"id":"unit","status":"passed","discovered":12,"executed":12,"failed":0,"skipped":0},{"id":"browser","status":"passed","discovered":1,"executed":1,"failed":0,"skipped":0,"evidence":[{"id":"visible-filter","observed":"Clicked Active; A remained visible and completed B was absent.","reference":"browser case filter-visible"}]}]}
```

Every declared evidence ID needs exactly one nonempty `observed` value in its
own stage. Extra observations are allowed; duplicate IDs, missing observations
and wrong stage/run IDs cannot qualify. Each stage allows up to 256 evidence
entries; IDs are at most 1,024 bytes, expected/observed text and references at
most 8,192 bytes each. `reference` is optional data; the host does not open it.
The existing output cap still applies to the complete captured report.

Before the verification episode, the host records each stage's prerequisites
against its workspace and fresh run ID. `path` checks the exact path (relative
to the workspace unless absolute); `executable` checks command availability.
Neither proves runtime health. `manual` remains unknown, even if its text says a
user confirmed it. Each stage supports up to 16 unique prerequisites. These
host observations are separate from the checker's JSON and persist in the same
check receipt. Missing/unknown prerequisites prevent qualification, but the
configured check still runs after a behavioral failure or missing prerequisite.
Cancellation or persistence failure keeps the existing stop behavior.

The complete contract is frozen at run/attempt admission. Changing a requirement
cannot weaken the same admitted attempt; explicitly readmit it. The host verifies
structure and current ownership, while the existing reviewer assesses whether
observations satisfy the authored expectations. It does not treat an observation
as true merely because its ID matches, or compare prose for exact equality.

Existing `go-test-json` remains supported for a direct `go test -json -count=1 ...` command, with `expected_tests` such as `module/package/TestBehavior`. It requires current command-interval timestamps, complete run/completion pairs, nonzero executed tests and no skipped selected tests/packages. Shell wrappers and shell control operators are outside that narrow adapter. Go's native format does not echo the host nonce; its receipt carries the run ID and relies on the direct uncached command plus event interval. Neither format establishes test quality automatically.

Opaque commands, `go-test-json` and v1 keep their existing behavior. Adding
`stages` requirements to these formats is rejected rather than silently dropping
them; rich requirements require v2. Historical receipts are not upgraded.

Optional `/tests` suites use the same `report_format`, `expected_tests` and
`stages` fields and reader, but their separate archives never qualify Chat or PAT.
Their existing suite-level `prerequisites` still block execution when missing or
manual; stage prerequisites describe qualification and do not suppress the
configured check. Enabling, disabling or editing an optional suite cannot change
the required `coding` contract. Historical receipts retain their exact contract
and execution identity.
