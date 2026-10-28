# Worked oracle: swept clearance

## Swept envelope
Define the contract before choosing the inequality. A circular obstacle of radius r moves with maximum center excursion A around an anchor at distance D from the center of an exclusion disk of radius R. The entire obstacle must remain outside the exclusion disk throughout its motion. Its minimum surface distance from the exclusion center is D−A−r. Therefore require D−A−r ≥ R, equivalently D ≥ R+A+r. Use strict > if the product forbids tangency.

Derive this oracle from the geometry, independently of the implementation. With R=460, r=20, A=40, the threshold is 520: 519 is unsafe, 520 depends on the declared tangency rule, and 521 is safe. A predicate that only rejects D < R−(r+A) misses unsafe placements. Checking the anchor alone also misses the inward extreme.

Translate both centers by the same vector; the answer must not change. Exercise inward and outward extremes and the actual collision consumer. For a noncircular motion bounded by A, the inequality is conservative; do not claim it is an exact necessary condition unless the trajectory reaches the inward extreme. With fixed-point coordinates, specify units and rounding. With floats, derive any tolerance from the application's scale instead of adding an arbitrary epsilon to make a failure pass.

This proves clearance under these assumptions, not general collision correctness. Record radii, coordinate units, motion bound, tangency convention and observed result. A wrong sign, ignored collision radius and anchor-only check must each fail a distinguishing case.

[Build an independent oracle](knowledge:caliper/guides/tests/oracles#independence)
[Choose discriminating fixtures](knowledge:caliper/guides/tests/fixtures#discriminating-data)
