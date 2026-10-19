# Servers and models

This guide describes the server and model controls. See [PORTABILITY.md](PORTABILITY.md) for platform qualification. Existing installed application folders do not acquire source changes automatically.

## Choose the worker or judge

Open `/status` and refresh with `r`. Server rows show endpoint health and the availability of their launch executable. Model rows distinguish server catalog entries from confirmed residency. Load operations remain cancellable and have no fixed two-minute deadline. Select a catalog model and press `u` for the worker. Press `j` to stage the same catalog model as the judge, or choose a native Laya/System One candidate under its sidecar. `a` applies the selection; Settings `s` saves. Selections affect the next run. Decision-trained models are recommended.

Installed `gufo` and `llama-server` executables also appear as explicit launch choices on the configured HTTP loopback endpoint, using its existing port. Select an existing local model candidate and press `l`; discovery never starts these servers or downloads a model. Custom prefixes or other endpoint layouts use an explicit recipe.

Use `s` to start a configured server, `l` to load a supported local model, and `x` to unload a model through a supported native operation or release a server process this harness owns. Generic OpenAI-compatible catalog listings alone do not prove that weights are resident. External servers without a lifecycle API must be managed through their own application. Loading never means downloading.

## Add a custom server

Open `/setup`. Choose Worker, then Judge, optionally enter a project check, and review/save. Press `a` on either model step to add a server and model. Enter a name, server type and URL, then a served model ID or checkpoint folder. Leave Model ID blank to use a checkpoint path; an explicit model ID remains distinct from that path. Tab moves between fields, Ctrl+T tests the server catalog, and Ctrl+S stages the connection. Catalog success does not prove inference or judgment quality.

Known local MLX and Ollama launchers use the selected URL and port without custom arguments. Selecting `gufo` or `llama.cpp` uses its installed launcher when found; those choices use the OpenAI wire protocol. Installed Gufo/llama-server choices are also available in Status. Advanced custom launch commands accept a JSON argument array and optional working directory. Caliper does not install dependencies or download weights. Custom authenticated endpoints requiring additional headers are not supported by this form.

Setup saves reusable connections under the OS user configuration directory (`caliper/connections.yaml`); the project check and graph/session paths remain in the project configuration. Setup-managed project files omit `model`, `decision` and `serve`; later Status/Settings changes remain reusable. Existing project files with explicit connection sections remain project overrides. Explicit project YAML wins over machine defaults.

Safetensors folders are checked against their shard index. Split GGUF folders are grouped by the declared filename set; every shard must exist with a GGUF header. Header/file completeness is not tensor-integrity or runtime-compatibility proof. No shards are merged or copied.

Alternatively add an entry to `serve.recipes` in the project YAML. `argv` is an argument list: paths with spaces stay a single element. Omit `argv` for an endpoint that you manage externally. `runtime` describes the worker wire protocol, not the executable brand.

```yaml
serve:
  model_dirs: ["/absolute/path/to/existing/models"]
  recipes:
    - name: my-local-server
      kind: openai
      runtime: openai
      url: http://127.0.0.1:8080/v1
      argv: ["/absolute/path/to/server", "--model", "{model}", "--port", "8080"]
      dir: /absolute/server/working-directory
```

Replace the executable and arguments with your installed server's actual syntax. `{model}` is replaced literally in each argument; the harness does not build a shell command. Use an unused port and make the recipe URL match the server arguments. Discovery never starts processes, downloads assets or changes another application's model.

For upstream Gufo, the documented command is:

```yaml
argv: ["/absolute/path/to/gufo", "serve", "--host", "127.0.0.1", "--port", "8080", "llm", "--model", "{model}"]
```

[Gufo's server documentation](https://github.com/gufo-org/gufo/blob/main/docs/SERVER.md) identifies Linux x86-64 Strix Halo as its supported production platform. This recipe is not a claim that Gufo runs on this Mac. No Gufo-specific unload endpoint is assumed.

## Decision judges

Native `laya` and `system_one` use their typed decision protocol. Ordinary models served through `openai`, `mlx`, `ollama` or `rulith` use the existing chat transport and must return a valid picked option, matching option text and explicit confidence. Missing/malformed judgments fail; no confidence is invented. Chat confidence is labelled self-reported and is not a native decision score. A connection test does not qualify judgment quality. Chat judges use the shared configured sampling/output parameters and the server’s context admission; `decision.max_input_tokens` is enforced by native sidecars, not claimed as an exact tokenizer count for arbitrary chat servers.

For native Laya/System One judges, `/status` lists local safetensors candidates under the configured sidecar. Enter or `j` stages the exact local model path and snapshot revision; `a` applies through the existing ownership rules, and Settings `s` saves. External services show pending external activation. Candidate metadata does not establish runtime compatibility. The current device and explicit `trust_remote_code` permission are retained and disclosed. Native models retain their `laya`/`system_one` selection and typed sidecar protocol.

The Decisions view and CLI summaries label each recorded confidence source. Historical verdicts without provenance remain unspecified; pooled scores across sources are not comparable.
