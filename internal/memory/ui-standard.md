# Caliper UI standard

## identity
Reuse project/design/identity. If absent, choose and record a direction from the brief. Honor preferences; preserve unrelated identity facts.
[Develop a design language](knowledge:caliper/guides/ui/direction#brief-to-direction)

## typography
Choose fonts for the subject and reading conditions. Define heading, body and data roles with a deliberate scale, readable measure and reliable fallbacks. Avoid fashionable type combinations without a reason; test real content and long labels.
[Build type roles and a readable hierarchy](knowledge:caliper/guides/ui/typography#roles)

## hierarchy
UI excellence starts with a clear primary task, current state and next action. Establish reading order through size, position, contrast and grouping. Supporting detail should support, not compete. Distinctive art direction and familiar interaction can coexist.
[Compose around the user's sequence of questions](knowledge:caliper/guides/ui/layout#sequence)

## layout
Choose density, columns and spacing for actual content. Use cards for meaningful independent groups; use alignment and typography for comparison. Remove purposeless sections, oversized empty padding and repeated template patterns. Preserve essential relationships as the viewport narrows.
[Design grids, density and responsive composition](knowledge:caliper/guides/ui/layout#grid-and-density)

## color
Use a small semantic palette derived from the identity. Distinguish accent, selection and status; keep labels or shape alongside important color meanings. Gradients, dark neon and beige are choices to justify, not default identities.
[Specify palette, material and state](knowledge:caliper/guides/ui/color#palette)

## components
Build product-specific composition on reliable accessible primitives. Assign radius, border and elevation roles instead of applying identical rounded cards, soft glows or glass everywhere. Every visible control needs a supported action and observable result. Keep component states coherent.
[Create a system from representative components](knowledge:caliper/guides/ui/components#system)

## imagery
Choose assets that explain the product, subject or world. Compare real captures, photography, illustration, vectors and procedural graphics. Avoid unrelated orbs, sparkles, emoji icons and stock filler. Never fabricate customers, screenshots or metrics as real evidence.
[Choose assets and write a consistent asset brief](knowledge:caliper/guides/ui/assets#choice)

## motion
Animate meaningful transitions with timing and character derived from the identity. Avoid delaying access or distracting from controls. Keep equivalent feedback for keyboard and reduced motion; essential information must not require hover or movement.
[Design purposeful motion and interruption behavior](knowledge:caliper/guides/ui/motion#purpose)

## copy
Use specific content and action labels. Instructions must agree with actual prerequisites and handlers. Explain units, consequences and recoverable errors. Replace interchangeable promises and decorative jargon with useful information; ordinary punctuation is not evidence of AI authorship.
[Connect labels, prerequisites and visible outcomes](knowledge:caliper/guides/ui/components#behavior)

## behavior
Trace entry control to handler, state or service, and visible effect. A stored flag alone proves no capability. Check repeated actions, cancel/retry, failure and save/load where relevant. Link meaningful feature decisions to responsible code and observed evidence; keep planned, implemented and verified distinct.
[Test through the actual public interaction](knowledge:caliper/guides/tests/interface#public-path)

## access
Use semantic controls, accessible names and visible focus. Keep keyboard navigation usable alongside shortcuts. Handle dialog focus and restoration. Test narrow layouts, zoom, long content and reachable empty/error/disabled states; prevent overlaps and inaccessible actions.
[Specify access and state alongside components](knowledge:caliper/guides/ui/components#access-and-state)

## verify
Inspect the rendered surface at named viewports against the brief and identity. Check parent visibility and modal state through actual interactions. Contrast needs the actual composited background. Source inspection is not visual review; disclose untested states and unexecuted examples.
[Critique the result and improve the highest-impact mismatch](knowledge:caliper/guides/ui/critique#inspection)
