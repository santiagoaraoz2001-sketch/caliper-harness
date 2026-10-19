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
- **Your style, on the record.** A per-workspace styleguide lives in the
  state graph (knowledge key `styleguide`), evolving with rules you state.
  Turn on `decision.style_enforcement` and a style judge can send finished
  code back once for adjustment; leave it off and the document is still
  there for the model to consult.
- **Keyboard-driven TUI.** Tabs, plan navigation, live tool activity, and
  model/settings management without leaving the terminal — plus a headless
  `run` mode for scripts and evaluation.

## Quick start

### Managed bundle

Extract the complete application folder to a permanent location. On macOS or
Linux, install its terminal command once:

```sh
cd /path/to/Caliper
sh ./install.sh
```

Open a new terminal, enter your project directory, and run `caliper`. The command
opens the TUI; `caliper help` lists CLI commands. Keep the application folder
intact. Run its installer again after moving it or selecting a new version.
The installer needs no administrator rights and does not download runtimes or
models. See [FIRST-USE.md](FIRST-USE.md) for macOS prerequisites.

On Windows, run `powershell -ExecutionPolicy Bypass -File .\install-windows.ps1`
inside the extracted folder, reopen the terminal application, then run `caliper`.
Both Windows packaging modes include the installer. Windows and Linux native
execution still require qualification; source portability is not runtime proof.
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
Private runtime acquisition is a separate explicit command documented in
[PORTABILITY.md](PORTABILITY.md); neither Windows packaging mode is natively qualified.

On first run with an unconfigured worker, Setup opens: choose a Worker and
Judge, optionally enter your project check (for example `npm test`), then review
and save. Press `a` on either model page to add an existing server or local
checkpoint. Connections can be reused across projects; checks stay local.
`/setup` reopens it; `/status` inspects or loads models; `/help` lists commands.
See [Server and model connections](SERVER-MODELS.md) for details.

## Commands

| Command | What it does |
|---|---|
| `caliper-harness` | Open the interactive TUI |
| `caliper-harness run --config ... --prompt '...'` | Headless coding task; completion requires a declared check |
| `caliper-harness chat` | One model prompt, no agent tools |
| `caliper-harness sessions [id]` | List past runs / show one run |
| `caliper-harness judgments <id>` | Summarize one run's decision judgments |
| `caliper-harness graph --query '...'` | Run one in-process GraphQL operation |
| `caliper-harness list plans\|approaches` | Browse planning state |
| `caliper-harness launch <approach-id>` | Fan out one approach |
| `caliper-harness config` | Print the effective YAML configuration |
| `caliper-harness hw` | Hardware profile and limits |

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

- **Worker model** — any OpenAI-compatible local server. The Python worker
  side (`python/worker/`) owns native tool-call parsing for framed
  tool-call formats, including literal continuation of unfinished responses.
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

```sh
caliper-harness graph --query 'query { knowledge(key:"styleguide") { body } }'
```

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

Caliper is an early development release. Local-model runs have exercised
planning, task claims, edits, paired checks and independent review. The latest
completed benchmark qualified both Tasks but left their Approach and Plan open;
v0.1.0 corrected that handoff and a fresh benchmark is still in progress.
D1 can miss repetitive reasoning. Passing checks and reviewer agreement do not
guarantee correctness.

The downloadable application folder targets Apple Silicon Macs. Windows and
Linux share the source, but native builds and runtime behavior still need
validation on those systems. See [PORTABILITY.md](PORTABILITY.md).

Development plans, benchmark traces and scratch notes stay local. Release
changes and qualification notes are published on [GitHub Releases](https://github.com/santiagoaraoz2001-sketch/caliper-harness/releases).

## Testing

```sh
make test    # go test ./... — 31 packages
make vet     # go vet ./...
```

Python sidecars carry their own suites under `python/worker/` and
`python/decision/` (run with the managed runtimes). The example
configuration, prompt catalog, and GraphQL schema are all test-pinned
against drift; if you change a default, a prompt, or the schema, the
corresponding test tells you exactly what else to update. Regenerate the
GraphQL code with `go run github.com/99designs/gqlgen generate` (see
[gqlgen.yml](gqlgen.yml)).

## Repository layout

```
cmd/specific-harness/   CLI entrypoint (binary: caliper-harness)
internal/
  agent/                executor loop, supervision, context, style judge
  graph/                embedded GraphQL state API (generated in generated/)
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
