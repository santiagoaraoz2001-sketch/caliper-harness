# Worked direction: an operational instrument

## Direction
Example brief: an operator watches a small set of changing measurements, notices exceptions and adjusts a safe operating target. The design needs calm scanning, precise labels and unmistakable state. Choose an instrument direction with stable alignment, compact grouping, restrained surfaces and one focal action.

This does not require a black background, neon glow or a science-fiction font. Those choices can fit a fictional world, but they are not evidence of precision. Derive the visual character from the actual environment and audience.

[Translate the brief into enforceable design rules](knowledge:caliper/guides/ui/direction#translate-into-rules)

## Layout
Keep current state, target and deviation adjacent. Align comparable values on common columns and keep units visible. Reserve a distinct region for exceptions instead of coloring the whole surface red when one value changes. Put the adjustment control beside its preview and show whether the change is pending or applied.

A weak version places each measurement in an equally prominent glowing card, duplicates labels and moves values as their length changes. Improve it with tabular numerals, stable widths, quiet group dividers and a clear summary. The primary reading sequence becomes state, exception, action.

[Set density and grouping deliberately](knowledge:caliper/guides/ui/layout#grid-and-density)

## Material and state
Use a limited palette with separate meanings for selection, pending changes, warning and failure. A normal high value is not an error merely because it exceeds an arbitrary visual threshold. State semantics come from the product's rules. Use text and icons alongside color when the distinction matters.

Keep borders for real group boundaries and elevation for overlays. A subtle accent can identify the active control. Large soft glows reduce edge clarity and should earn their presence through an explicit fictional or material concept.

[Build a state specimen and verify contrast](knowledge:caliper/guides/ui/color#states-and-verification)

## Components
The adjustment control supports keyboard and pointer input, displays the unit and valid range, and gives a clear committed result. A cancelled edit restores the previous value. A service error retains the proposed value while explaining that it was not applied. An unavailable operation remains distinguishable from an idle one.

Narrow layouts preserve the current state and primary action, moving secondary history below them. Do not compress labels until the operator must guess what a number means.

[Test cancellation and persistence as distinct transitions](knowledge:caliper/guides/tests/state#transitions)

## Acceptance
Inspect steady state, a real exception, unavailable data, pending adjustment, cancellation, failed application and narrow layout. Check whether the operator can distinguish measured, desired and committed values. Evaluate motion for continuity and prompt feedback. A visually dramatic dashboard that confuses those states has failed its brief even if every component looks attractive.
