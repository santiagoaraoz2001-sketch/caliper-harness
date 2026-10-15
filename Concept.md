# Caliper Harness — Concept

## Goal
A lightweight coding harness that lets small models do real work. Low overhead, sharp tools, aggressive context management.

## Principles
- Operating window: designed for 64k–128k context models.
- Overhead stays lean: every tool and prompt earns its tokens.
- 5k challenge: a functionality experiment in minimal overhead, not a benchmark.
- Tools are sharp and brief. Pi's skill design is the strict model.
- Context is first-class: prune in-flight, compact on trigger, summarize aggressively.
- Local-first: runs against local vLLM / MLX from day one.
- Single fast native binary. Instant startup, responsive TUI.
- Operable by small models: every interface simple enough for a weak executor to use correctly.
- Everything tunable lives in YAML: thresholds, splits, and behavior switches are user-editable config, never hardcoded.

## Core: GraphQL
One GraphQL layer exposes memory, plans, tracking, writing, and feedback through queries and mutations. State validation and transitions live in the owning packages; in-process Go callers and model tools may invoke those same operations directly. GraphQL must expose the complete supported state surface and enforce the same rules. The server is embedded in the binary — no separate process to run. Storage lean: embedded Kuzu graph database. Must verify Go bindings and concurrent access first; SQLite is the fallback.

> OWNER AMENDMENT 2026-10-09 (R27/D02): accepted owning-package operations plus a complete GraphQL interface in place of exclusive GraphQL access. This is the target contract; completeness remains to be qualified. Evidence/disposition: verification/2026-10-09-remediation/CONTRACT_DECISIONS.md.

## 1. Memory and Recall
- Organic recall: the model queries past work, decisions, and behavior through GraphQL instead of relying on stuffed context.
- The harness surfaces relevant memory on request and auto-inserts appropriate context where relevant.
- The decision model may guide recall. Experimental — must be tested before committing.
- Writes are explicit mutations: what happened, what was decided, what to remember.
- Full responses stay available until compaction (important for behavioral data), then collapse to summaries.

## 2. Planning
Plans are the core object, built around the lab planning skills. Three levels, stored in GraphQL. Current plans stay in context; commands retire completed plans and approaches, which remain accessible.
- Plan: sits above the orchestrator. States what must be achieved. Carries dev rules, guidelines, and goals. Made of articles; each article gets one approach. Complete only when all approaches complete.
- Approach: one per plan article. Orchestrator-managed. An orchestration and validation plan built as a structured task list in the lab task-block shape (current, target, sketch, acceptance, tests, verify), authored one block at a time through a template. Each block must pass the granularity test — a smaller model can execute it with no questions asked — before the approach can launch.
- Task: one subagent prompt. Multiple per approach.
- Ordinary coding uses the core tools directly, with an actual declared project check. A model-callable `plan` entry invokes optional guided Plan→Approach→Task construction when useful or requested by the user. Planned work separates the user-facing orchestrator, bounded workers and independent validation; actual readiness/claims, paired checks and reviewer receipts qualify its tasks. Execution binding is separate from navigation focus.

> OWNER AMENDMENT 2026-10-12 (D46): implement the whole guided authoring/execution/validation/remediation/progress cycle, not a flat brief or question wording alone. Model-authored questions and answers remain durable, IDs and transitions belong to the host, and active context maintenance covers orchestrator and workers. Ordinary coding does not require PAT. Accepted target; source/native qualification is pending. The complete recovered instructions and implementation mapping are in verification/2026-10-09-remediation/R34/optional-guided-planning-20261012/.

> OWNER AMENDMENT 2026-10-14: connect current planned work to its actual execution. The genuinely claimed frozen Task becomes protected system instructions, followed by one short shared check/review/PAT handoff paragraph. Reuse direct main-task execution and independent review; explicit worker dispatch remains available for concurrency. The host binds identities and legal handoffs. Existing advisory progress checkpoints cover settled ordinary/PAT tool batches, including Bash, and real phase changes. Continue and supervisory confidence below 0.5 are silent; other valid directions nudge at natural message boundaries without discarding reasoning. No new limit, tool or mandatory PAT for ordinary coding is implied. Implementation/native proof are recorded separately in verification/2026-10-14-pat-progress-connection/.
- Small-model feasibility: the model operates one node; the harness owns the graph. Transitions are enforced by code — no completing an approach with open tasks, no spawning outside an approach. One tool per transition, each returning the legal next moves. Templates scaffold every plan, approach, and task prompt; validation scripts reject malformed ones before spawn. The decision model rules on all gates: task well-formed, approach verified, replan needed.

