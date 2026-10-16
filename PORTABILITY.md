# Windows conversion branch

Branch: codex/windows-conversion. Base: b5f61fc. This is an incomplete Windows source port, not a Windows release. No commits were created during this wave.

## Implemented

- Existing Unix subprocess implementation moved unchanged behind a build tag.
- Windows subprocess owner starts the child suspended, creates a kill-on-close Job Object, assigns the process, then resumes its primary thread. Assignment/resume failures kill and reap the suspended child. Cancellation terminates the job and its leader, including the interval before assignment. Normal return closes the job to terminate remaining children. Run refuses a command not created suspended.
- Windows physical-memory detection uses GlobalMemoryStatusEx. CPU description is best-effort from the registry; absent description stays unknown.
- Windows-native tests cover normal leader exit with a live descendant, cancellation with a live descendant, rejection of an unsuspended command, and nonzero physical-memory measurement.

## Evidence and remaining acceptance

Windows amd64 hw/process packages cross-build and their Windows test executables compile. Linux amd64 hw/process packages still cross-build. These checks do not execute Windows APIs or prove descendant cleanup. Actual Windows tests MUST run before calling this implementation qualified. Test binaries built on this Mac are `/tmp/caliper-process-windows.test.exe` and `/tmp/caliper-hw-windows.test.exe`; copy and run them on an explicitly authorized Windows test host with `-test.v`. Their test subprocesses own only their test descendants.

For a full native Windows developer build, Go 1.25+, a working CGO C toolchain compatible with go-kuzu v0.11.3, and its kuzu_shared.dll/import library are required. Build cmd/specific-harness on that host with CGO enabled, then place the matching DLL and all discovered runtime dependencies beside the executable. This procedure has not yet been qualified; do not advertise a ready installer.

Bash remains the check/tool language. Windows must supply a compatible bash in PATH for current tool semantics; PowerShell is not silently substituted. Ripgrep, project check tools and selected server runtimes must be installed/available. Managed Mac bundles do not work on Windows: bundle manifest validation, Python executable paths, executable-bit checks and archive/build scripts still require target-specific changes. The MLX worker adapter remains Apple-specific; Windows should use a supported selected inference server, whose tool-call and continuation behavior must be tested.

Other native qualification gates: graph reopen and cross-process locking, Unicode/drive paths and path spaces, case/symlink/hardlink behavior, file replacement, TUI key/paste/rendering, server discovery/load/unload, decision runtime/selection, and actual coding/check/review flow. Additional-terminal-tab functionality explicitly supports only macOS today. No remote host, GPU or model inference was used for this branch.

## Native developer packaging starter

`scripts/bundle-windows.ps1 -OutputDirectory C:\existing-parent\new-folder` provides a native Windows amd64 builder using installed Go, gcc, ripgrep and Bash plus cached dependencies. It downloads nothing, refuses existing output, retains BUILD-INCOMPLETE on failure, and packages Kuzu DLL, prompts/templates, decision sources and a `start.ps1` launcher. No managed runtime marker is written. Run the launcher from the intended project directory; it preserves cwd/argv. The builder omits the example YAML's explicit source-relative template override, allowing the existing executable-relative default to locate bundled templates when launched from another project. This transformation has only been inspected and fixture-checked on the Mac; native PowerShell execution is still required.

PowerShell is unavailable on the audit Mac: these scripts have not been parsed or executed by PowerShell. Native CGO import-library compatibility and transitive DLL audit remain outstanding; a successful build alone is not a runnable or redistributable Windows release. Complete native dependency/license qualification before distribution.

## 2026-10-16 packaging reconciliation

The offline developer builder remains `scripts/bundle-windows.ps1`. The WIP managed builder is retained separately as `scripts/bundle-windows-managed.ps1 -Out NEW_FOLDER -AllowDownloads`; its preparation helper also requires explicit download opt-in. Both include templates, prompt catalog and the decision adapter's mps_attention.py. The managed manifest uses UTF-8 without BOM for Go JSON compatibility, and publishes only after relocation import verification. Windows memory uses GlobalMemoryStatusEx; WIP best-effort machine metadata is preserved. No Windows build, PowerShell execution, dependency download or native runtime qualification was performed by the Mac reconciliation. Earlier references to no private runtime describe the offline mode.
