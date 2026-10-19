# Caliper Harness: first use on Windows

This application folder includes Caliper and its private Python dependencies. Release checks exercise native application loading, installation, package imports and relocation. Model inference and GPU compatibility depend on the server and hardware you select.

Keep the folder intact: `caliper.exe`, any `*.dll` beside it, `bin\`, `runtime\`, `prompts.yaml`, `templates\`, `python\`, `notices\` and `MANIFEST.sha256`, `ensure-windows-runtime.ps1` and the launcher scripts belong together. Verify the supplied files in PowerShell from that folder with `Get-Content .\MANIFEST.sha256 | ForEach-Object { $h, $p = $_ -split '  '; (Get-FileHash -Algorithm SHA256 $p).Hash.ToLower() -eq $h }`. Go and a C compiler are builder tools; they are not needed to run the supplied executable. In the managed bundle this verification is also automatic: every startup re-hashes all files against `MANIFEST.sha256`, and if only downloadable dependencies (the private Python runtime or bundled ripgrep) are missing or changed, Caliper warns, names them, and asks a yes/no question before downloading the pinned replacements and re-verifying. Answering no (or pressing Enter) refuses the launch; damage to anything else still requires replacing the complete folder.

## Install so `caliper` works in every terminal

With Node.js 20 or newer, `npm install -g caliper-harness` installs the complete
managed application and the `caliper` command. `/update` stages an available
release for the next launch; your active run and previous work remain intact.
You can also use the downloadable folder:

From inside the bundle folder, run:

```powershell
powershell -ExecutionPolicy Bypass -File .\install-windows.ps1
```

Before activation, the installer checks the **system Microsoft Visual C++ x64
Runtime (14.44.35211 or newer)**. A compatible installation is used silently,
without downloads. If it is missing or outdated, Caliper downloads Microsoft's
installer directly from Microsoft, checks its Authenticode signature, and opens
its setup window. Review Microsoft's terms there; Windows may request
administrator approval. Cancelling or declining leaves the existing Caliper
installation unchanged. If Microsoft requests a restart, restart Windows and
run the Caliper installer again. A verification/access error is reported rather
than treated as a missing runtime. Caliper does not distribute Microsoft runtime
DLLs or the installer. See [Microsoft's runtime information](https://learn.microsoft.com/en-us/cpp/windows/latest-supported-vc-redist/).

To run the downloaded folder without installing a terminal command, use
`powershell -ExecutionPolicy Bypass -File .\start.ps1` from your project, with
the full path to that script. The launcher performs the prerequisite check
before loading the native executable; a direct `caliper.exe` invocation cannot
install its own DLL prerequisites.

This copies the application into a separate folder under `%LOCALAPPDATA%\Caliper\applications` and registers `%LOCALAPPDATA%\Caliper\commands` on your user `PATH`. Open a **new** terminal (cmd, PowerShell or Windows Terminal) and run `caliper`. Re-running the installer selects a new intact copy without moving the user-data directory, replacing active executables or deleting previous versions. `-TargetDirectory C:\path\Caliper` selects another application root; central user data stays in its original location. `-NoPath` leaves PATH management to you. Windows SmartScreen may flag the unsigned development executable on first run; confirm the hash above before allowing it.

Install a compatible Bash on PATH (for example Git for Windows Bash). Shell tools and coding checks use Bash even when the interactive terminal is PowerShell. Project check tools and inference servers remain external.

## Prepare your model (Ollama)

On Windows the worker model is served by Ollama ([releases](https://github.com/ollama/ollama/releases)). Install Ollama, then fetch a small coding model, for example:

```powershell
ollama pull qwen2.5-coder:7b
```

Caliper does not download weights and does not switch models on failure. The MLX worker (Apple Silicon only) is unavailable on Windows; the Ollama route covers the same Setup/Status flow. One capability differs: literal continuation of unfinished responses requires the owned MLX worker, so truncated replies on Windows are recorded as failures to continue rather than resumed invisibly.

The Windows bundle supplies private standalone Python, the pinned decision-sidecar dependencies (both supported adapters and their PyTorch/Transformers dependencies) and ripgrep. Its built-in recipes select those interpreters automatically. Users do not create virtual environments, run pip or install Python for harness dependencies. Nothing changes the system Python or your project environment. Explicit custom recipes retain their original interpreter and environment.

## Configure and run

Work from the project directory you want the model to edit and run `caliper`. When no worker is configured, Setup opens automatically. Select a Worker and a Judge, optionally set a project check, then review and save. Press `a` on either model page to add an existing endpoint or local checkpoint/launcher. Connections are reusable across projects; new project settings and state are kept under `%LOCALAPPDATA%\caliper\workspaces`, keyed by the project path. Existing project-local files and explicit paths remain in use. Native decision models are recommended, and compatible chat judges can also be selected. See [Server and model connections](SERVER-MODELS.md) for supported runtimes, launch controls and precedence. For manual configuration, copy `caliper-harness.yaml.example` into your project as `caliper-harness.yaml` and set:

- `model.runtime`: `ollama`, or `openai` for a generic compatible server (`mlx` is macOS only).
- `model.server`: the worker's actual API base, e.g. `http://127.0.0.1:11434/v1` for Ollama.
- `model.name`: the server's actual model identity (an Ollama alias such as `qwen2.5-coder:7b`).
- `decision.runtime`, `decision.model`, `decision.path`, `decision.server`, `decision.device`: same meaning as on macOS.
- `coding.check_command`: a real project check, such as `npm test`, `go test ./...` or `powershell -File scripts\test.ps1`.

Use `/help`, `/status`, `/settings` and `/sessions` exactly as on macOS. In Settings, disabling the decision model uses human approval for readiness and Risky actions, preserves checks and independent review, and disables automatic progress advice and recovery. New terminal tabs require Windows Terminal (the `wt.exe` on your PATH); in other consoles Caliper prints the resume command instead of opening a tab.

## Building from source (developers)

Two native Windows amd64 packaging modes are available. Both require Go 1.25+, compatible mingw-w64 GCC/CGO, Bash and cached Go dependencies. Both require the system Microsoft Visual C++ x64 runtime described above. Build scripts check it without downloading or opening an installer; run `scripts\ensure-windows-runtime.ps1` interactively first if needed.

Offline developer folder (installed ripgrep, system Python for optional decision sidecar; no downloads):

```powershell
powershell -File scripts\bundle-windows.ps1 -OutputDirectory C:\build\new-folder
```

Run its included `install-windows.ps1`, then `caliper` from your project directory. Its command shim preserves the developer mode, working directory, arguments and exit status without adding a managed-runtime marker. `start.ps1` remains available.

Private runtime candidate (also requires uv, curl.exe and tar.exe):

```powershell
powershell -File scripts\bundle-windows-managed.ps1 -Out C:\build\new-managed-folder -AllowDownloads
```

This explicit mode downloads the pinned Python/ripgrep archives and hash-locked Python packages. Archive hashes are checked before extraction; dependency synchronization requires hashes. Imports and package origins are checked again after relocation. The build also audits native executable/DLL imports against bundled libraries and the verified system runtime. These checks do not load a model or prove native inference or GPU compatibility. Both modes refuse an existing output folder and retain incomplete evidence on failure. Do not launch a folder containing `BUILD-INCOMPLETE`.
