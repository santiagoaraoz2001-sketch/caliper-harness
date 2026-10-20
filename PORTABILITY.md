# Platform support

MacOS, Windows and Linux share the same product source. Platform-specific code
handles process cleanup, terminal input, library loading and installation.

| Platform | Package | Included model runtimes |
|---|---|---|
| macOS Apple Silicon | arm64 | MLX worker and native decision runtime |
| Windows | x64 | Native decision runtime; connect your coding server |
| Linux | x64, ARM64 | Native decision runtime with CUDA dependencies; connect your coding server |

All packages include the executable, Kuzu library, ripgrep, prompt catalog
and private runtime dependencies. Model weights, GPU drivers and your
project's build tools remain separate. The Linux CUDA bundle is several GB.

Windows checks for Microsoft's installed x64 Visual C++ runtime and offers
Microsoft's setup only when the required version is missing or outdated.
Caliper does not redistribute Microsoft's runtime installer or DLLs. Shell
commands use Bash; Windows needs a compatible Bash on PATH. PowerShell is not
silently substituted for project check commands.

The MLX worker is specific to Apple Silicon. On Windows and Linux, use a
compatible server configured in `/manage`. A server's catalog or health response
establishes connectivity, not compatibility with every model or GPU.

## Installation and updates

Install with `npm install -g caliper-harness`, then launch `caliper` from your
project. You can also extract the complete release folder and use its installer.
Keep the executable and its supporting files together.

Updates install a new application folder and take effect after restart. Settings,
connections, graph records and sessions live separately from application versions.
Personal Settings follow you between directories; explicit project configuration
overrides them. Project checks and project data remain scoped to their workspace.
Previous application folders are retained, but older executables may not understand
newer settings or data, so arbitrary downgrade compatibility is not promised.

See [macOS first use](FIRST-USE.md), [Windows first use](FIRST-USE-WINDOWS.md),
and [Linux first use](FIRST-USE-LINUX.md) for platform instructions.

## Verification scope

Release notes identify the exact commit and native checks performed for each
archive. The release workflow builds and launches the native product, verifies
private-runtime imports after relocation, runs the platform's Go test packages,
exercises `/status` and `/manage` in a real terminal, and installs the complete
product through npm. Windows also checks prerequisites and reinstall preservation.
Archives are checked against their hashes and file inventories.

The published v0.1.6 packages passed these checks on all four targets in
[workflow 39448081115](https://github.com/santiagoaraoz2001-sketch/caliper-harness/actions/runs/39448081115),
from commit `f8977791546e1b2685ca8320fbd9b0fabda069a5`. Later release results are
listed in their own notes; success on an older commit does not qualify a new build.

Native packaging and terminal tests do not establish native model inference.
Current model benchmarks run on a Mac. Windows/Linux inference quality, GPU-driver
combinations and unfamiliar terminal hosts require separate testing. macOS bundles
are not notarized, and Windows bundles are not signed. Release checks cannot cover
every fresh-machine environment.
