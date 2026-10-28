# Domain guidance and selected project knowledge

Domain articles start near 1,000 tokens. The runtime ceiling is 2,000 selected-model tokens per domain retrieval when the adapter supports exact counting, including its locator and navigation. Adapters without that capability use the existing conservative UTF-8-byte estimate with a 2,000-byte ceiling on the same domain envelope. This estimate is disclosed and is not an exact token count or universal tokenizer guarantee. The ordinary GraphQL source row/byte limits and overall model context admission still apply.

Bundled domain articles live under `caliper/standards` and `caliper/guides`. Authors explicitly opt into the same domain delivery contract with `project/guides/<domain>/<topic>`; use an `index` topic to introduce a new domain. Classification does not infer a domain from arbitrary prose, a word such as “tests”, or a reference to a guide. Current `project/design/identity`, `project/testing/strategy`, other facts and PAT records are outside the domain cap. Topical recall retains its independent character budget; it neither limits current owner facts nor crops domain guides into that recall channel.

## Navigation and authorship

Use an index section to identify the relevant decision, a linked topic to explain the method, and another linked section to demonstrate a worked case and a credible negative. Each section has useful labels and further relevant references, not a recursive expansion. GraphQL discovery returns keys and section headings; request one selected body next. Read-only roles follow the same graph but cannot mutate knowledge.

For a testing example, retrieve `caliper/standards/tests#oracle`, then its `caliper/guides/tests/oracles#independence` link, then the `caliper/guides/tests/clearance#swept-envelope` example. Each hop uses `knowledge(key:"<returned key>",section:"<returned section>"){sections{id body references{key section label}}}`. Reuse already delivered material and stop when the decision has enough evidence. A missing target is unavailable, not permission to invent its contents.

User-requested and agent-built graphs use the same section and reference mechanics. Store current user decisions under their established owner. An enriched article may quote/apply a preference with its source/version and link back to the owner; it must not become a competing mutable authority. `appendKnowledge` keeps immutable versions and compare-and-swap ownership. Omitted references preserve existing links; an empty list deliberately clears them. Structured `sourceSection` links work even when the body has no Markdown link.

## Selected delivery

At goal, approach and observation questions, optional `guidance` accepts returned `knowledge:key#section` locators, one per line. Binding resolves the exact current immutable version and validates its section before the existing owner mutation. Omission inherits; an explicitly empty list clears. The most specific authored Task selection overrides Approach, then Plan. Subsequent planning steps preserve the selection.

The existing project-context frame delivers pinned bodies before the next complete model request, including in admitted workers and reviewers. Selected historical versions remain pinned instead of silently following a new head. Workflow browsing supplies context only: it cannot claim execution, load unrelated task notes, add permissions or make an old check current. Stale/foreign/malformed owners are unavailable or refused by their existing owner boundaries.

Testing guidance off suppresses automatic testing advice, including previously pinned testing sections; metadata remains disabled so it can be restored when enabled. It does not hide the current testing strategy, remove required checks or prohibit an explicit GraphQL read.

## Counting basis and backend limits

Model-facing bounded GraphQL classifies catalog values before field projection, then counts the selected serialized values, including aliases and links. An over-budget or uncountable response reports `data_omitted` and the omitted domain aliases/list positions. Ordinary sibling values and ordinary values in the same list remain available; the returned list is incomplete, not a complete search result. Mutation effect receipts remain visible: a post-projection error is not rollback. A standalone ordinary-fact query never invokes the domain tokenizer. Host inspection through raw GraphQL is not model-context delivery.

Rulith uses its selected native `/tokenize` endpoint without generation. No completion request, Judge, server change or tokenizer dependency is introduced. MLX, Ollama and generic OpenAI-compatible adapters explicitly declare that native count-only capability is unavailable; their compact domain reads instead use the existing uncalibrated context policy of charging each UTF-8 byte. The complete serialized domain values, navigation and basis label count toward the 2,000-unit bound. A section that exceeds it must be retrieved through a narrower section and further useful links; it is never cropped silently.

Model-facing GraphQL exposes `domain_budget_basis`; each supplied project-context domain item exposes `domainBudgetBasis`. `native-token-ids` means the selected native tokenizer counted the envelope. `utf8-bytes-conservative` means a conservative byte estimate, not measured model tokens. Ordinary facts and PAT values are not charged against this domain allowance, even alongside domain results.

Fallback applies only to the explicit supported-adapter capability marker. Missing counters, unknown runtimes, malformed native tokenizer responses, timeouts and cancellations remain failures; they cannot silently switch accounting policy. Native exact counting on MLX/Ollama/generic adapters and natural model-use benefit remain separate qualification gaps. Tests against fixture counters establish host mechanics only, not tokenizer accuracy or equal backend performance.
