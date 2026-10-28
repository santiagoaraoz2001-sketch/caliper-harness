# Test design and management — v0.2.3 implementation contract

Requested 2026-10-25 and explicitly authorized for the next v0.2.3 corrective build. This document records the design contract and implemented first scope. Candidate3/4 historical qualification does not qualify these additions; current-source tests, the next exact binary and native acceptance remain separate gates. Follow development-principles.md; reuse existing ownership and GraphQL/Kuzu, with no new runtime judge, mandatory review tier, test framework or dependency.

## Problem and intended result

The current benchmark demonstrates that passing tests can coexist with broken startup, lost saved upgrades and expectations copied from the implementation. It also demonstrates the opposite problem: incorrect fixtures and stale expected values can fail correct behavior and trigger unnecessary production changes.

Help the user and model define what a test establishes, implement it at the right boundary, execute a deliberate selection and understand exactly what remains unverified. Give users useful controls without allowing a smaller selection to masquerade as stronger qualification.

## Build on existing design and verification owners

Design currently uses sectioned bundled standards, versioned project knowledge such as project/design/identity, references to code and bounded retrieval. Testing should follow the same pattern:

- A concise caliper/standards/tests document with selectable sections. Extract and link overlapping testing guidance from the existing code standard rather than injecting both in full.
- A versioned project/testing/strategy entry describing relevant test levels, normal commands, fixture policy, platform prerequisites and important exclusions. Create separate requirement/spec entries only when independent lookup or reuse is useful; do not inventory every assertion.
- Existing task acceptance, tests, verify, checkCommand and acceptanceCommand fields carry the current work. Small ordinary tasks do not have to adopt PAT just to test something.
- Existing check execution, task/attempt identity and immutable receipts own results. Extend those owners where needed; authored knowledge never becomes a passing receipt.

| Fact | Canonical owner |
| --- | --- |
| Behavioral intent, expected outcome and rationale | Versioned project knowledge; current accepted task contract references its version |
| Executable assertions and fixtures | Repository test files and the project's existing test framework |
| Optional suite/profile selection and guidance setting | Existing project configuration owner, with visible scope and persistence |
| Required checks for the current attempt | Frozen task/attempt contract |
| Execution, cancellation, output and outcome | Existing runner receipts/session archives |
| Searchable test coverage/status | Derived GraphQL projection of the above; not another editable results ledger |

The graph links requirements, spec versions, relevant files and evidence. It does not duplicate test bodies or logs, independently own editable pass flags, or replace normal repository test commands. File, spec and selection changes cannot silently reuse old evidence.

## Designing a useful test

The first interaction should be small: “What behavior should this prove, and what plausible wrong behavior should it catch?” Ask only when the requirement is genuinely unresolved; otherwise use the existing accepted requirement.

A compact spec records:

1. Observable requirement and where it came from: user request, existing contract, or explicitly proposed assumption.
2. Setup and actual entry point, including necessary state and prerequisites.
3. Expected outcome and the independent basis for that expectation.
4. A distinguishing failure case or counterexample, plus important boundaries where relevant.
5. Test location/selector, command and known limits. Resolve runtime details through the existing configuration/runner rather than making the model reconstruct them.

Expose advanced detail on demand. Examples:

- **Save upgrades:** purchase a nonzero upgrade through the normal state API, save, reload, and compare purchased levels and derived stats. A test retaining only credits does not establish progress persistence.
- **Start game:** open a clean browser context and click the visible start control, then assert the expected playable state. Importing modules or forcing a hidden control is insufficient.
- **Perk entitlement:** define independently how many claims rank 1 and rank 2 permit; check each successful claim and duplicate rejection. Reusing the implementation's rank-minus-count formula is not independent justification.
- **Movement:** compare actual motion against declared bounds with controlled timing and finite-value assertions; do not choose a tolerance only because it makes one run green.

Prefer meaningful effects over counts or incidental text. Distinguish unit, integration, normal user journey, visual inspection and performance evidence. Debug-state manipulation can be a useful integration fixture, but its result must not be presented as a normal player journey.

