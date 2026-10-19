# Caliper on Linux

Plain `npm install caliper-harness` also registers `caliper` for your user account.
Reopen the terminal if the command is not found. Use `-g` to avoid local npm files.

With Node.js 20 or newer, install the complete application and terminal command:

```sh
npm install -g caliper-harness
cd /path/to/your/project
caliper
```

Packages are provided for x86-64 (`amd64`) and ARM64 (`arm64`). They include the
private decision-model Python environment, CUDA dependencies and ripgrep. The
GPU runtime is downloaded upfront; model weights, GPU drivers, your worker
server and project build tools remain separate. Linux does not run the MLX
worker. Select an installed Ollama or compatible server in Setup.

Use `/manage` to inspect servers and models, `/settings` to configure the harness,
and `/update` to prepare an available update for the next launch. Updates keep
the running version intact. Turning D1 off replaces required readiness and Risky
decisions with human approval; checks and independent review still apply.

New workspace state lives in `$XDG_DATA_HOME/caliper`, or
`~/.local/share/caliper` when that variable is unset. Each project has a separate
workspace keyed by its canonical path. Existing project-local data and explicit
configuration paths remain in use. Reusable connections live in the OS user
configuration directory. Keep these user directories when removing old versions.

## Downloadable folder

Large Linux archives are split to fit GitHub's per-file release size. Download
all matching parts and the checksum file from the same release. For example:

```sh
cat caliper-0.1.2-linux-amd64.tar.gz.part-* > caliper-0.1.2-linux-amd64.tar.gz
sha256sum -c caliper-0.1.2-linux-amd64.tar.gz.sha256
tar -xzf caliper-0.1.2-linux-amd64.tar.gz
cd Caliper
sh ./install.sh
```

Use `linux-arm64` filenames on ARM64. The npm installer assembles and verifies
parts automatically. Keep the extracted application folder intact. Its native
library dependencies are listed in `notices/NATIVE-DEPENDENCIES.txt`; a complete
runtime import check does not establish compatibility with every Linux driver
or model. See `SERVER-MODELS.md` for connection and loading controls.

Use `/manage` for explicitly saved server/model connections and lifecycle controls; `/status` is the read-only activity and usage screen. Caliper does not scan model folders or listening ports. See [Models & Servers](SERVER-MODELS.md) for adding a connection.
