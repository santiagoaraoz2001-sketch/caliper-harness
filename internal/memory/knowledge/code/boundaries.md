# Finding the smallest coherent architecture

## Ownership
Trace a concrete input from entry point to result before adding an abstraction. Identify the owner of state, validation, side effects and cleanup. List existing callers. A common function name does not prove shared ownership, and different presentation code does not prove duplicate execution engines.

Keep one authoritative representation of each fact. Views can project it for display; they should not reconstruct authority from status text. A receipt records what actually happened, while a plan describes intended work. Combining those concepts can make a planned action look completed.

For a small application, a few cohesive modules may be sufficient. For a larger one, isolate boundaries where invariants, concurrency or dependencies differ. Avoid layering every function behind an interface without a present substitution or testing need.

[Follow a worked feature through its real owner](knowledge:caliper/guides/code/architecture-example#data-flow)

## Interfaces
An interface should expose the question a caller needs answered and the legal actions it may take. Prefer structured values with explicit absence and failure to prose that the caller must parse. Preserve the distinction between requested, accepted, committed and observed.

Validate at the boundary that has authority and sufficient information. Repeating the same validation in every caller creates drift; omitting it from alternate callers creates bypasses. Keep presentation checks for usability, but let the resource owner enforce the invariant.

[Model state and commit boundaries explicitly](knowledge:caliper/guides/code/state#transitions)

## Simplification decision
Before replacing code, describe what it helps, what it costs and which behavior must survive. Compare a concrete smaller design, including its adapters and migration burden. Removing generated wrappers can be valuable if a shared executor preserves the same public semantics. Packing lines, moving code into templates, or dropping compatibility silently does not simplify the system.

Measure total burden: runtime size, conceptual owners, dependencies, input/output complexity, tests, performance and recovery. A generic framework that saves a few repeated handlers but introduces an opaque language may increase burden. Prefer a direct table for repeated data and a small shared function for identical mechanics.

[Use evidence to evaluate performance and cost](knowledge:caliper/guides/code/performance#measurement)
