# Building useful project knowledge graphs

## Scoped deep dives
Build a navigable explanation for the current project. An index gives compact topic choices. A topic explains the method and decision criteria. A further worked section gives concrete inputs, expected outcomes, credible alternatives and a negative case. Label each outgoing link by the decision it helps answer. Do not replace a deep dive with another generic summary or copy the complete library into every node.

Use `project/guides/<domain>/index` and `project/guides/<domain>/<topic>` for explicitly domain-focused instructional material, including new user-requested domains. Start sections near 1,000 tokens; an enriched retrieval may use up to 2,000 tokens including preferences, locators and navigation. A broader explanation should split at meaningful decisions, keeping examples intact. Ordinary project identity, strategy, workflow and task facts stay in their existing owners and are not subject to this domain cap.

For example, a chart-design index can link a `comparison` section to `project/guides/charts/axes#shared-scale`, which links to `project/guides/charts/examples#misleading-range`. The deep example should show how two actual ranges change interpretation and how a reader can detect the misleading version. Include an alternative that fits a different goal; do not turn one stylistic preference into a universal rule.

Read one returned key and section at a time with `knowledge(key:"...",section:"..."){sections{id body references{key section label}}}`. Follow another hop when its label resolves a missing decision. Stop once there is enough support to act. Reuse delivered sections; cycles do not require repeated reads. Inspect `status` for a missing target before assuming its contents.

[Specialize guidance without losing user intent](knowledge:caliper/guides/code/knowledge#preferences-and-provenance)
[Keep state under one owner](knowledge:caliper/guides/code/boundaries#ownership)

## Preferences and provenance
Keep the current user decision in its canonical project record. An enriched article may quote or apply that decision, identifying the source/version and the context where it matters. Link back to the current identity or strategy; do not create a competing mutable source of truth. Distinguish a user's stated preference, an agent's proposed alternative and an observed result. A stored assertion is not evidence of execution.

Write complete new versions through `appendKnowledge` with the actual `expectedPreviousId`; preserve still-valid content. Give structured links `sourceSection`, target `key` and `section`, an accepted relation such as `explains`, and a useful `label`. Omitted references preserve prior links; `[]` deliberately clears them. Check that renamed headings leave no broken source or target anchors.

An explicitly selected planning reference pins a version for the next decision and subsequent task context. If the preference changes, revise its owner and deliberately reselect the new section. Do not silently reinterpret an old pinned article as current. Read-only reviewers can assess the selected material and its evidence but cannot rewrite it.

[Review persistence and repeated effects](knowledge:caliper/guides/code/state#durability)
[Separate observation from a causal explanation](knowledge:caliper/guides/tests/execution#causal-claims)
