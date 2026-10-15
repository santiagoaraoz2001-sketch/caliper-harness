# Caliper Harness — capability inventory

Source review updated 2026-10-14. This records the current interfaces and proof boundaries, not release qualification. Exact current checks and remaining gates live in [ConstructionPlan.md](ConstructionPlan.md), [CapabilityRubric.md](CapabilityRubric.md) and the linked evidence. Historical model results stay attached to their tested binary/catalog.

## Interface

One Go executable hosts the in-process Bubble Tea TUI. Chat, Plans, Workflows, Files, Diff, Status, Sessions, Decisions, Settings and Setup provide the existing coding workflow. Startup and Setup discover models already downloaded by the user; loading/unloading refer to server memory residency, not acquiring weights. The managed Mac application folder includes the actual Kuzu library, private Python runtimes, sidecars and ripgrep. Preserve the intact folder; fresh-host/signing and useful small-model coding qualification remain separate gates.

Settings validates a complete configuration copy before live application or YAML save. External decision services show pending activation for requested model/device changes; an existing external process is not silently restarted. Configuration fields and the actual view determine the current rows.

## Slash commands

The current registry has 30 commands:

chat diff plans sessions decisions workflow workflows files settings status models new stop retry btw tools auth recall remember compact context clear comment goto open-diff changes turns help setup quit

`/tools` selects coding or full exposure. `/workflow` explicitly selects root or an owning qualified reference/UUID for subsequent requests. `/btw` delivers human steering through the existing generation interruption/handover. `/comment` retains task/source provenance and does not silently change a frozen objective. `/help` is user-facing; the model-callable help tool separately loads selected schema or guide content into model context.

## Model interface

Core coding tools are read, read_outside, find_outside, edit, write, bash, grep, find_file, find_dir, help, ask and check. Ordinary run wiring also exposes plan and GraphQL. Full exposure adds registered workflow, launch, memory, judge and retrieval operations. The current request registry is the authority: role and workflow scope determine its actual names, arguments and permitted effects. `help(topic="schema")` lists that current surface; selected schema/tool help avoids pasting the whole graph or catalog.

Read includes directory children, Go definition lookup and file outline. Outside access is read/search only. Edit/write remain project-bound and use actual file hashes; line-range edits require the current range read. Native arrays validate before effects and then execute serially with individual approvals/receipts. Text compatibility uses its advertised single block. A malformed call never authorizes guessed or unexecuted effects.

Ordinary coding can execute directly. Optional plan guides the question-led Plan→Approach→Task workflow. Preparation retains discovery tools; planned execution uses owning readiness and a real task claim. Creating metadata is not execution. Task briefs should own a requested behavior and its assertions together, rather than depend on tests assigned to later work. GraphQL and typed owning tools share the same lifecycle; they disclose real IDs and partial effects. Useful current results already in context do not require a duplicate query.

## Supervision, evidence and context

YOLO, authorize-risky and authorize-all retain their actual effect policies. Risky judges non-free tools; judged_tools also records explicit intent. Authorization/readiness thresholds remain separate from progress supervision.

The owner's 2026-10-14 progress rule is fixed: Continue sends no model message; supervisory confidence below 0.5 is logged as failed and supplies no nudge. Other valid directions are nonbinding nudges at natural request boundaries. They cannot stop, discard or forcibly restart reasoning. Literal output continuation preserves its input and thought prefix. Long thinking, including 32k or 100k, is not itself failure.

Actual coding completion still needs the declared behavioral/project checks and independent review where the owning workflow requires them. Failed, missing, denied or stale checks remain explicit. A normal complete final can announce/run the configured check once under existing rules. Model assertions, baseline zero-test runs and metadata writes do not establish requested behavior.

Proactive context cleanup is deterministic: only consumed tool outputs beyond the configured 50k-token age are eligible; reasoning, messages, current facts and raw history remain. Model-written summaries are available at the configured 90% emergency context threshold or an explicit compaction request. Host system instructions are protected. JSONL event logs and bounded continuation snapshots preserve actual outcomes for resume; measured latest prompt occupancy stays separate from cumulative usage.

## Current proof boundary

The interrupted Laguna/D1 Snake run eventually wrote game.js, but reached no game checks, independent review or playable product before the host reboot. Its captured traces and user actions remain evidence; it was not restarted. Confidence .404 progress/.340 bookkeeping guidance in that run should be silent under the latest rule. Source suppression and instruction-consistency corrections are being verified separately under [the current correction plan](verification/2026-10-14-supervision-silence/PLAN.md). A passing component suite does not establish native coding readiness. Qualification remains 29/41; feature construction is frozen during these corrections. Phase 6 exit, later construction phases and native release qualification are not promoted by this inventory.
