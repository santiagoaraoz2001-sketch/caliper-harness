# Caliper

Local models, practical coding tools and project memory.

```sh
npm install caliper-harness
caliper
```

Installation downloads the complete application and registers `caliper` for your
user account. If your current terminal cannot find it, open a new terminal;
installation cannot change the PATH of a terminal that is already running. You
can then enter any project directory and run `caliper`.

Recent npm versions ask for approval before running a package's install script.
If npm reports that `caliper-harness` is not covered by `allowScripts`, run
`npm install-scripts approve caliper-harness` and then
`npm rebuild caliper-harness` in the same directory to finish installing.

Plain npm installation creates `node_modules` and npm package files in the
installation directory. To avoid those files in a project, install globally with
`npm install -g caliper-harness`; npm supplies the global command in that case.

Supported platforms are macOS Apple Silicon, Windows x64 and Linux x64/ARM64.
Node 20 or newer is needed for the npm launcher. On Windows, the launcher checks
the Microsoft Visual C++ runtime (the x64 redistributable, version 14.44.35211.0
or newer) before starting. If it is missing or outdated,
the shared prerequisite helper downloads Microsoft's installer directly and may
request administrator approval or a restart. A compatible installation needs no
download. Caliper does not bundle Microsoft's installer or runtime DLLs.

Model weights and tools belonging to your project remain yours to install.

Caliper offers available updates at startup. Use `/update` to download an update
for your next launch, or repeat your npm installation with `caliper-harness@latest`.

Your settings, graph and sessions live in your operating system's user-data
folder, separate from application versions. Updates preserve previous versions
and never delete your project work or model files.
