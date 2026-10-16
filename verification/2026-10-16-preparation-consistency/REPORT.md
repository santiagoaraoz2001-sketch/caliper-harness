# Preparation prompt and plan contract consistency

Owner request: stabilize preparation system instructions and the plan schema across the product. No live restart, new limits, workflow redesign or goals.

## Implemented

- `planningDefinition` retains the owning current-question validator and stale-state guard, and supplies a fixed native advertisement containing the supported argument names/types. Required fields and legal action values remain in the current question, not a changing early wire header.
- Preparation's selected state/current question is supplied at the conversation tail on a change, unless the latest receipt already contains that question. The system prefix retains the stable role; actual claimed Task and settlement roles retain their existing authority/identity.
- YAML instructions distinguish saving draft metadata from running code, explain that implementation tools follow claim, and retain checks, independent review, stopped-work recovery and existing-work handling. Help and progress preparation guidance refer to the latest question/receipt.
- Shared call templates show arguments directly, not a name/arguments transport envelope inside the argument example. GraphQL operation help now reuses that same formatter; its duplicate serializer was removed.
- `plan` joins the existing preparation-receipt recognizer, so a successful answer can replace stale failure disposition.

## Validation

New regression tests compare native plan declarations byte-for-byte through drafting/readiness, while proving a wrong current action still refuses. Another test proves preparation system text remains unchanged as the current question advances and unchanged questions are not duplicated. Existing protocol/help/resume/claim tests are updated to verify the new example shape and state placement rather than requiring state in the system prefix.

Initial draft duplicated field documentation in the stable schema and crossed context budgets in fixtures. Fixed by leaving detailed questions in receipts and advertising only stable names/types. No context limit was raised. Removed duplicate focused/planning role paragraphs and empty schema descriptions; reuse receipts even when subsequent host advice follows. The actual two-task check/review/handoff regression passes with its original context size. Full regression result to be recorded after completion.

## Evidence boundaries

This is source/fixture validation, not evidence of improved Laguna speed or model comprehension. The running Laguna process is on its unchanged prior candidate. Historical guidance replacement, phase transitions and explicit compaction may still alter prefixes; this change does not claim guaranteed cache hits. No grouped Q/A redesign was implemented.

At 15:44 UTC the existing run had 30 plan calls, 2 edits, 1 check and current-check-observed validation; review/handoff not yet observed. The operator snapshot script was repaired to ignore list-valued tool results when extracting mutation effects; no harness/model process was changed.

## Final validation result

- Build, vet, gofmt and diff whitespace checks passed.
- Full final-source suite: 30 packages passed; run package had one obsolete resume assertion requiring wording from the removed duplicate role paragraph. No other failures in that run (run256.023s).
- Corrected that assertion to require exactly one actual preparation role while retaining the existing single behavior/intent and no-replayed-creation checks. Reran scoped creation/resume, stable plan wire, two-task checks/review/handoff, current scope and all preparation recovery tests: PASS21.140s. Only the assertion changed after the full suite; production source stayed fixed.
- Final component suites: agent23.231s, tool1.185s, prompts0.567s, tools1.022s. GraphQL help/schema tests pass using the shared formatter.
- New source remains uncommitted and is not loaded into the running test. No new live performance claim.
- 15:59 UTC observer: request46 pending, no terminal event; prior settled evidence remains two edits and one check. Read-only process inspection confirms harness69266, Laguna85741 and nativeD164057 remain present. No restart/stop performed.
