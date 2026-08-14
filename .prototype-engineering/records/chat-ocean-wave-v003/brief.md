# Prototype Brief

## Hypothesis

The ocean background can become an informative ambient system: active AI work increases wave energy, idle state calms it, wave contours stay physically clipped to each water band, and transient overlays/agent status remain spatially independent from the chat scroll canvas.

## Primary user flow

The user opens an active conversation, starts an AI turn, observes the ocean transition into an energetic state, sees it return to idle after completion, and can still use notices, the Agents board, messages, and composer without overlap or displaced overlays.

## Success criteria

- Existing runtime truth adds/removes an ocean-active class without DOM probing.
- Active and idle wave motion use visibly different amplitude/speed while respecting reduced motion.
- Day/night switching coordinates the sky, celestial body, stars, clouds, light path, and sea palette with the reference scene timing.
- White contour patterns never render outside their owning wave surface.
- Notice overlays stay inside the chat canvas with a consistent edge gutter.
- Agents board remains an overlay and does not reserve timeline layout space; its design intent is documented.
- Dark/light real-browser screenshots and focused/full frontend checks pass.

## Fidelity and data mode

High visual and interaction fidelity using the real Wails-backed conversation and existing runtime/agent state. No mocked backend or alternate status source.

## Out of scope

- Changing agent orchestration semantics or the timeline scroll ownership.
- Redesigning the full notification system or Agents board content.
- Adding persisted animation intensity settings.
