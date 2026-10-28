# Worked examples of tests that can fail for the right reason

## Geometry
Suppose a contract says a moving point collides with a circle only when it approaches and reaches the boundary during the interval. Put the circle at the origin with radius 2, the point at x=5, and horizontal velocity -4 for one time unit. The point approaches, reaching x=2 after 0.75 units. A separate case with velocity +4 moves away and must not report that impact.

The expected time comes from distance 3 divided by speed 4, not from calling the production intersection helper. Add a translated equivalent case and an explicit tangent/boundary convention from the specification. A random fixture that only generates outward motion can report many passes while never testing an impact.

If the product fails the inward case, inspect its coordinate and sign conventions. If the test accidentally used outward motion, repair the fixture from this derivation and preserve the original mistake. Do not reinterpret the product's output as the definition of correctness.

[Design representative fixtures](knowledge:caliper/guides/tests/fixtures#discriminating-data)

## Constant output
Suppose a persistence test saves the default theme and then reads "default." An implementation that performs no write and always returns the default passes. Use a non-default valid theme and a second independent setting, close the store, reopen it and read through the public API. Also check another workspace retains its distinct values.

Inject a no-op writer in a controlled fixture. The strengthened test must fail. This demonstrates sensitivity to lost persistence; it still does not prove atomicity during a crash, which needs separate evidence.

[Extend to cancellation and stale updates](knowledge:caliper/guides/tests/lifecycle-example#sequence)

## Required stage
Suppose a wrapper prints "browser stage SKIP: dependency unavailable" and exits zero. A harness that treats exit zero as full success produces a false qualification. The oracle is the declared required-stage contract: browser evidence was required and absent.

Create one fixture where the required stage executes and passes, one where it fails, one where it skips with exit zero, and one where no stage is discovered. Only the first may satisfy that gate. Keep an optional-stage skip separate so the system remains useful without pretending optional work was executed.

[Record stage outcomes alongside process status](knowledge:caliper/guides/tests/execution#required-stages)

## Consumer behavior
A settings test that asserts a stored boolean proves storage only. If the feature is "mute audio," enter through the visible setting and observe the audio consumer under a known sound-triggering event. The output must change, then restore after unmuting. A stale consumer cache should fail even if the database contains the correct value.

Use this pattern for real features: name the consumer and the externally meaningful effect before choosing the assertion.
