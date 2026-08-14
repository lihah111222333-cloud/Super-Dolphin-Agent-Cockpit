# Prototype Brief

## Hypothesis

Applying one exact 67%-opaque frosted-glass material to user messages, assistant messages, and the composer will visually integrate the interactive chat layer with the animated ocean while preserving readable hierarchy in both themes.

## Primary user flow

The user opens an existing conversation, reads both sides of the exchange through the moving ocean backdrop, then focuses and uses the composer without a material discontinuity.

## Success criteria

- User and assistant bubble backgrounds resolve to an alpha value of 0.67 in the real browser.
- The composer card background resolves to the same 0.67 alpha value.
- All three surfaces use backdrop blur and saturation, while borders and text remain readable in dark and light themes.
- The material remains scoped to the ocean chat prototype and does not alter unrelated pages.
- Focused and full frontend checks pass.

## Fidelity and data mode

High visual fidelity using the real message DOM, composer, themes, and ocean animation. No mock data or alternate component path.

## Out of scope

- Redesigning bubble geometry, timestamps, actions, or composer controls.
- Applying frosted glass globally outside the ocean prototype.
- Changing message or composer behavior.
