<h1 align="center">Caliper Harness</h1>

```
        /|           _ 
       / |          |  \  
      /  |          |   \
     /   |__________|____\________________________________
    /    | | | | | | | | | | | | | | | | | | | | | | |    | 
   /_____|_________[_______]______________________________|
                    |     |
                    |WWWWW|
```
               
<p align="center">
  <strong>A terminal coding workspace built for small local models.</strong><br>
  By <a href="https://github.com/santiagoaraoz2001-sketch">Specific Labs</a>
</p>

<p align="center">
  <img alt="Status: developer preview" src="https://img.shields.io/badge/status-developer%20preview-orange">
  <img alt="Go" src="https://img.shields.io/badge/go-1.25-00ADD8?logo=go&logoColor=white">
  <img alt="Local models first" src="https://img.shields.io/badge/models-local--first-green">
  <a href="LICENSE"><img alt="License: MIT" src="https://img.shields.io/badge/license-MIT-blue"></a>
</p>

Caliper Harness lets a small local language model do real coding work in your
terminal — inspect a codebase, plan a change, edit files, run your project's
own checks, and report what actually happened — while you watch every model
reply, tool call, and judgment as it streams past. It is built around a simple
conviction: **with small models, the harness is the product.** Model output is
unreliable in specific, repeatable ways, so the harness owns planning state,
tool boundaries, verification, and an evidence record that never lets a
claimed result outrun an observed one.


## Why Caliper?

- **Local-first, small-model native.** Local OpenAI-compatible model servers
  are first-class citizens, including native tool-calling transports for
  models whose structured output is… creative. Malformed calls get
  repairable refusals with the current legal call, not dead runs.
- **Plans the model can't fake.** Work is organized as Plan → Approach → Task
  (PAT) in a GraphQL state graph. A task only counts when its own declared
  check passes and an independent review agrees; prose claims settle nothing.
- **A second, smaller brain supervises.** An optional decision sidecar
  (typed-choice judge) watches progress at specific triggers — stuck loops,
  repeated failures, phase changes — and advises. Advisory means advisory:
  if the judge is slow or down, work continues and the outage is recorded.
- **Evidence, not vibes.** Every run writes a durable session record: model
  replies, tool receipts, judgments with confidence, file effects, and the
  final outcome. `caliper-harness judgments <session>` replays the verdicts.
- **Your style, shared with every role.** `/style add <rule>` saves a project
  convention. Coding agents and independent reviewers receive the same
  version from the start of a run, including when D1 is off. Changes apply
  to the next run; earlier versions remain in the state graph. Optional
  `decision.style_enforcement` adds final-answer advice, not a source audit.
- **Keyboard-driven TUI.** Tabs, plan navigation, live tool activity, and
  model/settings management without leaving the terminal — plus a headless
  `run` mode for scripts and evaluation.

## Quick start

### Install with npm

With Node.js 20 or newer:

```sh
npm install -g caliper-harness
cd /path/to/your/project
caliper
```

The package installs the complete application for your operating system, including
its private runtimes and ripgrep. Model weights and project build tools remain
separate. Linux includes the CUDA runtime upfront, so its download is several GB.
The supported packages are macOS arm64, Windows amd64, and Linux amd64/arm64.

Plain `npm install caliper-harness` also registers the command for your user account.
Reopen your terminal if it cannot find `caliper`. Local npm installation creates
`node_modules` and npm package files where you run it; `-g` avoids those project files.

On Windows, launching also checks the Microsoft Visual C++ runtime (the x64
redistributable, version 14.44.35211.0 or newer) and offers Microsoft's setup when
it is missing or outdated. Recent npm versions ask for approval before running a
package's install script; if npm reports that `caliper-harness` is not covered by
`allowScripts`, run `npm install-scripts approve caliper-harness` and then
`npm rebuild caliper-harness` in the same directory to finish installing.

Caliper offers available updates at startup. Accept the offer or use `/update` to
prepare the new version; close and reopen Caliper to use it. Active runs keep their
current executable. `npm install -g caliper-harness@latest` also updates the command.
Previous application folders are retained, along with your configuration, graph,
sessions and custom prompts. Older executables may not understand settings added
by a newer release; retaining a folder does not guarantee arbitrary downgrades.

New workspaces store application state in Caliper's OS user-data directory, keyed
by the project path. Existing project-local state and explicit configuration paths
remain in use. Launching from a directory does not add application files to it.

