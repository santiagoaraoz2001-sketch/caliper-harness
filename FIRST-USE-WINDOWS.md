# Caliper Harness: first use on Windows

This application folder is a development build. Local relocation is tested by the builder; fresh-host installation, signing and broader coding capability are still being qualified.

Keep the folder intact: `caliper.exe`, any `*.dll` beside it, `bin\`, `runtime\`, `prompts.yaml`, `python\`, `notices\` and `MANIFEST.sha256` belong together. Verify the supplied files in PowerShell from that folder with `Get-Content .\MANIFEST.sha256 | ForEach-Object { $h, $p = $_ -split '  '; (Get-FileHash -Algorithm SHA256 $p).Hash.ToLower() -eq $h }`. Go and a C compiler are builder tools; they are not needed to run the supplied executable.

## Install so `caliper` works in every terminal

From inside the bundle folder, run:

```powershell
powershell -ExecutionPolicy Bypass -File .\install-windows.ps1
```

This copies the folder to `%LOCALAPPDATA%\Caliper` and adds it to your user `PATH`. Open a **new** terminal (cmd, PowerShell or Windows Terminal) and run `caliper`. Terminals that were already open keep the old PATH. Re-running the installer replaces the installed copy. Windows SmartScreen may flag the unsigned development executable on first run; confirm the hash above before allowing it.

## Prepare your model (Ollama)

On Windows the worker model is served by Ollama ([releases](https://github.com/ollama/ollama/releases)). Install Ollama, then fetch a small coding model, for example:

```powershell
ollama pull qwen2.5-coder:7b
```

Caliper does not download weights and does not switch models on failure. The MLX worker (Apple Silicon only) is unavailable on Windows; the Ollama route covers the same Setup/Status flow. One capability differs: literal continuation of unfinished responses requires the owned MLX worker, so truncated replies on Windows are recorded as failures to continue rather than resumed invisibly.

The self-contained Windows bundle supplies private standalone Python, the pinned decision-sidecar dependencies (both supported adapters and their PyTorch/Transformers dependencies) and ripgrep. Its built-in recipes select those interpreters automatically. Users do not create virtual environments, run pip or install Python for harness dependencies. Nothing changes the system Python or your project environment. Explicit custom recipes retain their original interpreter and environment.

## Configure and run

Work from the project directory you want the model to edit and run `caliper`. When no worker is configured, Setup opens automatically (same behavior as on macOS; see the README). For manual configuration, copy `caliper-harness.yaml.example` into your project as `caliper-harness.yaml` and set:

- `model.runtime`: `ollama`, or `openai` for a generic compatible server (`mlx` is macOS only).
- `model.server`: the worker's actual API base, e.g. `http://127.0.0.1:11434/v1` for Ollama.
- `model.name`: the server's actual model identity (an Ollama alias such as `qwen2.5-coder:7b`).
- `decision.runtime`, `decision.model`, `decision.path`, `decision.server`, `decision.device`: same meaning as on macOS.
- `coding.check_command`: a real project check, such as `npm test`, `go test ./...` or `powershell -File scripts\test.ps1`.

Use `/help`, `/status`, `/settings` and `/sessions` exactly as on macOS. New terminal tabs require Windows Terminal (the `wt.exe` on your PATH); in other consoles Caliper prints the resume command instead of opening a tab.

## Building from source (developers)

Requirements: Go 1.25 with CGO, a mingw-w64 `gcc` on PATH (the Kuzu graph store uses cgo), and `uv` for the managed Python runtime. Then:

```powershell
go mod download
make bundle-windows OUT=C:\build\Caliper
```

or run `scripts\bundle-windows.ps1` directly. The output folder is self-contained; `scripts\install-windows.ps1` inside it performs the per-user install described above. For development builds without the managed runtime, `go build -o caliper-harness.exe ./cmd/specific-harness` works from the repository root.
