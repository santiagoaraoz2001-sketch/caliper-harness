# Worked architecture: a configurable preview

## Data flow
Example feature: a user edits a preview density setting, sees a draft, commits it and expects the next session to restore it. Trace the actual flow: control event → draft state → validated commit → durable configuration → preview consumer → visible layout.

Choose one configuration owner. The UI owns only its temporary draft and renders owner state. The preview subscribes to or reads the committed value through a defined interface. Persistence serializes the authoritative configuration, not a separately reconstructed UI snapshot.

[Find owners before introducing a new service](knowledge:caliper/guides/code/boundaries#ownership)

## Design choice
A small program can use a typed configuration value and a single commit function. A generic event bus, repository layer and plugin registry would add little unless there are real independent consumers or storage implementations. Conversely, writing directly to a global variable in each event handler makes cancellation, validation and alternate entry points inconsistent.

The commit function validates the full proposal, compares its version when concurrent edits are possible, writes atomically, then publishes the new committed state. A failed write preserves the previous state and the user's draft. The UI shows failure rather than pretending the preview is durable.

## Evidence map
Record the feature's entry control, owning commit symbol, persistence path and consumer symbol in a concise architecture entry. Link the testing spec and current evidence. Do not duplicate source code into the graph. A changed file hash says the implementation changed; it does not establish that the feature works.

The test enters through the public action, commits non-default density, closes and reopens the store, then verifies the consumer's visible layout. A second workspace retains another setting. An isolated no-op writer and a stale consumer should each fail a different assertion.

[Use a full lifecycle sequence](knowledge:caliper/guides/tests/lifecycle-example#sequence)
[Test consumer behavior rather than only stored flags](knowledge:caliper/guides/tests/oracle-examples#consumer-behavior)

## Extension
If a second interface is added, it calls the same owner and supplies its own presentation. If a second storage backend becomes necessary, introduce the narrow persistence interface at that boundary. Do not build those layers in advance merely to make a diagram symmetrical.

This example demonstrates proportionate architecture: explicit ownership and evidence without inventing a framework. The same method applies to a CLI setting, project identity, test selection or saved game option, but each has its own actual contract.
