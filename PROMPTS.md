# Editing model instructions

Edit `prompts.yaml` in the application folder. In the source checkout it points
to `internal/prompts/prompts.yaml`, the single canonical catalog. The next run
loads your edits automatically; an admitted run, its workers and its reviewers
keep one immutable snapshot. The session’s model-control notice names the source.
Only the active role, current help section and advertised tool descriptions enter
context; the full catalog is never inserted into every request.

The `texts` mapping groups system/role instructions, worker and review scaffolds,
decision instructions, compaction, titles, preparation and continuation guidance,
model help and tool descriptions. Comments explain purpose, original owners and
allowed named fields. For example, edit `system.native.orientation` to change
the coding role or `help.read` for model-facing read help. `help()` pulls the guide
into model context; `help(topic="read")` pulls only the relevant section.

`help(topic="reference")` opens the small topical index; request a document such
as `reference.pat` for lifecycle guidance. `help.reference.<topic>` in this same
YAML is its editable source. Startup versions changed documents in captured
project knowledge under `harness/reference/<topic>`, preserving previous heads.
For example, `query { knowledge(key:"harness/reference/pat",limit:1) { key body } }`
reads one through the existing bounded GraphQL interface. Reference retrieval is
independent of executable tool scope; tool help labels actual availability and
shows a current schema only when available. These references are separate from
`plan`'s current question-led authoring workflow and grant no execution authority.

Keep every key and each nonempty string value. Quote standalone yes/no/on/off
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
A missing file, unknown/missing key or invalid template fails admission; there is
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