## 3. Capabilities
Tool protocol: use structured tool definitions, calls and correlated results where the selected endpoint supports them, through the existing client and registry. Validate each complete structured call array before any effect, then execute serially with per-call approval, receipt and step charge; failure, denial or cancellation leaves the tail unexecuted. Preserve argument validation, cancellation and partial-output safeguards. Text compatibility batching remains refused. The schema-first text protocol remains an explicit compatibility route: the model names a tool, receives its schema and purpose, then fills key: value slots; missing/invalid slots receive targeted repair. Never silently downgrade or execute guessed corrections. Advertised tools and arguments must match the current request registry. Schemas and native call/result associations remain intact during deterministic pruning.

> OWNER AMENDMENT 2026-10-09 (D11/R36): owner accepted structured calls where supported after the observed prefixed-tool premature completion. This is the target contract; implementation and coding benefit remain unqualified.
- Coding completion: ordinary runs require a declared project check, configured in YAML or explicitly proposed and recorded from inspected project files. Only its actual current exit0 receipt can establish declared-check success; later edits or opaque shell commands invalidate it. Preserve artifacts on missing/failed checks. There is no conversation mode. Workflow tasks retain their required check plus reviewer PASS; title/summary helpers remain separate.

> OWNER AMENDMENT 2026-10-09 (D12/D14): this is a coding harness. The completion contract above is accepted target behavior, pending implementation and live qualification; a model final cannot substitute for the check.

> OWNER AMENDMENTS 2026-10-10 (D17/D18): validated native arrays execute serially. A normal complete final triggers a clearly announced configured host check once if none was attempted at the current revision, through existing approval and execution rules. No failed/denied retry, guessed command or continuation after thinking-only/partial/managed stops. These are approved target changes; implementation and Mac coding qualification remain pending.

> OWNER AMENDMENTS 2026-10-10 (D19/D20): inclusive line edits require the exact range to have been returned by read at the current complete file hash, with conversation-owned transient read authority. Bash, whole-file write and exact-span edit keep their existing rules. Successful edit/write receipts disclose actual operation scope, hashes, newline status and at most 16 deleted/inserted source lines in total under the existing byte ceiling, explicitly marking omissions. No argument or newline correction; actual-read admission does not prove correct source selection or comprehension. Source/component implementation is accepted after root full27-package checks and independent alias/read/receipt controls; Mac coding efficacy remains unqualified after the fresh thinking-only TaskG failure. See verification/2026-10-09-remediation/R36/structured-calls/read-grounding-native-terminal/ROOT-ACCEPTANCE.json.

> OWNER AMENDMENT 2026-10-09 (D15): a fully completed normal reply containing only thinking, with no answer or tool call, stops the run and requires explicit resume. Preserve original reasoning and known usage without inventing tool execution. No automatic continuation or restart; truncated replies and transport failures retain their failure safeguards.

> OWNER AMENDMENT 2026-10-13: output-length continuation carries the actual unfinished assistant prefix through the compatible runtime, without adding a continuation prompt or executing partial calls. A normal thinking-only finish remains subject to D15. Cancellation and transport failures preserve their actual outcomes. Component implementation and native coding qualification are separate; see verification/2026-10-13-context-continuation-snake/.

> OWNER AMENDMENT 2026-10-13 Lima / 2026-10-14 UTC: use the actual results already present in model context. Do not require another model query just to read them again. Receipts must disclose applied, refused, partial and unknown effects accurately; an error must not erase earlier committed effects. Host-owned current-state checks retain lifecycle and authorization rules. Guided planning presents its current question and legal arguments; future control words are used only when that phase is available.
- Search and browsing: minimal, fast code search plus web fetch. Returns digests, not dumps.
- Bash: managed execution with timeouts, output caps, and summarization. Raw output never floods context.
- Questions and answers: the model can ask the user unlimited questions mid-run.
- Authorization modes: YOLO, Authorize Risky, Authorize All. Risky calls are judged by the decision model.
- Subagents and workflows: launched by a single tool call on the approach document. The harness reads its task list and fans out deterministically — spawns subagents, queues workflows, collects results. Loops and retries are harness-managed, not prompt-hoped. The orchestrator monitors instead of micromanaging. Launching and managing them must be trivially simple — a small model does this alone.
- Compaction: automatic. See section 4.

