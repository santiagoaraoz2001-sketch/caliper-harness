# Stopped-run audit — 2026-10-16

## Scope and outcome
Owner requested stop and discussion before further patches. Owned controller received Ctrl-C at approximately 16:08:40 UTC; PTY EXIT.json confirms settlement. Application exit 0 is not task success. Session records cancellation and Task A attempt 1 unverified. No restart or product changes in this audit. Resident model services were not killed.

Approximately 2h03 elapsed. All 47 worker requests were indexed; 46 responses completed and request 47 was interrupted. Review combines the complete wire/event timeline, relevant reasoning and repeated-paragraph analysis, actual tool receipts, D1 inputs/results, source routing, and isolated post-stop regression checks. Full source prompts from the previous preparation-consistency change were not loaded into this immutable run.

## Final rubric
| Capability | Result | Evidence |
|---|---|---|
| Startup/model transport | Pass for this run | Laguna and native D1 served requests; no recorded D1 judgment-error |
| Discovery and tool use | Mixed | Useful reads; initial wrapper/phase errors recovered; stale beliefs about Bash persisted after claim |
| PAT drafting/readiness/claim | Functional, inefficient | Plan, Approach and two Task briefs; Task A claimed 15:04:01 UTC, about 58 minutes after start; 30 successful plan calls overall |
| Implementation | Task A succeeds; Task B absent | Two edits at 15:26:52; movement validation and regression test changed; score-load fix untouched |
| Requested regression-before-fix | Missed | Model edited test and implementation in the same native array without first running failing regression |
| Paired checks | Pass for Task A | 15:30:34: task-specific behavioral test and project suite, 18/18 project tests |
| Independent review | Not reached | No reviewer verdict; successful check is not qualification |
| Handoffs/closure | Not reached | Premature EXECUTE did not advance unqualified Task A; no Task B execution or Approach/Plan closure |
| D1 progress supervision | Available, ineffective at key points | 62 judgments, 0 judgment errors; stale repair advice and final continue despite repetitive completion confusion |
| Context/continuation | Mechanism works; progress not assured | Request 47 messages identical to request 46, exact response_prefix supplied, no synthetic continue message; then user cancellation |
| Cancellation/evidence | Pass | Partial response preserved, task marked unverified, session and files retained |
| Other auth modes/evolution/web | Not exercised | Outside this YOLO two-fix test |

## Confirmed failures and proposed minimal corrections

### 1. Completion instructions conflict
The claimed-task instructions say “your answer requests independent review” and “A final answer requests independent review”. The latest planning receipt says “answer only the next advertised question”. internal/run/planning.go groups TaskRunning with TaskStopped/TaskCancelled into phase stopped. Its receipt describes the healthy running task as “running, stopped or cancelled” and advertises STATUS/recovery guidance, not a clear review handoff.

After passing checks, request 44 tries EXECUTE again; request 45 asks STATUS. Response 46 repeatedly searches for COMPLETE/FINISH and considers checking again. It emits no final answer/tool call before the output boundary. This is a demonstrated instruction/interface defect, not evidence that the independent reviewer itself failed.

Proposed correction: reuse current state/receipt owners; distinguish running from stopped instructions, and state the exact next action consistently in the claimed-task instruction, check receipt and STATUS: checks passed, review not started, send a normal final answer with actual evidence and no tool call to request review. Remove planning-only question guidance from execution receipts. Keep qualification and premature-handoff protection. No new completion tool or limit.

### 2. Stale phase/refusal advice outlives the refusal
Post-claim response 37 still reasons that edit/write/Bash are unavailable, even while noticing Bash in the current tool header. Response 41 repeatedly concludes it cannot execute the requested regression before editing. It finally writes the regression and fix together. D1 selected repair-latest-rejected-call after claim and again after the successful check, despite no pending repair. Historical refusal/advice competes with current permissions.

Proposed correction: existing phase-change receipts should explicitly establish currently available coding actions; stop reissuing resolved repair advice as current. Ground the existing watcher advice in current phase and pending refusal, while retaining historical evidence. Previous preparation changes already recognize successful plan progress, but stale stored advice and execution-phase receipts still need attention. Do not add another enforcement gate for the red test.

