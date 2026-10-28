# Worked invariant: account for committed effects once

## Same operation retry
For a purchase costing 200 from an opening balance of 1,375, submit one operation ID and confirm the committed balance is 1,175. Retry that same accepted ID before and after closing and reopening all owners. The balance remains 1,175 and the upgrade is applied once. A new permitted purchase ID is a distinct operation with its own next-level price; do not confuse legitimate repeated purchases with retries.

Derive the oracle from the ledger: closing balance = opening balance − sum(prices of unique committed purchases) + sum(unique committed refunds). The test calculates this from its authored inputs and committed operation identities, not by invoking the product's balance-calculation helper. The upgrade consumer must also observe exactly one application.

Distinguish duplicate debit, duplicate capacity application, debit committed before an otherwise failed purchase, and a refund applied twice. Each breaks a different assertion. Test cancellation before commit separately from cancellation after commit. After commit, report the committed result; cancellation does not invent rollback. A retry with the same ID but different payload must follow the product's explicit conflict policy, not silently mutate the old operation.

For a concurrent retry, synchronize two callers at a documented boundary and inspect settled owners and committed entries. A timeout is not evidence of rejection. Keep the original record and both results so a late response cannot conceal duplicate work.

[Follow purchase through public save and load](knowledge:caliper/guides/tests/upgrade-roundtrip#purchase-save-reopen)
[Construct a controlled race](knowledge:caliper/guides/tests/state#concurrency)
[Apply invariants and transformations](knowledge:caliper/guides/tests/oracles#invariants-and-transformations)
