# Testing transitions, persistence and concurrent ownership

## Transitions
Model the externally meaningful states and legal transitions, not every private boolean. For a job, these might be idle, running, cancelling, succeeded and failed. Specify what each transition commits and what remains recoverable. Test success, one meaningful failure, cancellation before commit, and cancellation after commit where behavior differs.

Observe the actual owner and committed state. A "cancel requested" message is not proof that work stopped. A queued input is not proof that the active operation consumed it. Separate submission, durable ownership and subsequent application in the evidence.

[Follow an explicit save/cancel/reopen sequence](knowledge:caliper/guides/tests/lifecycle-example#sequence)

## Persistence
Change several non-default valid values, save through the public path, close the owner, reopen the same storage and read through the public path. Verify both values and identity. Also test an interrupted or failed save if the contract promises atomicity.

Compare state that matters to the user, including optional values, order and ownership. A check that only finds a database row cannot prove the reader uses it. A check that reads the in-memory object after "save" cannot prove durability.

[Distinguish storage from application](knowledge:caliper/guides/code/state#durability)
[Verify an upgrade through purchase, save and fresh load](knowledge:caliper/guides/tests/upgrade-roundtrip#purchase-save-reopen)

## Concurrency
Use explicit synchronization to create the race being tested. For compare-and-swap, read the same version twice, commit one update, then attempt the stale update and verify rejection without partial effects. For duplicate execution, hold the first operation at a known boundary and submit the second. Assert one owner and one committed effect.

Prefer barriers, channels or controlled fake dependencies to arbitrary sleeps. A timeout is a safety bound, not proof that an event occurred. After cancellation, wait for an observable owner exit or a documented settled state. Preserve error and cleanup evidence if a deadline expires.

## Ownership
Verify that one project's state cannot be read or changed through another project's scope. Check that retry creates or resumes the intended owner according to the contract, and that late results from an old owner cannot overwrite a new one. Test cleanup using owned identities rather than names shared with external services.

[Build negative controls for these races](knowledge:caliper/guides/tests/lifecycle-example#negative-controls)
