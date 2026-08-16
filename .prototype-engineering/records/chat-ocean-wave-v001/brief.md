# Prototype Brief

## Hypothesis

Adding a restrained, theme-aware ocean atmosphere to the existing chat panel will make the workspace feel more distinctive and spatially layered without reducing message/composer readability or changing chat behavior.

## Primary user flow

The user opens Chat, sees the ocean atmosphere behind the welcome state, chooses a suggestion or project, then continues using the unchanged composer and message timeline while the atmosphere becomes quieter behind active conversation content.

## Success criteria

- The chat panel visibly echoes the reference specimen through a luminous sky field and at least three wave layers.
- Existing chat controls, suggestions, composer, messages, and side-panel shortcut remain reachable and readable.
- Light and dark themes both retain sufficient text/control contrast.
- Motion is decorative, pointer-transparent, and disabled under `prefers-reduced-motion`.
- Desktop and compact layouts avoid overlap, horizontal overflow, and clipped composer controls.
- Frontend lint, tests, build, LSP diagnostics, and browser scenario checks pass.

## Fidelity and data mode

High visual fidelity for the atmosphere and low behavioral fidelity: use real existing chat UI/store bindings, no mock message protocol, and deterministic browser states limited to intro, active-chat-shaped DOM coverage, compact width, and reduced-motion CSS coverage.

## Out of scope

- Day/night preference persistence or a new appearance setting.
- Changes to backend RPC, thread/turn lifecycle, composer behavior, or message serialization.
- Recreating the specimen control panel or its exact typography.
- Production hardening, analytics, feature flags, or performance telemetry beyond the prototype checks.
