# Model operating guide

The active model guide is in the shared editable `prompts.yaml` catalog:
`help.overview`, `help.<tool>` and `help.reference.<topic>` contain its sections.
The model `help` tool reads a selected section and labels actual availability;
only available tools include their current schemas. `help(topic="reference")`
opens the small topical index. Startup publishes these same reference documents
as versioned project knowledge under `harness/reference/<topic>` through the
existing knowledge owner. The reference library is separate from `plan`'s
current authoring Q/A; its bodies enter model context only through selected help
or GraphQL reads.
This file is a source-navigation pointer, not a second prompt source.
