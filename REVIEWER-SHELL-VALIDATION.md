# Reviewer and shell freshness correction — 2026-10-18

The preceding Laguna benchmark reached real edits and checks, then failed qualification: its reviewer modified artifacts and worked on a sibling task, a final `ls` invalidated the passed checks, and its final answer omitted a verdict. This patch fixes those owning paths without adding tools, limits or an alternative workflow.

## Changes

- Shell inspection preserves check freshness when project contents are observed unchanged. Persistent changes or unavailable observation invalidate it. Ordinary editing and child-workflow rules remain.
- Independent review has a protected frozen-task role and an actual read/search/help/Bash/check registry. Its help closure sees the same restricted registry as dispatch.
- Reviewer Bash and check operations are observed for project changes. Changes are preserved and recorded, and cannot qualify review. Cancellation/managed-stop status is preserved. Approval and current-task revision checks still precede execution.
- A normal completed reviewer answer missing PASS/FAIL/UNVERIFIED receives feedback in the same Runner, preserving evidence and existing budgets. Explicit UNVERIFIED terminates; thinking-only, partial and failed replies do not get this repair.
- Only owning qualified settlement enables the existing task/approach handoff. Review cannot implement a sibling task through editing/workflow tools.

## Content-observation boundary

Actual Git project roots include tracked and nonignored untracked files. Other project roots use the local file tree, even inside an ignored ancestor repository. Exact harness session/Kuzu state paths are excluded. File names, modes, contents, additions, deletions and renames are observed. Ignored Git outputs, linked-directory contents, external effects and temporary edit-then-restore operations are outside this proof. This is post-command observation, not a read-only shell sandbox. Nongit projects also observe generated files; commands producing those files can invalidate freshness. No arbitrary `data/` or source-extension exclusions are used.

## Validation

Evidence directory: `verification/2026-10-18-reviewer-shell-fix/` (local raw logs retained).

- Pre-fix controls reproduce `ls` invalidation, missing-verdict termination and a reviewer nested-check source mutation that still qualifies.
- Shell unchanged/create/rename/delete and content hashes with restored size/mtime pass. Ignored-ancestor and exact-state-exclusion controls pass.
- Actual reviewer Bash/check mutations refuse qualification. Current help/schema excludes editing; protected role contains actual task values. Explicit UNVERIFIED and sequential handoff pass.
- Full sweep exercised31 test packages:28 passed; launch/run/tools exposed old fixture assumptions. Full launch rerun passed111.653s; all affected run integrations passed40.640s; changed prompt golden passed0.737s. Final reviewer/handoff controls passed7.880s. Build/vet/format/diff pass.
- Fixture corrections preserve their assertions: role detection uses the original request, append-only test counters live outside the reviewed project, and the old stale-Bash test now actually changes a source file. Negative logs remain available.
- Windows amd64 and Linux amd64/arm64 pure-Go agent/tool/config/serve/hardware/process cross-builds pass. Target-OS native execution and complete CGO application qualification remain unperformed.
- Independent adversarial code review found no remaining substantive blocker after its findings were corrected.

## Fresh native benchmark

Case: `verification/2026-10-18-reviewer-shell-native/`. Unchanged two-fix Snake brief and untouched original baseline; Laguna-S-2.1-NVFP4-mlx plus native LiquidAI/d1-3B on the Mac GPU. Same sampler,32k output and context settings; no step or duration ceiling. No operator source edits, graph rescue, second task injection or game development. Startup and subsequent live status are recorded in the case rubric. This document does not claim a successful native PAT/check/review/handoff result before that run produces one.
