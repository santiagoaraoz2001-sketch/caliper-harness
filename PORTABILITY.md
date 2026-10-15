# Linux conversion branch

Branch codex/linux-conversion, base b5f61fc. Source-development/external-server distribution; not a self-contained runtime or release. No commit was created.

`./scripts/bundle-linux.sh /existing/parent/new-folder` builds on native Linux amd64 or arm64 only. It requires installed Go 1.25+, a matching CGO C compiler, Bash, ripgrep, ldd, sha256sum and cached Go dependencies including go-kuzu v0.11.3. Network dependency acquisition is disabled. It refuses every existing output path, reserves a new directory, and leaves BUILD-INCOMPLETE plus partial evidence if a step fails. No unrelated output is removed. Native binary, matching Kuzu .so, ripgrep, prompt catalog, templates, decision-sidecar sources and example config stay together.

From the project directory, run `/path/to/new-folder/start.sh --config /path/to/config.yaml`. Use the launcher: direct executable invocation does not set the owned library path. The Kuzu dependency embeds its build-cache rpath; the launcher selects the bundled library first via LD_LIBRARY_PATH. The launcher preserves cwd and quoted arguments, adds the application-owned library and tool folders to lookup paths and starts the existing TUI. No managed runtime manifest is written: this distribution does not pretend to contain the Mac private Python/MLX runtime. The builder records native dependencies and source/module version information. System C/C++ dependencies remain external. Relocation onto a system with an incompatible ABI is not supported merely because ldd passed on the builder.

Use an already installed compatible inference server configured through existing recipes. Model weights are user-installed. MLX worker adaptation is not supplied on Linux. The decision sidecar requires a compatible system Python environment plus its requirements and checkpoint. GPU/device/model behavior must be proved for the selected runtime; CUDA availability is not implied by this bundle.

## Actual verification

- bash/sh syntax checks pass on the Mac.
- Invoking the builder on the Mac refuses before creating output with `Native Linux builder required; no cross-build qualification.`
- Linux amd64 hardware/process pure-Go cross-build passed in the platform audit.
- Native Linux arm64 Kuzu C API smoke passed on 2026-10-15 in the already cached Debian-based Docker image: resolved library dependencies, created/inserted a graph in a path with spaces, closed/reopened it and read the exact stored value. Evidence: `../Specific Harness/verification/2026-10-15-server-platform/wave2/linux/native-kuzu.log`. This tests the actual Linux v0.11.3 library, not the Go application or full Store owner.
- Launcher argv/cwd/environment controls are shell-level tests with a fake executable only, not application or inference evidence.

## Required native acceptance

An existing Docker Desktop Linux arm64 VM was admitted for an isolated native library smoke; the inspected cached image has GCC but no Go or ripgrep. No image/dependency download or inference was performed. Full native Go build, application Store locking/reopen, complete bundle relocation, actual TUI, shell descendant cancellation, selected server discovery/load/unload, decision selection, coding/check/review remain UNRUN. Check amd64/arm64 independently. Linux first-use documentation and complete redistribution notices/signing/release packaging remain incomplete. Mac-only extra terminal tabs currently return a truthful unsupported error. Public deployment requires completion of these gates; this branch is not a qualified Linux release.
