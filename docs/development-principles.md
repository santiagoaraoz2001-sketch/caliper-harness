# Caliper development principles

1. Simplicity and lightness come first. Prefer removing work to adding mechanisms.
2. Take load off the model. Host code owns deterministic mechanics; the model supplies judgment.
3. Treat bloat as a defect: unnecessary dependencies, tools, instructions, outputs and workflows all impose costs.
4. Make complex work structured and step by step. Present the current question and legal next action, not every future possibility.
5. GraphQL and Kuzu are the shared knowledge and state foundation. Retrieve relevant facts, follow useful links and update meaningful decisions without making the model maintain an exhaustive inventory.
6. Keep instructions consistent across roles, help, tool schemas and host enforcement. Reuse one source for shared rules.
7. Design tools for small models: clear names, few required arguments, useful examples and actionable errors. Reveal advanced options only when needed.
8. Help must explain the current capability, exact call and recovery path without granting unavailable authority.
9. Evidence over claims. Available, implemented and verified are different states. Missing checks and uncertainty remain explicit.
10. Host-owned mechanics. The harness manages identities, scope, transitions, validation and recovery; the model should not reconstruct them from prose.
11. Measure total burden: tokens, tool calls, latency, memory and recovery effort together. Shorter prompts that cause more mistakes are not an improvement.

Apply these as development constraints, not an additional model checklist. Preserve user data and current work during recovery. Optimize measured waste; qualify behavior with real platform tests. A benchmark is evidence about that run, not a guarantee of future performance.
