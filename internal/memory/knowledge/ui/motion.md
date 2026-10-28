# Motion as explanation and expression

## Purpose
Name the transition before animating it: a panel arrives, an item changes position, an action commits, a value changes, or an object responds to input. Motion can explain continuity or express a deliberate physical character. If it explains neither, it is likely decoration competing with the task.

Choose timing, distance and easing as a family. A precise instrument may use short restrained transitions; a playful object may have elastic movement, but its controls still need prompt feedback. Do not delay access for an entrance sequence. Repeated scroll fades, floating decorations, cursor glows and automatic marquees often distract because they animate without a state change.

[Tie motion to the design direction](knowledge:caliper/guides/ui/direction#translate-into-rules)

## Interaction contract
Separate immediate acknowledgement from longer completion. A button can show a pending state while work continues; it should not appear completed merely because the animation ended. Repeated input, interruption, cancellation and navigation must leave coherent state. Prefer transitions that can start from the current rendered state rather than snapping back to an assumed beginning.

Define what reduced motion means for each essential effect. Replace spatial travel with a direct change or a restrained opacity transition where appropriate. Preserve all information and control access. Do not communicate success, danger or location only through movement.

Hover should clarify affordance without making essential text or controls disappear. Provide equivalent focus behavior. Avoid large moving backgrounds behind reading content and changes that trigger layout repeatedly when a cheaper compositing approach is sufficient.

## Verify
Inspect the actual transition at normal and reduced-motion settings, with repeated actions and slower execution. Confirm focus, pointer targets and visible status remain coherent during the transition. Measure frame behavior if performance is a concern; elapsed total time alone does not identify the cause of jank.

For a game or simulation, separate intended world animation from interface motion. Menus, overlays and settings need predictable interaction even when the world continues moving. A visually exciting scene does not excuse unreadable instructions or a focus trap.

[Test transitions through public behavior](knowledge:caliper/guides/tests/state#transitions)
[Inspect rather than infer visual quality](knowledge:caliper/guides/ui/critique#inspection)