For a custom checker, demonstrate that it detects one relevant wrong result using an isolated fixture or a safe known-failing control. Broad mutation testing is optional, not a new default dependency or compulsory execution stage. Semantic test quality cannot be guaranteed by a deterministic scanner or by an LLM score.

## User workflow and controls

The entry point is a single `/tests` view, reusing normal TUI navigation and the existing model conversation. `n`/`e` add or edit optional suite JSON; Space changes enabled state; `r` executes the selected suite; `R` executes enabled optional suites serially; `x` cancels; Enter inspects bounded result history; PgUp/PgDn scroll details and older results; `f` refreshes; `d` prepares a visible design/refine Chat draft; `g` changes future drafts' guidance. Draft preparation never submits a model request automatically.

The view lists configured suites and the existing configured coding check, their purpose, required status, selection, prerequisites, latest result and freshness. Details show expected behavior, test location, evidence and exclusions. Commands run serially under one owner. Running all enabled optional suites does not implicitly run the required configured check; select it explicitly with `r`. A suite does not create or revise a PAT contract. Required task/attempt changes continue through their existing owners.

Keep these facts separate:

| Field | Meaning |
| --- | --- |
| Required | Needed to satisfy this accepted task/release scope |
| Enabled | Selected for this execution |
| Runnable | Necessary prerequisites are available |
| Result | Not run, passed, failed, blocked or cancelled; freshness is separately current/stale/unknown |

Disabling a failing optional test preserves its failure history and visibly narrows the scope. The UI refuses to disable the configured required check. Optional suites cannot be marked required through editable configuration. The coding model cannot disable an obligation to qualify its own task; a deliberate requirement change uses the existing authorized revision path. Routine test additions and corrections should not acquire a new approval ceremony.

A separate **testing guidance** switch controls whether relevant testing advice is offered. Turning guidance off does not weaken required verification or remove history. Changing configuration while a run exists affects a future selection; it cannot rewrite the frozen attempt. Re-enabling a test exposes its last result and freshness, never an invented pass.

Reuse existing GraphQL for spec/state access and existing check execution for commands. A run-selected control may extend the existing check owner, but selecting an optional subset must not satisfy the full frozen required check. Avoid a new family of model tools for every test action.

## Host mechanics and truthful reporting

Freeze accepted spec versions, required selection, commands, workspace and existing source/check identity into each attempt. Reuse existing contract comparison and stale-evidence machinery; add only the missing selection/spec provenance.

Structured adapters accept `go-test-json` from the current command receipt and
framework-neutral v1/v2 reports using the fresh `CALIPER_TEST_RUN_ID`. They retain
executed/failed/skipped counts and refuse incomplete, replayed or contradictory
coverage. V2 adds `stages` declarations for prerequisites and expected evidence;
current producer observations stay separate from host prerequisite presence.
Unknown prerequisites cannot qualify and do not suppress the required check.
All formats use the existing check owner. The required `coding` contract freezes
separately from optional Tests selection. See [Declared test reports](testing-report-contract.md)
for producer examples, compatibility and assurance limits. Fixture controls must
prove zero-match and missing-prerequisite behavior.

For an opaque command, say “process passed; coverage unknown.” Do not infer semantic coverage from exit zero, scrape arbitrary PASS/SKIP words or pretend that arbitrary assertion edits can be classified automatically. An aggregate result must expose failed, disabled-required, blocked and stale obligations rather than showing only a green passed count.

Changing an oracle or deleting coverage creates a visible spec/code diff and invalidates relevant prior evidence. The existing independent review can assess the changed expectation against the requirement. No additional reviewer or per-edit LLM call is needed.

## Retrieval and total burden

Use the current task's explicit spec/requirement references to select a small amount of testing context. When the user invokes test design or the current task already declares tests, retrieve the relevant strategy/standard section once and reuse its version. Do not guess relevance from every source edit, traverse the entire graph, repeatedly read the same note, or inject a long universal checklist.

Record availability, retrieval, application and successful verification separately. Evaluate total tokens, execution time, tool calls, false failures, repair cycles and missed seeded defects. A guide that is never used provides no demonstrated benefit; a more elaborate workflow that increases bookkeeping without catching defects is not an improvement.

