# Caliper

Local models, practical coding tools and project memory.

```sh
npm install -g caliper-harness
cd your-project
caliper
```

The installer downloads the complete application for macOS Apple Silicon,
Windows x64, or Linux x64/ARM64. Node 20 or newer is needed for this launcher.
Model weights and tools belonging to your project remain yours to install.

Caliper offers available updates at startup. Use `/update` to download an update
for your next launch, or `npm install -g caliper-harness@latest`.

Your settings, graph and sessions live in your operating system's user-data
folder, separate from application versions. Updates preserve previous versions
and never delete your project work or model files.

For a project-local installation, `npm install caliper-harness` followed by
`npx caliper` works too; npm creates `node_modules` in that project. Use the global
command above to keep project directories free of installation files.
