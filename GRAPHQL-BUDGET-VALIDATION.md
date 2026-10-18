# GraphQL budget correction — 2026-10-18

The owner rejected Q/A simplification and authorized expansion of the existing GraphQL guard, publication across MacOS/Windows/Linux, and a fresh unchanged Laguna/native-D1 benchmark. Guided questions and all PAT execution/qualification rules remain unchanged.

The old default `tools.graphql.max_result_bytes: 65536` was used both for serialized responses and conservative prefetch source accounting (four bytes per character). The native run saved both task briefs, then its internal `Assigned.Facts` query failed while selecting full draft/active plan inventories. The refusal was not a model context overflow or a failed authoring mutation.

The default and example YAML now use `1048576` (1 MiB). Existing explicit user overrides remain honored. Request, selection, row, time and native D1 input budgets are unchanged. No new query path, ledger, tool, limit, workflow or prompt was introduced.

`TestCapturedPATInventoryFitsDefaultBudget` reconstructs the exact authored Plan/Approach/two Task briefs from the saved native calls into a temporary real Kuzu graph and runs the same inventory selection. Before the fix it fails with the identical `input:1:202: active tools.graphql.max_result_bytes selected source exceeded` error. After the fix, it returns 19,969 bytes with both complete sketches/review instructions. It also retains an explicit 64 KiB negative control, proving the user override still refuses the original payload.

Full-suite validation initially caught the example YAML default mismatch and two historical cap tests whose fixtures fit the new default. The example was corrected; the two tests now explicitly configure their original 64 KiB control, preserving refusal and retention/qualification assertions. The default replay remains separately tested.

Validation results and fresh native startup are recorded below when observed. This larger finite budget fixes the captured case; it does not establish unlimited graph size or live coding success. Windows/Linux native execution is unavailable; cross-compilation is distinguished from native testing.

Local gate update: build/vet/format/diff pass; affected config/graph reruns pass (0.740s/28.941s); Windows/Linux amd64 config tests cross-compile. Full sweep passed28 packages and exposed the documented config/graph assumptions; the run package then exhausted its aggregate240-second test timeout while TestPlanningWireDeclarationStable had been running only1second. No failed assertion in that package was recorded. An isolated run-package rerun with600seconds test time is in progress. This changes test admission only, not product/model limits.

Final component gate: isolated run package passed: `ok  	specific-harness/internal/run	239.630s`. All31 tested packages have passing results across the sweep and corrected/isolated reruns; this is not one uninterrupted all-green sweep. Build/vet/format/diff and Windows/Linux amd64 config cross-compilation pass. Captured old-budget failure reproduced; expanded default returns complete19969-byte inventory. Fresh native qualification remains pending.
