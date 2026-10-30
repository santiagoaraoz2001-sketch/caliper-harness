# Models & Servers

Use `/manage` to add and manage your server connections and models. Use `/status` for runtime activity and usage. Coding and Judge are roles assigned to a model connection, not server names.

In `/manage`, use `n` to add a server, `+` to add a model, `e` to edit, Backspace to remove, and `t` to test. Forms use Tab between fields, Ctrl+S to save, Ctrl+T to test, F2 for advanced options, and Escape to cancel.

## Set up with your preferred LLM

As a second option, open `/manage assisted` or press `g` in `/manage`. Press `c` to copy the complete setup prompt and paste it into your preferred LLM. It includes this computer's actual configuration paths, the connection format, native judge guidance and validation steps. A coding assistant with local access can inspect your existing servers and checkpoints; a chat-only assistant will need you to provide paths and command output. Caliper makes no model request and changes no settings when you open or copy this prompt. Review the proposed wiring before applying it, then restart Caliper to reload saved connections.

Caliper does not scan your computer or open ports looking for models or servers. Only saved connections and services Caliper started are listed. Existing selected model/server settings remain available. Old `serve.model_dirs` settings remain readable but no longer populate the list.

## Connect a server

Open `/manage` and choose Add server. Select a preset such as MLX, Ollama or llama.cpp, or Custom for another compatible server. Give the connection a name and enter its actual URL. An already-running server needs no launch command. For a local service you want Caliper to start, choose its preset/executable; custom arguments are advanced settings.

Test checks the selected endpoint. A responding catalog does not prove a model is loaded or that inference works. Saved offline connections remain visible so you can start or correct them.

## Add a model

Select the saved server and choose Add model. Enter the exact ID that server accepts, or the exact local checkpoint folder/GGUF file used by your local launcher. A server's model catalog can supply IDs; no filesystem scan is required. For split safetensors, select the checkpoint folder containing its index; for split GGUF, select the first shard. The server must support the checkpoint's architecture and format.

Choose Use for Coding or Use for Judge to assign the saved model. Decision-trained models are recommended for judging; a connection test does not qualify judgment quality. Settings can disable the judge, in which case required human decisions use the existing approval flow.

## Start, load and stop

Start uses the saved server launch command. Load and Unload manage memory residency through the supported runtime or Caliper-owned service. These actions do not download model weights. Servers managed by another application remain external; unsupported lifecycle actions must be performed in that application. Only processes Caliper owns may be stopped by Caliper.

Edit or Remove changes the saved connection, not the checkpoint files. Reusable connections are stored in the OS user configuration directory at `caliper/connections.yaml`. Explicit project connection settings continue to override machine defaults.

## Status

`/status` reports current conversation activity, reported generated tokens, recorded coding requests, tool calls, model identities and the latest observed connection state. Request throughput includes prefill and transport, and excludes time spent executing tools. Counts cover retained recorded coding runs; judge requests, title generation and manual compaction are excluded. Missing usage stays unknown or partial; resumed history without measurements is labelled accordingly. Use `/manage` for configuration and lifecycle actions.

## Continuing at output limits

For `rulith` requests advertising structured tools, Caliper renders the original conversation
through the existing `/apply-template` API and streams native `/completion`
output. A clean output-limit receipt continues the same rendered prompt plus
the exact unfinished UTF-8 prefix. It does not ask the user to continue, inject
a smaller-work instruction, or execute incomplete calls. No server modification
is needed. The server retokenizes the retained bytes; this is not a guarantee
of identical sampled token IDs or RNG state. Cancellation and broken transfers
remain separate outcomes and never authorize partial effects.

The current native decoder supports Qwen XML and JSON `tool_call` frames, with
ordinary prose and `think` reasoning boundaries. Fully balanced, unescaped XML
control sequences inside argument values are inherently ambiguous in that wire
format; isolated marker strings and JSON-escaped strings are covered by tests.
Other native formats require their own qualification. Plain Chat, Judge,
compaction, empty-tool requests and explicit text compatibility retain their
existing parsed chat transport. The MLX worker retains its existing token-prefix
contract; generic OpenAI/Ollama compatibility alone does not prove this same
continuation capability. A full context window is handled separately from an
ordinary per-response output boundary.


## Test design and execution

`/tests` manages project suites and executes their commands through the existing
Bash authorization and receipt owners. It does not launch another coding model
or add a test reviewer. Its design/refine action prepares a visible Chat draft;
the selected coding model runs only when that draft is submitted. The project
`testing.guidance` switch controls the draft's optional knowledge references,
not required verification or model selection.

Test prerequisites belong to the actual command environment. A reachable coding
server does not establish that Go, Node, a browser or another test dependency is
available. Structured reports support `go-test-json`, declared-stage
`caliper-test-report-v1`, and `caliper-test-report-v2` with per-stage prerequisites
and expected observations. The host checks exact declared path/executable
presence, never runtime health or manual confirmation. Missing or unknown stage
prerequisites prevent qualification while the configured check still runs;
optional suite-level prerequisites retain their preflight behavior.
Commands without a report format retain unknown coverage even when they exit
zero. See the [report contract](https://github.com/santiagoaraoz2001-sketch/caliper-harness/blob/v0.2.4/docs/testing-report-contract.md) for configuration,
evidence limits and the separation between standalone results and Chat/PAT checks.

## Ollama D1 decision model

Ollama D1 uses its native `/v1/systemone` choice endpoint. Select it explicitly;
the empty protocol preserves existing chat judges. Pull `d1:3b` with Ollama,
then configure the decision connection independently of the coding model:

```yaml
decision:
  disabled: false
  runtime: ollama
  protocol: system_one
  server: http://127.0.0.1:11434
  model: d1:3b
  device: auto
  timeout_seconds: 180
```

Remove native-sidecar `path`, `revision`, and `trust_remote_code` overrides.
Ollama owns loading and device placement. Caliper validates the selected model,
choice, and normalized option probabilities; confidence is the chosen option's
probability, not Ollama's concentration score. The complete request is capped
at 64 KiB and any smaller caller byte budget. Token/context admission belongs
to Ollama; `decision.max_input_tokens` is not enforced by this endpoint.
Catalog availability does not prove successful inference or GPU residency.
Rulith can continue serving the coding model on its separate endpoint.

### Domain guidance token admission

Domain articles start near 1,000 tokens and may expand to 2,000 native-counted tokens per enriched retrieval, including preferences and navigation. Rulith uses its native `/tokenize` without generation. MLX/Ollama/generic adapters explicitly lack that capability and use a disclosed conservative UTF-8-byte estimate capped at 2,000 bytes for the complete domain envelope, including links and the accounting-basis label. This keeps compact guidance available without claiming exact token counts or universal tokenizer guarantees. Unknown runtime, missing counter, malformed native response, timeout and cancellation remain failures, never fallback triggers. Ordinary project facts and PAT retain their own limits; exact counting and native benefit on other backends remain separate gates. See [domain guidance](https://github.com/santiagoaraoz2001-sketch/caliper-harness/blob/v0.2.4/docs/domain-guidance.md) for scope, selected-body delivery and multi-hop authoring.