## 4. Context Management
- Proactive cleanup is deterministic and makes no model calls. Only consumed tool-output bodies more than 50,000 context tokens old may be replaced with archive references; the age threshold is configurable and estimated counts are labelled.
- Preserve thinking, schemas, user messages, current information, unread/protected results and tool-call/result associations during proactive cleanup. System instructions remain outside pruning and model-written compaction. Raw history stays addressable.
- LLM-guided compaction runs only at 90% context usage or an explicit user request.
- Model-written compaction at 90% context usage keeps the latest 40% verbatim and targets a concise summary using the configurable 10%/1%-step/20%-maximum writing targets. These targets guide writing; a longer summary is accepted when the complete continuation is smaller and fits the model context. An over-target summary alone must not stop the run.
- GraphQL add-in keeps full model responses addressable until compaction, so pruning never loses retrievable detail.

## 5. Decision and Quality Model
A separate small model (e.g. Laya, JEV-like role) runs as a first-class concurrent harness service — fast enough to run alongside the executor without contention. It aids decisions, gates quality, manages loops and workflow transitions, and at compaction decides what to keep, what to summarize, and what to drop. The executor does the work; the decision model keeps it on track.
Invoked both ways. Automatic at mandatory gates — task well-formedness, approach launch-readiness, compaction triage, loop detection, workflow transitions — triggered by the harness so nothing is skipped. On-call as a tool for discretionary judgment: stuck checks, option picks, result acceptance. Rule: if skipping the check risks fake progress or a stuck loop, it's automatic.

> OWNER DIRECTION 2026-10-10 (D23/R27): trigger the decision model to enforce meaningful Plan→Approach→Task bookkeeping and durable product knowledge. An unfinished plan with no relevant graph update prompts judgment about whether an executor update or alert is needed. Reuse host evidence and owning state rules; model judgments cannot substitute for actual checks or reviewer PASS. Exact triggers and thresholds remain a concrete proposal, not implemented or qualified behavior.

> OWNER AMENDMENTS 2026-10-10 (D24–D26/R27/R28): the main model executes a real bound task with independent review. A normal complete final with missing required bookkeeping may receive a decision-directed repair within the remaining run budgets; thinking-only, truncated and stopped replies retain explicit-stop rules. The model GraphQL interface gets dedicated configurable limits, with new numerical defaults still awaiting owner selection. These are target contracts, not qualified capability.

> D26 FOLLOWUP 2026-10-10: selected GraphQL defaults are 64 KiB request/result, 1,024 expanded selections, 256 aggregate records and a 30-second ceiling within the remaining run budget. Composed mutations are allowed with explicit partial effects: typed validation precedes dispatch; earlier committed writes and actual IDs must be disclosed if a later field or delivery fails. No implied rollback or automatic replay. Bounds apply before large selected reads as well as to the encoded reply; implementation remains unqualified.

## 6. TUI and Sessions
- Sophisticated terminal UI. Fast, keyboard-driven.
- Past sessions browsable inside the TUI. Selecting one opens it in a new tab as a live session.
- Views: chat, session browser, and tabs as the base; Plan/Approach/Task trees with deep-dive, decision-model activity, workflows, file-following activity feed, live diff monitor, and settings on top.
- Design follows the Specific Labs color scheme and design language.
- Advanced model parameters (temperature, topK, repetition penalty, etc.) adjustable live from the TUI.
- Sessions stored as plain JSONL files on disk, indexed through GraphQL. Auto-deleted 3 months after last use; threshold is customizable.

## 7. Local-First and Hardware
- Models are acquired by the user. The harness discovers existing model locations and connects compatible assets to their runtime. Load means resident in memory and ready to generate; unload releases that memory. Model download/pull controls are outside the harness.
- Compatible from deployment with vLLM and mlx_lm.server (≥0.31: continuous batching, prompt cache) matching its concurrency and speed on Mac. Concurrency knobs: --decode-concurrency, --prompt-concurrency.
- Hardware autodetect manages headroom and limits (context size, concurrency, model choice).

## 8. Feedback and Evolution
- One slash command triggers a background model to analyze the transcript.
- The planned evolution route updates a configured Markdown user profile and the Behavior document with explicit version checks and write receipts. GraphQL stores authored knowledge and observed lifecycle facts; it does not substitute for an actual document write.
- The behavior document is always injected into the models. Starts empty (omitted when empty); capped at 1,000 tokens.

## 9. Performance and Language
- Requirement: single binary, instant startup, responsive TUI, low idle footprint.
- Language: Go. Single binary, strong concurrency, excellent TUI ecosystem (Bubble Tea), fast builds.

## 10. Error Handling
- Errors are explicit, in plain natural language, short and simple.
- Every error states what failed and what the model can do about it, so a small model can react without guessing.
- User-facing errors are explanatory and link to a wiki entry describing the failure mode and how to fix it.
