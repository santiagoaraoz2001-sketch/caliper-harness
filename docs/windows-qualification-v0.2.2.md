# Windows qualification — v0.2.2 local candidate

Recorded 2026-10-24 on the HP, Windows amd64, Go 1.27.0. This records the local source candidate, not a published release. Later production edits require their relevant checks; the reviewer GraphQL access changes being developed after this run are not covered by its results.

## Full-suite result and corrected rechecks

Executed `go test -json -count=1 -timeout 30m ./...`. The command completed with **exit 1**, not a clean full-suite pass. The slowest package, `internal/run`, completed in **883.421 seconds**; it did not time out.

The JSON log records 38 packages: 31 passed, 3 failed, and 4 had no test files. It records 3,585 passing test/subtest events, 15 failing events and 33 skipped events. Parent and child subtests are counted separately; these numbers are not independent assertions or coverage percentages.

Every failure from that full run was subsequently diagnosed and corrected:

| Failure | Cause and correction | Recheck |
| --- | --- | --- |
| Store buffer-pool/alias ownership | Fixtures required privileged Windows directory symlinks. They now use actual junctions when symbolic-link creation is unavailable. Canonical physical identity remains required. | Entire store package passed, 11.358s. |
| Existing-work and planning-instruction wording | Tests required superseded phrases such as “fresh paired” despite the unified current-task/attempt receipt rule. Assertions now check that rule and missing/stale-check obligations. | Prompt package and focused lifecycle checks passed; see final-rechecks log. |
| Typed and full-receipt task claims | Tests expected root/no focus after `task_start`, contradicting the intended v0.2.2 task-scope fix. They now require task focus with the exact actual claimed ID, while preparation remains unclaimed. | Native/text, explicit/omitted task variants and full-receipt claim passed. |
| Preparation recovery, structured coherent-final case | The scripted model mistook a legitimate summarizer request for the next executor request. The fixture now answers that separate role, checks protected guidance anchors, and retains every original executor preservation assertion. No context window increase or production workaround. | All preparation-recovery variants passed. |
| Scoped host check success/failure | Git Bash `pwd` returns MSYS paths; the test compared them directly with Windows paths. The Windows fixture uses `pwd -W`, then native path normalization. Approval, exit code, task state and receipt assertions are unchanged. | Both success and failure cases passed. |

The combined final selection of affected run tests passed in **43.566s**. This closes the observed failures with focused evidence; it does **not** turn the earlier full command's exit code into zero. A second complete command was not run merely to repeat unchanged passing coverage.

`go vet ./...` passed on the production source after the shared physical-path fix, before later reviewer-access work. `git diff --check` also passed at this checkpoint.

## Product defect exposed during qualification

**WIN-007: directory junctions bypassed the logical workspace boundary and split identity.** On current Go versions Windows junctions appear as `ModeIrregular`, not `ModeSymlink`; `filepath.EvalSymlinks` leaves them unresolved. Before the fix, `TestBoundDiscoveryWorkspaceScope` returned `escape/visible.txt` from a directory outside the bound workspace. Model discovery also treated a junction alias as a duplicate model directory, and alias-based workspace/store identity was inconsistent.

The shared `internal/pathutil` helper resolves existing Windows paths through `GetFinalPathNameByHandle`, closes the handle, and bounds the path buffer. It uses the existing `x/sys/windows` dependency and does not enumerate a directory tree. Other platforms retain `filepath.EvalSymlinks`. Physical identity is used consistently by discovery, file access, graph/source anchors, store/configuration, sessions, launch and package paths. Final reparse directories are refused where a real final directory is required.

Real-junction tests now cover outside search refusal, allowed internal aliases, store writer/buffer-policy ownership, workspace identity, bundle-manifest containment and updater archive containment. Search, files, graph tools, serve, store, configuration, bundle and updater checks passed after the changes. A dedicated native test covers Unicode paths, physical outside identity, missing-path errors and relative-path behavior.

If older Windows runs created separate histories through junction aliases, these files remain intact. This fix does not automatically merge previously split histories. It does not change the HP's ordinary non-junction project location or claim to provide a Bash sandbox.

