# Prototype Brief

## Hypothesis

The accepted ocean chat experiment can become the default production chat presentation without changing chat data, actions, runtime truth, accessibility ownership, or backend contracts.

## Primary user flow

A user opens Chat, reads and sends messages over the day/night ocean atmosphere, sees the ocean enter and leave its active state with real AI work, and continues to use notices, the Agents board, and the composer normally.

## Success criteria

- The ocean presentation is integrated on the latest `origin/main` without losing intervening upstream chat changes.
- User and assistant bubbles plus the composer retain the accepted 67%-opaque frosted-glass material in both themes.
- Existing message, composer, notice, Agents board, responsive, reduced-motion, and AI active/idle behavior remain covered.
- LSP diagnostics, frontend lint, the full test suite, and production build pass after integration.
- The productionized change is committed on `main` with unrelated local work preserved.

## Fidelity and data mode

Production fidelity using the real React chat page, real store/runtime state, existing theme tokens, and existing backend adapters. No mocks, feature-only route, new persistent state, or alternate data path are introduced.

## Out of scope

- Redesigning the sidebar, terminal, runtime panels, or chat information architecture.
- Changing DTOs, RPC methods, persistence, providers, or backend behavior.
- Introducing new visual libraries, raster assets, or a user preference for disabling the ocean theme.