## Implementation boundaries and qualification sequence

The first implementation uses `testing.guidance` and `testing.suites` in project configuration, existing versioned knowledge for strategy/spec references, existing session archives for standalone runs, and existing Bash authorization/check execution. It introduces no GraphQL schema or model-tool family. The bundled standard is retrieved by section; it is not appended to every coding prompt. Turning guidance off changes the optional design draft only. The advice value is excluded from execution selection identity; saving a configuration file inside the observed workspace can still conservatively mark earlier source evidence stale.

Standalone suite receipts never discharge current Chat/PAT obligations, including when the selected command equals the configured coding check. Frozen spec references must resolve to the current version in the same project. Configuration, source and spec freshness are derived during inspection; historical bytes remain unchanged. Manual prerequisites cannot be verified by this runner and remain blocked. Archive retention follows the existing session policy, not permanent storage.

Supported UI and runner behavior needs component and exact-binary native qualification. Natural-model adoption, improved test quality and net token/time benefit require fresh comparative evidence and are not implied by the existence of the guide or passing scripted fixtures. Broader report adapters, automatic semantic assessment and additional test frameworks are outside this first scope.

1. **Guidance and intent:** add the sectioned test standard and project strategy convention; remove duplicate advice. Exercise actual bounded retrieval and a few independently specified test examples.
2. **Selection and inspection:** add the single Tests view and small project-config extension. Project existing specs/results through GraphQL. Test enable/disable, guidance off, persistence and immutable history without model inference first.
3. **Execution completeness:** extend the existing check receipt with frozen selection/spec references and one supported structured-result adapter. Preserve current command/ownership checks and legacy opaque-command limits.
4. **Natural-model qualification:** rerun a bounded comparable workload with and without the feature. Include known wrong fixtures, untested persistence, hidden UI, missing prerequisites and changes to expected behavior. Compare measured quality and burden; do not count our benchmark hints as spontaneous uptake.

## Required acceptance controls

- Disabling a failed required test cannot make qualification green.
- Optional disabling preserves failures, scope and a user-visible reason; re-enable preserves history.
- Guidance off reduces offered context while required checks remain enforced.
- Zero matching tests, absent browser prerequisites and missing reports cannot satisfy declared coverage.
- Spec, test file, command or selection changes make incompatible prior results stale.
- Resume/retry retains the frozen selection and correct owner; no duplicate execution or borrowed receipts.
- An ordinary read-only task does not acquire unrelated coding or testing obligations.
- A checker fails a known incorrect fixture and passes the intended valid fixture.
- Real public-entry-point evidence is distinguishable from helper/debug-state tests and visual inspection.
- Existing scripts and projects without registered specs continue working with honest limits.

## Current evidence motivating the proposal

R2 CODE-002, CODE-013, CODE-018, CODE-021 and VERIFY-002 provide concrete counterexamples: lost upgrade maps, hidden startup UI despite 37 passing tests, mistaken fixture/oracle repairs, semantic edit damage, and synthetic browser probes described too broadly. These are retained in v0.2.3-failure-map.md and the append-only failure ledger. The proposal does not retroactively repair or qualify those artifacts.

Implementation owners: `internal/memory/knowledge_navigation.go` and `tests-standard.md` provide sectioned knowledge; existing GraphQL knowledge stores strategy versions; `internal/config/testing.go` owns project suite settings; `internal/run/testing.go` owns frozen standalone execution and archive inspection; `internal/tui/tests.go` owns controls and the visible Chat draft. Existing task/contract/check receipt owners remain authoritative for their attempts. Source-level existence and focused tests are not release or natural-model qualification.


### Structured report boundary

The first adapter supports direct `go test -json -count=1` output captured by the current check receipt. Each structured event must have a timestamp within the recorded command interval; historical, missing, malformed or truncated output cannot establish completeness. Skipped packages remain visible separately from skipped tests and invalidate complete selected-scope coverage. Other commands retain process-result evidence with coverage unknown. These checks reject accidental replay; they cannot prove an intentionally fabricated test report truthful or establish semantic test quality.