## Fixture corrections and coverage limits

Other Windows fixture repairs retain their behavioral assertions: legal native Unicode filenames replace POSIX-only colon/quote/control characters on Windows; POSIX cases remain on POSIX; canonical/native path expectations match the relevant tool contract; fake discovered executables use `.cmd`; a cmd marker no longer includes an accidental trailing space. Four configuration roundtrip/default tests now isolate personal connection settings themselves.

Directory junctions exercise directory-reparse behavior without changing system privileges. **File-symlink tests still require Developer Mode or privilege** and remain explicitly skipped when unavailable. Mixed tests preserve their non-link assertions and expose the unavailable file-link fixture as a named subtest. No privilege or security settings were changed.

Windows cannot represent raw non-UTF8 filename bytes through its UTF-16 path API. That native filename subtest is explicitly skipped; malformed/base64/non-UTF8 decoder controls still execute. Other recorded skips concern opt-in live models, captured external traces, managed/released bundles, visual galleries and platform permission behavior. Three directory-link tests that were skipped in the initial full log were later exercised successfully with junctions: workspace identity, external bundle link refusal and updater archive escape refusal. Skips are not passes.

Linux/macOS binaries, release transfer behavior, publication and installation are outside this local Go qualification. The upcoming live Space Transporter run supplies different evidence from scripted integration tests.

## Reproduction and raw evidence

Raw logs remain in `%TEMP%`, not the repository:

- `caliper-v022-full-qualification.jsonl` and `.exit`: complete original suite, nonzero result retained.
- `caliper-v022-win-current.log`: native fixture failures before corrections.
- `caliper-v022-win-fixed.log`: real junction scope bypass before the physical-path fix.
- `caliper-v022-win-physical.log`: files/graph-tools/serve passes after the fix; remaining search expectation failures were corrected next.
- `caliper-v022-win-scope-final.log`: complete search and physical-path package passes.
- `caliper-v022-store-final.log`, `caliper-v022-final-rechecks.log`, `caliper-v022-run-failures-final.log`: corrected package/lifecycle results.
- `caliper-v022-recovery-diagnostic.log`: summarizer-role diagnosis; temporary diagnostic logging was removed from the test.
- `caliper-v022-junction-package-checks.log`: bundle/updater passes; also contains a configuration run affected by personal connections, corrected and rerun in `caliper-v022-config-self-contained.log`.
- `caliper-v022-vet-full.log`: empty successful vet output.

The full suite used process-local `APPDATA` under `%TEMP%/caliper-v022-test-runtime/appdata`, the bundled Python runtime as `PYTHONHOME`, and a temporary Python3 launcher/PyYAML site directory on `PATH`/`PYTHONPATH`. PyYAML 6.0.1 matches the existing decision requirements and was installed only in that temporary test directory. No persistent environment variables or live inference connection settings were changed. Later self-contained configuration tests passed without the external APPDATA override.


## Subsequent role-access qualification

The final consistency audit found that child roles needed graph knowledge access. Workers now use only knowledge/appendKnowledge, bound to their actual child-task author; reviewers use knowledge only. The API rejects disallowed root operations before effects, including aliases, fragments and mixed-operation documents. Help and system hints reflect the same restricted operations. No additional model-facing tool family or review pass was added.

After this change: full tools, tools/graph, prompts and graph packages passed; actual reviewer identity retrieval and write refusal passed; worker ownership/help/lifecycle-refusal tests passed. Changed-package vet passed. Two agent tests initially encountered the Windows Store Python alias; those tests and shared-context regression passed with the established temporary Python shim. Raw logs: `%TEMP%/caliper-v022-review-role-test.log`, `caliper-v022-role-test.log`, `caliper-v022-role-packages.log`, `caliper-v022-role-agent-regression.log`. The controller's eight tests also pass. This does not rewrite the earlier full-command exit 1 as a clean full-suite run.

The final candidate's Windows Code Integrity signing block remains unresolved; these component results do not constitute a successful final-binary launch or live game run.
