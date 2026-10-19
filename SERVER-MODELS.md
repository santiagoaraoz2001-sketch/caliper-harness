# Servers and models

This guide describes the server and model controls. See [PORTABILITY.md](PORTABILITY.md) for platform qualification. Existing installed application folders do not acquire source changes automatically.

## Choose the worker or judge

Open `/status` and refresh with `r`. Server rows show endpoint health and the availability of their launch executable. Model rows distinguish server catalog entries from confirmed residency. Select a catalog model and press `u` for the worker. Judges use native Laya/System One candidates under the sidecar; `j` stages those candidates only. Selections apply to the next run and save to the selected configuration file.

Installed `gufo` and `llama-server` executables also appear as explicit launch choices on the configured HTTP loopback endpoint, using its existing port. Select an existing local model candidate and press `l`; discovery never starts these servers or downloads a model. Custom prefixes or other endpoint layouts use an explicit recipe.

Use `s` to start a configured server, `l` to load a supported local model, and `x` to unload a model through a supported native operation or release a server process this harness owns. Generic OpenAI-compatible catalog listings alone do not prove that weights are resident. External servers without a lifecycle API must be managed through their own application. Loading never means downloading.

## Add a custom server

Add an entry to `serve.recipes` in the project YAML, then reopen the harness. `argv` is an argument list: paths with spaces stay a single element. Omit `argv` for an endpoint that you manage externally. `runtime` describes the worker wire protocol, not the executable brand.

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

## Native decision judges

Only `decision.runtime: laya` or `system_one` is supported. Ordinary OpenAI-compatible chat models cannot be selected as judges; those servers remain available for worker models. Existing configurations with `decision.runtime: openai` must select a compatible native decision service. Remove any retired `decision.chat.instructions` override from custom prompt YAML; unknown prompt keys are rejected.

For native Laya/System One judges, `/status` lists local safetensors candidates under the configured sidecar. Enter or `j` stages the exact local model path and snapshot revision; `a` applies through the existing ownership rules, and Settings `s` saves. External services show pending external activation. Candidate metadata does not establish runtime compatibility. The current device and explicit `trust_remote_code` permission are retained and disclosed. Native models retain their `laya`/`system_one` selection and typed sidecar protocol.

The Decisions view and CLI summaries label each recorded confidence source. Historical verdicts without provenance remain unspecified; pooled scores across sources are not comparable.
