# State that survives alternate callers and recovery

## Transitions
Describe valid states and the operations that move between them. Put the transition rule at the owner, not in the UI label. Separate an intent from its committed effect and from the consumer's subsequent use. A queued save is not durable; a durable setting is not necessarily applied by a stale consumer.

Use a transaction or appropriate synchronization when multiple changes form one logical commit. Validate the whole proposed state before replacing live state. On failure, preserve the prior valid state and report the rejected operation. Avoid partially mutating an object while parsing an untrusted saved file.

[Test transitions and ownership independently](knowledge:caliper/guides/tests/state#transitions)

## Durability
Save enough information to reconstruct meaningful behavior, including relationships, units and time semantics. Decide whether a deadline is an absolute time or a remaining duration and preserve that meaning across restart. Reopening a fresh object with default values does not test durable state.

Keep a version or identity when stale updates can overwrite newer work. Compare-and-swap should reject the entire stale update, including linked records, rather than commit part of it. Historical readers should remain small and explicit when formats evolve. Migrate only with preserved evidence and a way to identify the source version.

[Use non-default data through save and reopen](knowledge:caliper/guides/tests/lifecycle-example#sequence)

## Repeated effects

[Prove a retry does not debit or apply an upgrade twice](knowledge:caliper/guides/tests/accounting#same-operation-retry)
Define the idempotency boundary. Retrying a read is different from retrying a payment, file write or process launch. A retry should either reuse the committed result or execute only the uncommitted operation, according to the contract. Do not infer this from a friendly status string.

Track effects by the actual owner and operation identity. Late callbacks from an old owner must not mutate the new owner's state. Cancellation needs a settled outcome that says whether an effect committed. Never claim rollback merely because the cancellation request was accepted.

[Handle cancellation and resource cleanup](knowledge:caliper/guides/code/failures#cancellation)
