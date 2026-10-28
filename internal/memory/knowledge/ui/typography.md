# Typography as a working system

## Roles
Begin with real content: the longest title, a paragraph, a list, a number with a unit, a warning and a button. Assign roles before fonts. A display face may express the subject; body type must sustain reading; data needs distinguishable digits and reliable alignment. One well-chosen family can do all three. Add another only for a clear expressive or functional contrast.

Compare candidate fonts on those samples at the actual sizes and widths. Inspect letter shapes, punctuation, numbers, weight range and fallback behavior. Do not choose a fashionable family solely because it appears in a component starter. Avoid adding monospace micro-labels or widely tracked capitals without a role. A very large title, arbitrary italic word and colored fragment cannot substitute for a meaningful hierarchy.

[See type choices in an editorial composition](knowledge:caliper/guides/ui/editorial-example#type-and-space)
[Inspect a concrete type specimen and its alternatives](knowledge:caliper/guides/ui/typography#worked-specimen)

## Hierarchy
Choose a short scale tied to the content: page title, section title, body, supporting text and data emphasis. Establish distinction with size, weight, placement and spacing together. Do not make every distinction a new font or color. If the section title is weak, first remove competing emphasis rather than increasing everything.

Set line height and measure by reading full paragraphs, not isolated lorem ipsum. Keep captions close enough to belong to their asset and far enough from neighboring content to avoid confusion. Use tabular figures for changing numbers when available; keep units visible and less prominent than the quantity. Long numeric values need room, not shrinking text on every update.

At a narrow viewport, reduce the display scale and adjust line length; preserve reading order and essential labels. Balance line breaks where they improve a specific heading, but avoid fixed breaks that become awkward on another screen. Truncation should preserve recoverability and never conceal the primary action or a critical error.

[Use layout to reinforce reading order](knowledge:caliper/guides/ui/layout#sequence)

## Verify and refine
Check the actual loaded font, not just a CSS family declaration. Test an unavailable-font fallback, long names, Unicode, increased text size, high zoom and mixed content. Look for cramped descenders, orphaned words, weak small text and confusing symbols. Check labels on controls in their disabled, selected and focused states.

A good specimen has an obvious reading sequence without relying on color alone, a calm body rhythm and a distinctive voice appropriate to the subject. A weak specimen looks equally loud at every level or becomes elegant only after replacing real content with short filler. Keep the actual content and repair the system.

[Run a focused visual critique](knowledge:caliper/guides/ui/critique#inspection)

## Worked specimen
For an illustrated reading archive, try a 48px display title, 28px section titles, 18px body with 1.55 line height, and 14px metadata. Use an expressive serif for display and a restrained sans for body only if the samples show a useful contrast. Treat these as starting values for this example, not a universal scale. Set a paragraph measure around 60 characters, then judge the actual font and content.

Build one specimen with a two-line title, a 150-word paragraph, a caption, a record dated "26 October 2026", a quantity "1,240.5 km" and an action "Compare selected observations". Compare it with an all-sans version using the same content. The serif version earns its place if it expresses the archive's character while keeping the sequence calm; the all-sans version may win when compact comparison dominates reading.

Reject a version whose 72px title forces the first useful record off the screen, whose 11px metadata becomes hard to read, or whose decorative monospace labels add a third voice without clarifying data. Repair those roles rather than adding more font families. At narrow width, try a 34px title and single-column records while keeping body readability. Recheck wrapping instead of assuming the proposed numbers work.

Disable the preferred font and inspect the fallback. Compare the longest label, punctuation, numerals and an accented name. Keep the action readable and the quantity aligned; avoid fixed-height boxes that clip the fallback. The accepted output is a specimen with a reasoned choice and observed states, not merely a list of CSS tokens.