Settings saved in an ordinary workspace become personal defaults for every project.
Caliper keeps these in `settings.yaml` beside the reusable `connections.yaml` in
your OS configuration directory. Project checks and graph/session storage stay
with their workspace. A project `caliper-harness.yaml`, legacy
`specific-harness.yaml`, or explicit `--config` file overrides personal defaults;
Settings shows that project scope before saving. Older automatic workspace
snapshots are backed up when Settings first replaces their saved personal values.
No project files are created by an ordinary Settings save.

### Managed bundle

Extract the complete application folder to a permanent location. On macOS or
Linux, install its terminal command once:

```sh
cd /path/to/Caliper
sh ./install.sh
```

Open a new terminal, enter your project directory, and run `caliper`. The command
opens the TUI; `caliper help` lists CLI commands. Keep the application folder
intact. Run its installer again after moving it. Updates selected inside Caliper take effect on the next launch.
The installer needs no administrator rights and does not download runtimes or
models. See [FIRST-USE.md](FIRST-USE.md) for macOS prerequisites.

On Windows, run `powershell -ExecutionPolicy Bypass -File .\install-windows.ps1`
inside the extracted folder, reopen the terminal application, then run `caliper`.
Both Windows packaging modes include the installer. Native release checks cover
installation, selected Go packages and terminal workflows on Windows and Linux;
they do not qualify model inference or every GPU/driver combination.
See [FIRST-USE-WINDOWS.md](FIRST-USE-WINDOWS.md) and [PORTABILITY.md](PORTABILITY.md).

### Build from source

Requirements: Go 1.25 with CGO, and `uv` for the managed Python runtimes.

```sh
make build
./caliper-harness          # opens the TUI
./caliper-harness help     # every command
```

The Go module is `specific-harness` for compatibility; the public executable
is `caliper-harness`. `make bundle-macos OUT=...` prepares a fresh
self-contained app folder. On Windows, `make bundle-windows OUT=...` builds
an offline developer folder using installed Go, mingw-w64 GCC, Bash and ripgrep.
For the managed Windows folder with private runtimes, run
`powershell -ExecutionPolicy Bypass -File scripts/bundle-windows-managed.ps1 -Out NEW_FOLDER -AllowDownloads`
on Windows with the same build tools and `uv`. This mode is used by native release checks;
the offline developer mode has a separate environment and acceptance scope.

On first run with no coding model configured, Setup opens. Add your server
and model, choose the Coding and Judge roles, optionally enter your project
check (for example `npm test`), then review and save. Connections are reusable
across projects; checks stay local. `/manage` opens Models & Servers to add,
edit, test, select, load or unload saved connections. `/status` shows runtime
activity and measured usage. Caliper does not scan your computer for models
or servers. `/setup` reopens setup; `/help` lists commands.
For help wiring existing installations with your preferred LLM, use
`/manage assisted` and press `c` to copy the setup prompt.
In `/settings`, switch the decision model off to use human approval for PAT
readiness and Risky actions. Checks and independent review remain required;
automatic progress advice and tool-call recovery are disabled.
See [Server and model connections](SERVER-MODELS.md) for details.

## Commands

| Command | What it does |
|---|---|
| `caliper-harness` | Open the interactive TUI |
| `caliper-harness run --config ... --prompt '...'` | Headless task; changed project work requires a declared check |
| `caliper-harness chat` | One model prompt, no agent tools |
| `caliper-harness sessions [id]` | List past runs / show one run |
| `caliper-harness judgments <id>` | Summarize one run's decision judgments |
| `caliper-harness graph --query '...'` | Run one in-process GraphQL operation |
| `caliper-harness list plans` or `caliper-harness list approaches <plan-id>` | Browse planning state |
| `caliper-harness launch <approach-id>` | Fan out one approach |
| `caliper-harness config` | Print the effective YAML configuration |
| `caliper-harness hw` | Hardware profile and limits |

Plans created through the owner CLI graph route need an explicit project binding
before launch. Run the following from the intended project directory, using the
actual ID returned by `createPlan` and the same `--config` for graph and launch:

```sh
caliper-harness graph --query 'mutation { bindPlanProject(id:"ACTUAL_PLAN_ID") { id projectRoot } }'
```

Check the returned `projectRoot` before preparing and approving an approach, then
run `caliper-harness launch ACTUAL_READY_APPROACH_ID` from that project. Binding
does not approve work or make an approach ready. A plan already bound elsewhere
cannot be rebound through this operation; use its original project. Missing,
unbound or mismatched ownership refuses before dispatch.

