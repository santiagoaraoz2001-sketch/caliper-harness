# A design system that carries behavior

## System
Build a few representative components from the chosen identity before proliferating variants. A button, input, selectable item, content group and dialog often expose most conflicts. Define spacing, text roles, corner and border roles, and state behavior once. Use reliable accessible primitives when they fit; adapt their presentation to the product. A copied library gallery is not a design language.

Component boundaries should follow repeated behavior and meaning. Share a button's keyboard and disabled behavior; do not force unrelated content into a universal card with dozens of switches. Add a variant when it expresses a recurring distinction, such as destructive action or compact density. Keep one-off composition at the page level.

The specimen should show real labels, long content and errors. Evaluate whether someone could create a new screen using the rules without inventing new spacing or color choices. Record a deliberate exception with its reason instead of silently drifting.

[Derive system rules from direction](knowledge:caliper/guides/ui/direction#translate-into-rules)

## Behavior
For a changed feature, trace the actual public path: visible control, event handler, state or service, and visible outcome. A toggled boolean does not prove the intended capability. Verify parent visibility, modal state, persisted values and the next action. A control that promises unavailable behavior should be removed or labelled honestly.

Use labels that describe the result. Confirmation and feedback must use the same nouns and units. Explain prerequisites where the user needs them, and ensure the handler enforces the same rule. "Continue" should not mean different actions at adjacent steps. Empty states should explain the absence and offer a real next step; errors should retain the user's work when possible.

[Test the public interaction rather than a private flag](knowledge:caliper/guides/tests/interface#public-path)

## Access and state
Prefer native semantics for buttons, links, headings and inputs. Make focus visible, keep Tab order meaningful, and preserve keyboard access when shortcuts exist. Dialogs need an accessible name, a clear close path, appropriate focus containment and focus restoration. Escape should cancel the intended layer without accidentally executing a background action.

Specify loading, empty, error, selected, disabled and partial-data states alongside the happy path. Prevent duplicate actions while an operation is pending when repetition would be harmful; expose cancellation or progress when useful. A responsive state must still allow touch, keyboard and zoom users to complete the task.

[Choose motion that explains transitions](knowledge:caliper/guides/ui/motion#purpose)
[Review a coherent operational specimen](knowledge:caliper/guides/ui/instrument-example#components)
