# Measuring useful work and removing waste

## Measurement
Name the question before measuring: startup latency, response time, whole-frame cost, memory, request volume or repeated verification. Record the exact scope, input and environment. A synchronous helper duration does not represent the whole frame; cached input still has processing and coordination cost even when billing differs.

Compare like with like. A longer run that produces a materially better product may be worthwhile. A short run that omits required behavior is not an improvement. Separate useful planning and new evidence from repetition with unchanged inputs.

## Diagnosis
Locate the cost with evidence before changing architecture. Count calls and bytes, inspect ownership and compare changed versus unchanged work. If a reviewer rereads the same files, determine whether the relevant evidence is absent, inaccessible, stale or simply not represented in its current decision packet. Adding another reviewer is unlikely to fix missing progress facts.

For rendering, distinguish repeated layout, painting, data recomputation and external waits. For storage, inspect actual query shape and returned volume. Do not infer a bottleneck's cause from one changed sample; isolate the factor or label the explanation a hypothesis.

[Keep evidence separate from unsupported conclusions](knowledge:caliper/guides/tests/execution#cost)

## Reduction
Remove unnecessary work before adding caching, concurrency or dependencies. Reuse current immutable results when their source/version is unchanged. Bound large output and expose a useful retrieval path rather than flooding context. Retrieve a relevant graph section and its next links instead of loading the whole library.

A cache needs an explicit key and invalidation rule. Concurrent execution needs independent owners or synchronization. Both can make the system harder to reason about if added before the cost is understood. Keep a simple correct path and test the invalidation or race that the optimization introduces.

## Acceptance
Measure before and after on matching workloads and inspect correctness. Report the observed improvement and remaining scope. Preserve worst-case or failure-path behavior when it matters. A reduction in lines or tokens is useful only if the resulting system remains understandable and does not shift work into repeated recovery.
