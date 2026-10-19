Help me connect my existing servers and downloaded models to Caliper Harness.
If you can access this computer, inspect it and propose the exact changes. If you cannot, ask me for the relevant paths and command output. Do not invent paths, model IDs, launch flags or runtime compatibility.

This computer's OS: {{OS}}
Reusable connections file: {{CONNECTIONS}}
Current project's settings: {{PROJECT}}

First ask which models and servers I want to connect, which model should do coding, and whether I want a native decision model. Ask where my checkpoints are stored before searching. Inspect existing connection settings, installed server commands and their --help, checkpoint config/index files and the selected server's catalog. A checkpoint folder must contain all its shards; a GGUF selection uses the first shard. Exclude datasets. Use the exact model ID accepted by that server; MLX local model launchers use the checkpoint's absolute path. Confirm support for the architecture, quantization and any speculative decoding head from the installed runtime before configuring it.

Back up connections.yaml, then merge the requested choices without deleting other connections. Preserve the existing prompts selection and every unrelated setting. Leave project settings, workspaces, graph, sessions, prompts and model files intact. Explicit project model/decision/serve sections override these machine defaults: explain any override and ask before changing it. Stop Caliper before writing and reopen it after validation so it reloads the file. Do not download models or dependencies, start inference, enable downloaded Python code, or stop another application's processes without my approval. Keep credentials out of chat and the copied prompt.

connections.yaml accepts these top-level keys only: prompts (preserve it), model, decision, serve. Missing fields use Caliper defaults. This is a YAML example, not evidence that these sample paths, ports or IDs exist:

```yaml
model:
  runtime: ollama
  server: http://127.0.0.1:11434/v1
  name: exact-installed-model-id
  tool_protocol: structured
decision:
  disabled: true
serve:
  recipes:
    - name: My Ollama
      kind: ollama
      runtime: ollama
      url: http://127.0.0.1:11434/v1
      launcher: preset
    - name: My MLX
      kind: openai
      runtime: mlx
      url: http://127.0.0.1:8080/v1
      launcher: preset
  models:
    - server: http://127.0.0.1:11434/v1
      name: exact-installed-model-id
    - server: http://127.0.0.1:8080/v1
      name: /absolute/path/to/mlx-checkpoint
      path: /absolute/path/to/mlx-checkpoint
```

Each serve.recipes entry has name (unique), kind, runtime and url, plus optional launcher, argv and dir. kind is openai, ollama, rulith, sidecar or auto; prefer the known protocol. Coding runtime is openai, mlx, ollama or rulith. Use openai for compatible custom servers such as llama.cpp or Gufo. MLX requires a compatible Mac runtime. launcher: preset uses Caliper's available local MLX/Ollama/native-judge launcher; verify availability on this OS. Omit launcher and argv for an externally managed server. For a custom local launcher, use argv as a YAML sequence of literal executable/argument strings and optional dir as its working directory. Only {model} is substituted; shell syntax, ~, environment variables and invented {port}/{host} placeholders are not expanded. Use absolute paths and explicit flags. Do not set both preset and custom argv.

Each serve.models entry has server (the matching recipe URL), name (wire ID, or absolute path for an MLX checkpoint launcher), and optional path (checkpoint folder or first GGUF shard). Saving a model does not load it or assign a role. model.server/name/runtime selects Coding; model.tool_protocol is structured or text. model.plain_request_timeout_seconds is optional; preserve its existing value.

For a native judge, set decision.disabled: false and decision.server/model/runtime/path/device to its verified endpoint, exact model ID, native runtime, absolute checkpoint path and desired device (auto, cpu or gpu). Native runtimes are laya and system_one; select from the checkpoint's actual native API, not its name. A generic chat endpoint does not become a native judge. Optional revision pins the checkpoint; trust_remote_code permits downloaded checkpoint Python only with my explicit approval. Preserve existing decision timeout, input-token, threshold, logging and style settings. Add a matching recipe with kind: sidecar, the same native runtime and URL, and launcher: preset only when Caliper should own it. Otherwise use the existing external sidecar. No judge selected: leave it disabled; required decisions ask me instead.

Validate the merged settings with the existing command `caliper config --config <current-project-settings-path>`; quote the actual path for this shell. This reads configuration and does not run inference. Do not pass connections.yaml as --config: it is loaded automatically before the project file. Unknown YAML keys are errors. Check that the printed effective selections match my choices. In the reopened TUI use /manage, r to refresh saved endpoints, t to test, u to select Coding and j to select Judge (native selection may require a to apply). Use s to start, l to load and x to unload only when I request those actions. A responding catalog/health check proves availability, not generation, GPU residency or judge quality. Report what was configured, what actually passed, and what remains untested.
