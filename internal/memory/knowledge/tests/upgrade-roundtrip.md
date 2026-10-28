# Worked lifecycle: purchase, persist and apply an upgrade

## Purchase save reopen
Specify one purchase: opening credits 1,375, upgrade level 1, base capacity 20 plus 10 per level, and price 200 for level 2. Commit through the public purchase operation. Expected state is 1,175 credits and level 2; capacity derives to 40. Confirm the public consumer permits a load of 40 and rejects 41.

Save, close every fixture-owned process and storage handle, then start a fresh owner and load through the public path. Assert the same non-default credits, level and consumer behavior. Workspace B, initialized with distinct values, must remain unchanged. Do not infer durability from the still-live object or application from the mere presence of a stored row.

Use bounded wrong implementations: a no-op save fails the fresh load; restoring default level fails level and capacity assertions; a stale capacity consumer fails the 40/41 boundary even when storage is correct; double debit fails the credit assertion. Keep the original expected values unchanged when diagnosing a failure.

Record operation identity, commit receipt, closed owner, new owner, loaded values and consumer observations. A failed stage is not repaired by skipping it or rewriting its oracle to match the product.

[Test the same operation twice](knowledge:caliper/guides/tests/accounting#same-operation-retry)
[Control cancellation and ownership](knowledge:caliper/guides/tests/lifecycle-example#negative-controls)

## Format migration
Create an old-format fixture containing a non-default committed purchase identity and level. Load it with the current public reader and migrate only through the supported path. Preserve the committed identity, credits and level, derive capacity from that authoritative level, then save and reopen again. Assert the second load neither repeats the purchase nor migrates twice.

Retaining mutable level and mutable derived capacity creates two authorities. Prefer deriving capacity; if a compatibility field must remain, verify its declared invalidation/recomputation rule. Unsupported versions must fail without partially replacing the original fixture. A fixture with default values cannot distinguish a real migration from a reset.

[Review durable state ownership](knowledge:caliper/guides/code/state#durability)
