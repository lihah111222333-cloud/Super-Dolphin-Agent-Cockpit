# Prototype Brief

## Hypothesis

Removing the full-canvas active-conversation overlay will eliminate the visible horizontal masking seam while preserving message readability through local message and composer surfaces.

## Primary user flow

The user opens an existing conversation in either dark or light mode, scrolls the chat history, and sees one continuous ocean atmosphere behind the timeline without a rectangular overlay beginning at the chat-layout boundary.

## Success criteria

- `.conversation` is transparent in active mode and has no backdrop filter.
- The sky, horizon, and waves remain continuous across the chat header/timeline boundary in both themes.
- User and assistant messages plus the composer remain readable and interactive.
- Intro mode remains visually unchanged.
- Focused regression tests, full frontend checks, LSP diagnostics, and real-browser screenshots pass.

## Fidelity and data mode

High visual fidelity using the user's real active conversation and existing theme toggle; automated coverage uses the repository's active-thread React fixture and direct CSS assertions.

## Out of scope

- Redesigning message bubbles, chat header geometry, or composer placement.
- Changing thread data, backend behavior, or persisted appearance settings.
- Altering the ocean palette or animation timing beyond what is required to remove the seam.
