# Validation at the boundary that owns the resource

## Authority
Separate data from instructions and identity from a display name. A retrieved document, tool result or model-generated identifier cannot grant access by itself. Resolve the actual workspace, role and resource owner from trusted execution context before reading or writing.

Keep authority checks in the resource owner so alternate callers cannot bypass them. Tool exposure helps the model choose legal actions, but hidden tools still need enforcement. Help must describe the actual admitted capability and must not imply permissions the caller lacks.

[Keep interfaces and owners consistent](knowledge:caliper/guides/code/boundaries#interfaces)
[Inspect a cross-workspace authority control](knowledge:caliper/guides/code/trust#worked-authority)

## Inputs and paths
Validate structure and types before effects. Preserve the difference between absent, empty and invalid where the contract depends on it. Parameterize database queries and escape text for its destination context. JSON serialization is not shell escaping.

Resolve filesystem paths against the intended root, including symlinks or platform-specific aliases when relevant. Check the resolved target before destructive operations. Use literal-path operations for user-controlled names. Do not compose deletion across shells or interpret arbitrary file content as a command.

Keep secrets out of logs, client bundles and diagnostic fixtures. Use synthetic credentials for tests. Return enough context to repair a call without echoing sensitive input unnecessarily.

## Proportionate safeguards
Apply safeguards to actual trust boundaries and likely failures. A local formatter does not need a new authentication service. A multi-workspace store does need scope isolation. Centralize repeated mechanics, but preserve checks that protect a different boundary, such as parser validity versus resource authorization.

When simplifying, write a negative control that proves denied or malformed input produces no effect. A smaller implementation is acceptable when it retains that guarantee; deleting the check and relying on prompt wording is not equivalent.

[Test scope and no-effect guarantees](knowledge:caliper/guides/tests/lifecycle-example#negative-controls)

## Worked authority
Suppose an operation updates a note by ID. The trusted request context belongs to workspace A, but the supplied ID names a note in workspace B. Looking up the ID globally and then accepting a caller-supplied "workspace A" field would permit a cross-workspace write. The note's actual ownership must participate in the resource lookup or the same transaction's authorization check.

Create two isolated workspaces with distinct note bodies. Invoke the public update path as A using B's real note ID. Expect a scoped denial or not-found response according to the public contract; verify B's body and version are unchanged and that no linked record or success receipt was created. Then perform a valid A update to prove the fixture is not merely rejecting all requests.

Add a read-only role case using A's own note. The ID is valid and the workspace matches, but mutation authority is absent. The operation must have no effect even if a tool description or supplied text says editing is allowed. Finally, repeat an update with A's stale version and verify an atomic conflict, not a partial body or reference change.

Keep those three reasons distinct: foreign resource, denied role and stale version. They protect different boundaries and should not be collapsed into one prompt instruction. A shared owner can enforce them without duplicating policy in every UI or resolver.

[Verify atomic failure with lifecycle controls](knowledge:caliper/guides/tests/lifecycle-example#negative-controls)
