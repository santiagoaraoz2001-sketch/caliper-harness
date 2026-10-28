# Testing the interface the user actually uses

## Public path
Enter through the public action: a command, endpoint or visible control. Assert the meaningful result at the consumer boundary. For a toggle, verify the behavior it changes rather than only the stored boolean. For a search, select distinguishable data and assert the visible results. For a save, reopen through the actual reader.

A DOM element's existence is not proof that it is visible or usable. Check parent visibility, disabled state, modal state, focus and the action's resulting feedback. A private function call can support diagnosis, but it should not be presented as the same evidence as clicking the actual control.

[Apply this to coherent component behavior](knowledge:caliper/guides/ui/components#behavior)
[Work through a control with an observable consumer](knowledge:caliper/guides/tests/interface#worked-control)

## Interaction states
Choose representative happy, empty, invalid, loading and failure states. Exercise cancellation without committing the draft, then confirm a valid action. Verify keyboard and pointer paths agree. For commands in a composer, ensure a selection handler does not consume command submission as an unrelated activation.

For overlays, check focus entry, focus containment where required, Escape behavior and focus restoration. Test long content and narrow layouts that can hide actions. For stateful interfaces, verify the next screen uses the committed value; a success toast alone is weak evidence.

## Visual and semantic evidence
Use screenshots to inspect composition and visible states, and semantic assertions to prove behavior. Pixel equality can catch unintended changes in a stable environment, but it cannot decide whether a design is excellent. A screenshot that has never been inspected is an artifact, not a visual review.

Capture the exact viewport, state, version and action sequence. If a debug shortcut bypasses normal navigation, disclose it and keep normal-entry coverage separate. Do not claim a screen was reviewed when only CSS or source code was read.

[Use a design critique alongside functional checks](knowledge:caliper/guides/ui/critique#inspection)

## Failure interpretation
When a UI test fails, inspect whether the control was absent, hidden, disabled, intercepted or wired incorrectly. Avoid increasing sleeps as the first response. Wait for the actual state transition and retain the last visible state when it fails. Distinguish automation-controller limitations from product behavior, and leave unsupported paths unverified rather than substituting a private call.

## Worked control
Suppose a visible "Show completed" setting should change which records appear. Seed one active record named "Inspect valve" and one completed record named "Replace filter". Open the actual list. Toggle the visible setting with its accessible control, then assert the rendered list contains both records. Toggle it off and assert only the active record remains. Also verify the selected state and count agree with the visible list.

This test rejects a handler that changes the setting but never refreshes the consumer, and a renderer that always returns every record. A test that asserts only the setting's boolean rejects neither. Exercise the keyboard path separately if it uses different dispatch, and check that focus remains on a meaningful control after the list updates.

If the setting is promised to persist, commit it, close the application owner, reopen the same workspace and enter through the normal list screen. Verify the completed record is visible again. If the setting is only a local draft until Apply, cancellation must restore the old list and leave durable state unchanged. These are different contracts; choose the one actually promised.

Use a test-only stale consumer to show the visible assertion fails even while storage is correct. Keep the data and action identical between valid and wrong controls. Capture the failed visible state and exact action sequence; a successful screenshot capture alone is not the assertion.

[Extend the same test to persistence and stale ownership](knowledge:caliper/guides/tests/lifecycle-example#sequence)
