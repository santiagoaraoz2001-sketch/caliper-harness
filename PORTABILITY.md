# Platform qualification

MacOS, Windows and Linux retain the same shared source and platform-specific build files. Windows/Linux native acceptance remains separate from Mac validation.

# Windows

Branch: Windows. This is a Windows source and packaging candidate, not a qualified Windows release. Historical evidence below retains its original scope.

## Implemented

- Existing Unix subprocess implementation moved unchanged behind a build tag.
- Windows subprocess owner starts the child suspended, creates a kill-on-close Job Object, assigns the process, then resumes its primary thread. Assignment/resume failures kill and reap the suspended child. Cancellation terminates the job and its leader, including the interval before assignment. Normal return closes the job to terminate remaining children. Run refuses a command not created suspended.
- Windows physical-memory detection uses GlobalMemoryStatusEx. CPU description is best-effort from the registry; absent description stays unknown.
- Windows-native tests cover normal leader exit with a live descendant, cancellation with a live descendant, rejection of an unsuspended command, and nonzero physical-memory measurement.

## Evidence and remaining acceptance

Windows amd64 hw/process packages cross-build and their Windows test executables compile. Linux amd64 hw/process packages still cross-build. These checks do not execute Windows APIs or prove descendant cleanup. Actual Windows tests MUST run before calling this implementation qualified. Test binaries built on this Mac are `/tmp/caliper-process-windows.test.exe` and `/tmp/caliper-hw-windows.test.exe`; copy and run them on an explicitly authorized Windows test host with `-test.v`. Their test subprocesses own only their test descendants.

For a full native Windows developer build, Go 1.25+, a working CGO C toolchain compatible with go-kuzu v0.11.3, and its kuzu_shared.dll/import library are required. Build cmd/specific-harness on that host with CGO enabled, then place the matching DLL and all discovered runtime dependencies beside the executable. This procedure has not yet been qualified; do not advertise a ready installer.

Bash remains the check/tool language. Windows must supply a compatible bash in PATH for current tool semantics; PowerShell is not silently substituted. Ripgrep, project check tools and selected server runtimes must be installed/available. Managed Mac bundles do not work on Windows: bundle manifest validation, Python executable paths, executable-bit checks and archive/build scripts still require target-specific changes. The MLX worker adapter remains Apple-specific; Windows should use a supported selected inference server, whose tool-call and continuation behavior must be tested.

Other native qualification gates: graph reopen and cross-process locking, Unicode/drive paths and path spaces, case/symlink/hardlink behavior, file replacement, TUI key/paste/rendering, server discovery/load/unload, decision runtime/selection, and actual coding/check/review flow. The terminal-tab code supports macOS Terminal/iTerm2 and Windows Terminal sessions (`WT_SESSION`) through `wt.exe`; other Windows consoles receive the resume command. Native Windows terminal execution remains unverified. No remote host, GPU or model inference was used for this branch.

## Native developer packaging starter

`scripts/bundle-windows.ps1 -OutputDirectory C:\existing-parent\new-folder` provides a native Windows amd64 builder using installed Go, gcc, ripgrep and Bash plus cached dependencies. It downloads nothing, refuses existing output, retains BUILD-INCOMPLETE on failure, and packages Kuzu DLL, prompts/templates, decision sources and a `start.ps1` launcher. No managed runtime marker is written. Run the launcher from the intended project directory; it preserves cwd/argv. The builder omits the example YAML's explicit source-relative template override, allowing the existing executable-relative default to locate bundled templates when launched from another project. This transformation has only been inspected and fixture-checked on the Mac; native PowerShell execution is still required.

PowerShell is unavailable on the audit Mac: these scripts have not been parsed or executed by PowerShell. Native CGO import-library compatibility and transitive DLL audit remain outstanding; a successful build alone is not a runnable or redistributable Windows release. Complete native dependency/license qualification before distribution.

## 2026-10-16 packaging reconciliation

The offline developer builder remains `scripts/bundle-windows.ps1`. The WIP managed builder is retained separately as `scripts/bundle-windows-managed.ps1 -Out NEW_FOLDER -AllowDownloads`; its preparation helper also requires explicit download opt-in. Both include templates, prompt catalog and the decision adapter's mps_attention.py. The managed manifest uses UTF-8 without BOM for Go JSON compatibility, and publishes only after relocation import verification. Windows memory uses GlobalMemoryStatusEx; WIP best-effort machine metadata is preserved. No Windows build, PowerShell execution, dependency download or native runtime qualification was performed by the Mac reconciliation. Earlier references to no private runtime describe the offline mode.

# Linux

Branch Linux. Source-development/external-server distribution; not a self-contained runtime or release. Historical qualification evidence below retains its original scope.

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

An existing Docker Desktop Linux arm64 VM was admitted for an isolated native library smoke; the inspected cached image has GCC but no Go or ripgrep. No image/dependency download or inference was performed. Full native Go build, application Store locking/reopen, complete bundle relocation, actual TUI, shell descendant cancellation, selected server discovery/load/unload, decision selection, coding/check/review remain UNRUN. Check amd64/arm64 independently. Linux first-use documentation and complete redistribution notices/signing/release packaging remain incomplete. Linux extra terminal tabs return an unsupported error with the direct resume command. Public deployment requires completion of these gates; this branch is not a qualified Linux release.