`--db` overrides the database only for `graph`; `launch` uses the database in
its effective configuration. If you use a graph override, it must name that same
database.

## How it works

```
┌────────────┐   native tool calls   ┌──────────────────┐
│  Worker    │ ────────────────────▶ │  Agent loop      │
│  model     │ ◀──────────────────── │  (Go)            │
│  (HTTP)    │   tools, refusals,    └──────┬───────────┘
└────────────┘   current questions          │ tool effects + receipts
                                            ▼
┌────────────┐   judgments (typed   ┌──────────────────┐
│  Decision  │ ────────────────────▶ │  GraphQL state   │
│  sidecar   │   choices, advisory)  │  Plans · Tasks · │
└────────────┘                       │  Knowledge ·     │
                                     │  Outcomes        │
                                     └──────────────────┘
```

- **Worker model** — a configured local server. Caliper owns continuation across
  output limits. The MLX adapter retains its native token prefix. The Rulith
  adapter uses the existing template/completion APIs for structured tool requests, preserves the rendered
  prompt and unfinished UTF-8 output, and resumes without a new user message.
  Rulith retokenizes that byte prefix; original token IDs/RNG state are not
  replayed. Completed native calls still pass the same registry admission before
  any effect. No Rulith server patch or extra inference service is required.
- **Agent loop** — one executor. It validates every call against the
  *current* question's schema, tolerates known compatibility spellings of
  arguments, and refuses the rest with the exact legal call so the model can
  self-correct. Nothing executes from an ambiguous payload.
- **PAT state graph** — Plans, Approaches, and Tasks with readiness gates,
  claims, per-task acceptance commands, and independent review. State lives
  in an embedded Kuzu store behind a schema-first GraphQL API
  (`schema/v1.graphqls`); the model reads and writes it through the same API
  you can query with `caliper-harness graph`.
  `tools.graphql.max_result_bytes` defaults to 1 MiB (1,048,576 bytes), shared
  by result serialization and source admission. Source admission conservatively
  estimates four bytes per character, so selected text may hit the guard before
  its serialized response reaches 1 MiB. Existing YAML overrides remain in effect.
- **Decision sidecar** (`python/decision/`) — a small typed-choice judge
  (for example LiquidAI/d1-3B on MPS). It classifies progress at triggers,
  judges plan coherence at readiness, and can gate risky tool calls. Every
  verdict is logged with its confidence; low-confidence and unavailable
  judgments degrade to silence on advisory paths, never to invented success.
- **Context management** — measured token calibration from the server's own
  usage reports, age-based pruning of consumed tool outputs, and
  model-written compaction at 90% of the context window or on `/compact`.
  A consistent summary needs decision confidence above 0.5; an uncertain or
  rejected summary leaves context intact and execution continues. Old
  `compaction_absolute_tokens` settings are ignored. The full retained context governs admission;
  estimates are disclosed, never silently trusted.

Domain guidance uses compact sections and explicit section → topic → worked-example
links. New articles and sections start with a 1,000-token limit, including their
locator and direct navigation. A later version of the same section anchor may
expand to 2,000 tokens for preferences and enrichment; newly added or renamed
anchors start at 1,000. Each section is checked before a domain append commits,
even for ID-only output. A multi-section article is bounded per section, and the
complete selected domain response stays within 2,000 tokens. This applies to bundled
`caliper/standards` / `caliper/guides` and opt-in project articles under
`project/guides/<domain>/<topic>`. Current project identity, testing strategy,
ordinary task notes and PAT state retain their separate source/context limits.
Planning's optional `guidance` selection pins a returned `knowledge:key#section`
and supplies that body before the next decision; selected domain items must fit
together within 2,000 units. Supplied pins replace generic entry guidance, while
older over-budget selections retain fitting bodies and explicit omitted-section
locators. Descendant bodies stay lazy.

