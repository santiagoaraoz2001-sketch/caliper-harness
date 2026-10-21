# Editing model instructions

Copy the shipped `prompts.yaml` to a permanent location outside the application
folder, edit that copy, and set `prompts.path` to its absolute path in Settings YAML.
The application folder is integrity-checked at startup, so editing its inventoried
files prevents launch. In a source checkout, `internal/prompts/prompts.yaml` is the
canonical catalog. The next run loads your selected edits; an admitted run, its
workers and reviewers keep one immutable snapshot. The model-control notice names
the source. Updates retain the external catalog. The npm upgrade path also preserves
historical edits to a shipped catalog outside the version folder before activation.
Only the active role, current help section and advertised tool descriptions enter
context; the full catalog is never inserted into every request.

The `texts` mapping groups system/role instructions, worker and review scaffolds,
decision instructions, compaction, titles, preparation and continuation guidance,
model help and tool descriptions. Comments explain purpose, original owners and
allowed named fields. For example, edit `system.native.orientation` to change
the coding role or `help.read` for model-facing read help. `help()` pulls the guide
into model context; `help(topic="read")` pulls only the relevant section.

`setup.assisted` is the prompt copied from `/manage assisted` for a user's preferred
external LLM. Its `.OS`, `.Connections` and `.Project` fields are filled with the
current computer and configuration paths. It is loaded through the same selected
catalog and is not included in ordinary coding requests. This UI-only key is optional
for older customized catalogs; ordinary coding and updates keep working unchanged.
To use assisted setup with such a catalog, copy this key from the shipped YAML while
retaining your edits. Assisted setup names the missing key instead of substituting
instructions from another catalog.

`help(topic="reference")` opens the small topical index; request a document such
as `reference.pat` for lifecycle guidance. `help.reference.<topic>` in this same
YAML is its editable source. Startup versions changed documents in captured
project knowledge under `harness/reference/<topic>`, preserving previous heads.
For example, `query { knowledge(key:"harness/reference/pat",limit:1) { key body } }`
reads one through the existing bounded GraphQL interface. Reference retrieval is
independent of executable tool scope; tool help labels actual availability and
shows a current schema only when available. These references are separate from
`plan`'s current question-led authoring workflow and grant no execution authority.

Keep required keys and nonempty string values. Older customized catalogs may omit
`setup.assisted`, `continuation.project_style` and `planning.answer.coverage`.
Assisted setup reports its missing guide. Style injection and Approach coverage
disclose when they use shipped wording for their missing key; supplied custom
text is preserved, and an explicitly blank value is rejected. Quote standalone yes/no/on/off
values (or use a literal block) to avoid YAML-version ambiguity. Named template fields such as `{{.TaskID}}`
are supplied as actual data by Go; their allowed names are listed in comments.
Reordering or repeating an existing field is supported. Interpolation supports
plain `{{.Field}}` or single-value `{{printf "%q" .Field}}` (`%q`, `%d`, `%t`, `%s`).
Variables, indirect/index lookups, control flow and template definitions/calls are
refused at load; write instruction prose directly rather than embedding logic.
Runtime source, user
requests, authored task briefs, Behavior/profile files and knowledge remain data;
editing instructions does not change tool names/types, authority, approvals,
readiness, claim, budgets, actual checks or independent-review rules.

For an explicit alternate catalog, set `prompts.path` in the regular config YAML.
A missing file, unknown key, missing required key or invalid template fails admission; there is
no silent per-key fallback. When the default asset is absent, Go uses its disclosed
embedded shipped catalog. An owned Python decision service requires the real
shared asset and receives its admitted absolute path via `--prompts-file`. Python
keeps its static readiness instructions for that service lifecycle; Go sends the
run’s selected fallback decision instruction on each judgment. Custom service
recipes retain their literal arguments, and external services retain activation
ownership. Sidecar startup reads that file separately from an admitted Go run;
it is a process-start snapshot, not the universal snapshot of all running work.

The legacy default `templates` resolves the normal shared asset. Other explicit
`templates.path` and `--templates-dir` choices select `<directory>/prompts.yaml`.
An explicitly selected standalone `.tmpl` directory now reports a migration
error. The old `.tmpl` source files are inactive historical scaffolds; editing
them has no runtime effect. Use `templates.plan`, `templates.approach`,
`templates.task`, `templates.subagent`, `templates.review` and
`templates.compaction` in this YAML instead. Avoid specifying both an alternate
`prompts.path` and an alternate legacy templates directory.

The ordinary and claimed execution instructions now give a regression-first route
for reported bugs: inspect, reproduce on the original source, fix, check the final
revision and report. Already-correct source must not be broken to manufacture a
failing assertion. Later causal controls belong in an isolated copy or overlay.

`help(topic="graphql")` describes selected historical recall. Use
`sessionEvidence(sessionId: "…", offset: 0, limit: 10, callId: "…")`
to retrieve a saved call and its correlated result. For planned attempts, query
`taskEvidence` metadata first; its `record` and `phase` arguments select the
recorded execution, reviewer or check event group. Request only the needed message
fields. These reads use the current project scope and existing GraphQL budgets;
historical checks never verify the current files. Incomplete active calls are
identified without inventing a completed result.

When upgrading a customized catalog to v0.1.1, start from the new shipped YAML
and carry over your edits. It adds `help.schema.graphql.noticed` and
`help.schema.graphql.notices`, plus short discovery guidance in the existing
system and GraphQL help sections. A catalog missing those keys reports the
missing names at load; existing admitted runs keep their original snapshot.

A claimed task's full frozen brief is supplied once at system role; the claim
receipt records its identity and attempt. Observations in that brief are labelled
as recorded during planning, so later tool receipts describe the current state.
The Q&A sequence and required checks are unchanged.

Successful guided answers already return the next planning question. They do not
need an extra bookkeeping judgment to repeat it. Successful claimed work uses
the existing progress watcher; failures, changed planning questions, delivered
comments and normal final answers retain bookkeeping review. Readiness and
independent verification remain separate. Judge requests reference exact duplicate
call arguments once, with call IDs and digests; worker context and raw history
are preserved.
