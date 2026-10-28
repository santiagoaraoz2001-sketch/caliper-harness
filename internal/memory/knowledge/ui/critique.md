# Judging and improving the actual design

## Inspection
Inspect the real rendered product at named viewport sizes with representative content. Source code, a screenshot file's existence and a successful build are different evidence from seeing the surface. If visual inspection is unavailable, say so and limit the conclusion to what was checked.

First assess reading order and the primary task. Then inspect typography, spacing, alignment, color, asset treatment and states. Finally exercise actual controls, including parent visibility and modal state. Check narrow width, long content, keyboard focus, empty/error/loading states and text enlargement where applicable. Contrast measurements need the actual composited background. A timing number can establish a delay; it does not establish its cause.

Record concrete observations: "the primary action is below the fold at this size," "the subtitle competes with the title," or "the selected record is indistinguishable without color." Avoid empty verdicts such as "looks premium" or "needs polish."

[Trace an interaction to a visible result](knowledge:caliper/guides/tests/interface#public-path)

## Identity
Compare the result with the recorded design language and the brief. Does it feel specific to this audience and subject? Are expressive decisions consistent across components and states? Could the same layout, icons and copy be pasted onto an unrelated product unchanged?

Look for habitual treatments: identical rounded cards for unrelated meanings, soft glows on every surface, arbitrary glass, generic purple gradients, centered slogan-and-two-button heroes, interchangeable icon grids, oversized empty spacing and decorative mono labels. These are questions about intent, not forbidden styles or an AI detector. Keep a treatment when it expresses the chosen concept; remove or replace it when it obscures structure.

[Revisit the direction if local polish cannot solve the problem](knowledge:caliper/guides/ui/direction#originality-with-purpose)

## Improvement loop
Choose the highest-impact mismatch, state its cause as a hypothesis, and change the smallest coherent part of the system. If hierarchy is weak, changing every color and font at once prevents learning. If the direction itself is wrong, many tiny spacing changes will not rescue it.

Compare before and after using the same content and viewport. Check whether the primary task is clearer and the identity more coherent without losing functionality. Retain useful evidence; rerun only affected checks after a local change. A beautiful landing view does not verify deeper states, and a functional test suite does not establish visual excellence.

Stop when the requested product and meaningful states meet the brief with evidence, rather than inventing endless polish tasks. Leave limitations explicit. Good judgment includes knowing which refinement changes the user's experience and which merely consumes time.