### 3. D1 stayed alive but did not diagnose the completion loop
62 recorded judgments: 43 PAT/product, 17 discovery, 2 coherence. 24 below confidence 0.5 correctly produced no nudge. Decision capture includes eight input-capacity rejections handled by existing optional-evidence reductions; these are admission retries, not service deaths/timeouts. Maximum recorded request elapsed about 10.4 seconds.

The streaming repetition guard recognizes short consecutive exact units, not the long paragraph cycles here. Completed-progress inputs reduce reasoning to hash/length. At response 46's output boundary D1 did receive the latest 4 KiB unfinished-reasoning sample and task/check state, yet chose continue at confidence 0.523. Thus this is not solely missing input; the decision itself was unhelpful. After the check it had chosen repair at 0.503 despite no pending repair.

Response 46 took 25m33 and used 32,768 output tokens. Its 1,013 nonempty paragraphs contained only 69 distinct paragraphs; approximately 91.5% of reasoning characters are repeated exact paragraphs after their first occurrence. This specific response is repetitive; long reasoning in general is not a failure.

Proposed correction: first remove completion ambiguity. Then replay captured D1 packets against current owner facts: active task, actual check result, review not requested, pending repair empty, and lack of new effects. Use the existing bounded sample and checkpoint mechanism. Preserve silent Continue, silent confidence below 0.5, and advisory-service failure fallback. Do not add a hard stop or token ceiling. Whether D1 catches semantic repetition within a single generation remains unproven; do not claim a receipt correction solves that by itself.

### 4. Planning/cache costs are real; existing source correction is not live-qualified
Claim took about 58 minutes. Many short Q/A responses spent 60–129 seconds with zero reported cached tokens. Prior complete trace/template analysis identified changes to early system/scope text and native plan schema, plus earlier-history guidance rewriting. Preparation-system/schema stabilization has been implemented and fixture-tested separately, but this run used the old candidate. Later pruning/history changes can still invalidate cache even with stable preparation headers.

Next evidence should qualify the already-written stable preparation changes and completion correction before introducing additional planning features. Keep cache measurement separate from decode timing; end-to-end response durations do not measure pure generation speed.

### 5. Continuation preserves an unresolved loop too
At the output boundary, continuation retained the same messages and supplied the original response prefix. This is the requested no-added-message behavior. It cannot alone teach the model how to request review when receipts contradict the completion contract. User cancellation stopped the next request. Do not solve this by deleting reasoning or adding arbitrary continuation prompts/limits.

## Independent audit of the actual code
In isolated copies after stopping (not operator changes during the live run): final candidate passes 18/18 tests; applying its new regression to original game.js fails, actual movement [3.5,3.5] versus expected [3,2]. This confirms Task A's regression distinguishes the original defect and its fix is useful. It does not retroactively satisfy the model's requested red-before-fix sequence or establish independent reviewer PASS. Logs: stopped-audit/candidate.log and baseline-with-new-test.log.

## Attribution and discussion order
Harness defects: contradictory completion receipts, stale progress advice/current capability ambiguity, mutable preparation prefix/schema in the running candidate. Model behavior: skipped red test, repeated status/completion deliberation, ineffective D1 classifications. Review and handoff are unexercised outcomes, not demonstrated engine failures; premature switching protection worked. No operator code or graph rescue during this run. Isolated post-stop checks are explicitly operator validation.

Discuss and patch completion consistency first, then stale repair/D1 grounding. Validate from captured post-check state before spending another full planning run. Prior preparation fix needs live qualification. No new product feature, limit, goal, restart or publication authorized/performed by this audit.

## Evidence map
- analysis/all-turns.json and STOPPED-TIMELINE.txt: all worker requests/responses.
- analysis/session-event-index.json: tool/judgment/check/settlement events.
- analysis/reasoning-37-indexed.txt, reasoning-41-indexed.txt, reasoning-44-indexed.txt, reasoning-46-indexed.txt: key reasoning.
- wire-capture/wire/laguna-native-00046.request.json and 00047.request.json: identical messages plus original continuation prefix.
- decision-capture/: complete native D1 requests/responses.
- pty/EXIT.json and run/state/sessions/: cancellation and unverified settlement.
- analysis/ROOT-CAUSES.md: prior preparation/cache investigation.
- ../2026-10-16-preparation-consistency/REPORT.md: separately implemented source changes and tests.