Rulith counts the complete domain envelope with its native tokenizer. MLX, Ollama
and generic OpenAI-compatible adapters explicitly lack that count-only capability
and use the existing conservative UTF-8-byte estimate at the same initial/enriched
1,000/2,000-unit limits,
including the disclosed basis label and links. This is not a measured token count
or universal tokenizer guarantee. Native tokenizer failures, cancellation, unknown
runtime and missing counters remain explicit errors; they never trigger fallback.
Ordinary graph facts and PAT retain their own limits. Exact counting and performance
across backends remain separate gates. See [domain guidance](https://github.com/santiagoaraoz2001-sketch/caliper-harness/blob/v0.2.4/docs/domain-guidance.md).

## Configuration

Copy [caliper-harness.yaml.example](caliper-harness.yaml.example) to
`caliper-harness.yaml` in your project. Every tunable is documented inline
and the file is drift-pinned by test to parse to the exact built-in defaults,
so deleting a line always restores default behavior. Existing
`specific-harness.yaml` files still work; `--config PATH` always wins.

```yaml
model:
  server: http://127.0.0.1:11434/v1   # your local model server
  name: your-model
coding:
  check_command: npm test            # the exact check a task must pass
decision:
  server: http://127.0.0.1:8090      # decision sidecar (optional)
  style_enforcement: false           # post-task style judge (optional)
  max_input_tokens: 15999            # complete native input: context + instructions + choices
```

The decision sidecar counts complete inputs with its native tokenizer before inference.
Progress advice can reduce optional evidence to fit this budget; the raw history and
worker context remain unchanged. Expired requests do not start inference, and busy
judgments are reported without queueing. An already-running native GPU forward
cannot be safely interrupted; a result arriving after its deadline is discarded.

Project rules can live in two model-readable places: `behavior.md` for how
the agent should conduct itself, and the styleguide document in the state
graph for how code should be written:

```text
/style add Prefer pure functions for game state.
/style
/style set Use explicit names and keep state updates pure.
/style clear
```

`add` appends a rule; `set` replaces the document; `clear` saves an empty
version. Each write preserves history and reports its project and version.
The guide applies to the next run and is shared by coding and review. It
does not override your request, permissions, or required checks.

Use `/remember <text>` to save a project note and `/recall <query>` to find
project notes, model-authored knowledge and earlier shared memories. Search
also matches saved note keys and file paths. `/remember --global <text>`
explicitly shares a new note across projects; existing global memories remain
available. These commands use Caliper's state store and add no project files.

## Design and run tests

Open `/tests` to manage optional project test suites alongside the configured
coding check. Use `n` to add or `e` to edit a suite as JSON, Space to enable or
disable an optional suite, `r` to run the selected suite, `R` to run enabled
optional suites, `x` to cancel, Enter to inspect result history, and `f` to refresh
evidence. Use PgUp/PgDn to scroll details and older results. The configured coding
check remains required and cannot be disabled here. Disabling an optional suite
keeps its history and records a reason.

Use `d` to prepare a test design/refinement draft in Chat. Review and submit it
normally; opening Tests or preparing the draft makes no model request. With
testing guidance on, the draft asks for relevant sections of
`caliper/standards/tests` and the project's versioned `project/testing/strategy`.
The guide covers independent expected outcomes, useful negative controls,
fixtures, prerequisites and honest evidence. `g` changes the project guidance
setting for future drafts. Turning it off neither removes explicit knowledge
lookup nor weakens required verification.

Suite settings are saved in the existing project configuration:

```yaml
testing:
  guidance: true
  suites:
    - id: unit
      name: Unit behavior
      purpose: Check the package's public behavior
      expected: Valid inputs work and invalid inputs are rejected
      command: go test -json -count=1 ./internal/example
      enabled: true
      prerequisites:
        - kind: executable
          value: go
      report_format: go-test-json
      expected_tests:
        - example.org/project/internal/example/TestBehavior
```

Replace the example command and expected test identifier with your project's
actual values. Optional fields include `location`, `exclusions`, and paired
`spec_key`/`spec_version` references to a current project knowledge version.
Prerequisites can name a workspace-relative path or executable. A declared
manual prerequisite remains blocked until it is replaced with verifiable setup;
the runner does not assume it was satisfied.

Runs freeze their selected suite, command, configuration and spec references.
Output and results use existing session archives; source, command or spec
changes can make earlier evidence stale. Saving a configuration file inside the
observed workspace can also mark evidence stale, including an advice-only edit.
The `go-test-json` adapter reads current uncached Go test events. The
framework-neutral `caliper-test-report-v1` adapter validates declared stages
against the current execution ID. `caliper-test-report-v2` also declares each
stage's prerequisites and expected observations, retaining actual observations
beside host-recorded prerequisite presence. See the
[report contract](https://github.com/santiagoaraoz2001-sketch/caliper-harness/blob/v0.2.4/docs/testing-report-contract.md) for configuration and producer
examples. These formats distinguish executed, failed and
skipped tests. Missing required tests or stages, malformed/truncated reports and
zero executed tests do not establish declared coverage. Without a report format,
exit zero means **process passed; coverage unknown**. No report establishes that
assertions are meaningful without inspecting their requirements and oracles.

These are standalone test results. Even running the configured command here
does not qualify an active Chat task or PAT attempt; those retain their own
required checks and receipts. Test commands use the existing Bash authorization
and process cleanup. A suite can modify its workspace, so review its command as
you would any other command.

## Record what you notice

While working on an Approach, the model can save weak spots, brittle code,
potential issues and opportunities through the existing GraphQL tool:

```graphql
mutation {
  noticed(approachId: "<actual approach ID>",
          body: "Cache entries do not expire. Long sessions may retain unused data; growth has not been measured.",
          filePath: "src/cache.go") { id key }
}
```

`notices(approachId: "<actual approach ID>") { id body filePath }` retrieves
that Approach's notes. They are ordinary project knowledge: saved across
sessions, with author and timestamp, and editable through existing versioned
knowledge operations. They do not create tasks or change verification status.
The model is instructed to record useful observations briefly and continue
its assigned work. Delegated workers report observations to the orchestrator, which records
them with their attribution; reviewers keep their existing read-only verdict flow.
`help(topic="schema.graphql.noticed")` provides the call
template and examples.

## Design principles

1. **Never fabricate success.** No silent fallbacks, simulated results, or
   green badges over unverified work — in code paths or in status reports.
   A check that cannot fail is not evidence.
2. **Refuse repairably.** A malformed model call gets the current legal call
   and another turn. Ambiguous bytes (duplicate keys, trailing data) are the
   exception: they have no settled meaning and fail loudly.
3. **Advisory supervision.** Judges advise at triggers; the executor keeps
   working when they are slow, uncertain, or absent. Only evidence and
   declared checks settle work.
4. **Fix the boundary, not the symptom.** When a failure mode is found, the
   fix lands at the shared mechanism, with a regression test proven to fail
   on the old code first.

## Release status

Caliper is an early development release. Mac GPU runs exercise planning,
task claims, edits, paired checks, independent review and handoff. Results depend
on the model and task; D1 can miss repetitive reasoning, and passing checks plus
reviewer agreement do not guarantee correctness.

The published v0.1.6 packages passed native build, installation, runtime-import
and terminal UI checks on macOS arm64, Windows x64 and Linux x64/ARM64.
Those checks do not establish Windows/Linux model inference. Each release's
notes identify the exact source, native package checks and upgrade tests that
passed before publication. See [PORTABILITY.md](PORTABILITY.md).

Development plans, benchmark traces and scratch notes stay local. Release
changes and qualification notes are published on [GitHub Releases](https://github.com/santiagoaraoz2001-sketch/caliper-harness/releases).

## Testing

```sh
make test    # go test ./...
make vet     # go vet ./...
```

Python sidecars carry their own suites under `python/worker/` and
`python/decision/` (run with the managed runtimes). The example
configuration, prompt catalog, and GraphQL schema are all test-pinned
against drift; if you change a default, a prompt, or the schema, the
corresponding test tells you exactly what else to update. GraphQL execution and
help load the same embedded `schema/v1.graphqls`; no code generation is required.
Root resolver interfaces and model structs remain typed, while the small
`internal/graph/execution` adapter uses gqlgen validation and execution contexts.

## Repository layout

```
cmd/specific-harness/   CLI entrypoint (binary: caliper-harness)
internal/
  agent/                executor loop, supervision, context, style judge
  graph/                embedded GraphQL API with schema-driven execution
  launch/ run/          run assembly, readiness, task lifecycle
  llm/                  model transports (native + text protocols)
  memory/               knowledge documents, recall, styleguide
  plan/                 Plan/Approach/Task semantics
  session/              durable session records and continuations
  store/                embedded Kuzu store
  tool/ tools/          tool contracts, validation, implementations
  tui/                  terminal interface
python/
  worker/               native response parsing + literal continuation
  decision/             typed-choice decision sidecar
schema/v1.graphqls      the state API contract
scripts/                bundle + runtime preparation
```

## License

MIT — see [LICENSE](LICENSE).


### Server and platform development

Server/model controls and custom recipes are described in [SERVER-MODELS.md](SERVER-MODELS.md). The `MacOS`, `Windows` and `Linux` branches carry the same shared source. Target-platform qualification is documented in [PORTABILITY.md](PORTABILITY.md).

The Tests structured adapter accepts current direct `go test -json -count=1` output. It rejects events outside the command interval and incomplete/skipped package or test results. Other commands report process status with coverage unknown; reported execution counts are not proof that tests correctly capture the requirement.
