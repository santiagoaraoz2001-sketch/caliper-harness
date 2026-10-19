# Caliper v0.1.0

Caliper is a terminal coding harness for local models. This is its first public release.

It includes source inspection and editing, project checks, guided Plan–Approach–Task workflows, independent task review, saved sessions, and a graph-backed project record. Worker and native decision models have separate server and model controls. The prompts live in an editable, commented YAML file.

This release fixes a completion bug that let a run finish with verified Tasks but an open Approach and Plan. The existing planning questions now carry the handoff through parent closure. Reviewers also receive recorded edit results, and reviewer and summarizer instructions distinguish their work from coding and final verification.

The detailed planning Q&A is unchanged. A decision to continue sends no message; advice below 0.5 confidence also leaves the model alone.

## Apple Silicon download

Download `Caliper-v0.1.0-macos-arm64.tar.gz`, unpack it, and keep the `Caliper` folder intact. From your project directory, run `/path/to/Caliper/caliper-harness`. `FIRST-USE.md` in the folder covers setup and model selection.

The folder includes the executable, Kuzu, ripgrep and private Python runtimes for the MLX worker and native decision sidecar. Model weights, Ollama and your project's own build/test tools are not included. The bundled MLX runtime requires macOS 14 or later on Apple Silicon.

This is an early development release with local ad-hoc signing, not a notarized installer. The application folder was built and tested on the development Mac, including relocation. Fresh-machine installation has not been qualified.

## Current limitations

- D1 can miss repetitive reasoning. In two replayed reviewer traces it still chose to continue after the role and instruction corrections. Better evidence reaches the reviewer now; faster live review remains to be demonstrated.
- A passing declared check is not a guarantee of correctness. Task qualification also requires independent review, which can itself make mistakes.
- Windows and Linux receive the same source fixes. Their native builds and runtime behavior have not been verified on target machines; this release supplies a Mac application folder only.

See `REVIEW-HANDOFF-VALIDATION.md` for the regression evidence and remaining live-test questions.
