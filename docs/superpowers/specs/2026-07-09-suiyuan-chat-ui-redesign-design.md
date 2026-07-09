# Suiyuan Chat UI Redesign Design

Date: 2026-07-09
Status: Approved design draft, awaiting user review before implementation planning

## Context

The Stitch project `Codex Style Main Console` contains a Suiyuan design system named `Luminous Minimalist` and a set of Suiyuan UI preview screens. The design system's `theme.designMd` has been exported to the repository root as `DESIGN.md`.

The current product UI source is `frontend-app`. The UI pages are routed from `frontend-app/src/AppRoutes.jsx`; the current Chat surface is implemented under `frontend-app/src/pages/chat`, with global tokens in `frontend-app/src/styles.css` and shell styling in `frontend-app/src/AppShell.css` / `frontend-app/src/AppChrome.css`.

During brainstorming, the chosen direction was **B3: Stitch-faithful high fidelity Chat redesign**. This means a stronger visual and layout migration than a token-only pass, but still scoped to the app shell and Chat workflow.

## Goals

1. Bring the primary app shell and Chat workflow close to the Suiyuan Stitch preview language.
2. Use `DESIGN.md` as the source for color, typography, spacing, radius, and elevation decisions.
3. Preserve the existing frontend data flow, API bridge behavior, thread state, runtime state, and fail-fast error behavior.
4. Make the Chat surface feel like a warm, calm, high-productivity work canvas rather than the current dark blue/purple console.
5. Keep implementation testable with existing React/Vite checks and focused Chat interaction coverage.

## Non-Goals

1. Do not migrate Skills, Prompts, Memory, Files, Workflows, Observability, or Settings in this pass.
2. Do not modify backend RPC contracts, store persistence, thread lifecycle, provider configuration, or runtime event payloads.
3. Do not paste Stitch-generated HTML directly into production React.
4. Do not introduce Tailwind, shadcn, or another UI framework.
5. Do not hide missing project, cwd, provider, thread, or runtime data behind visual fallback states.

## Design Source

Use these Suiyuan tokens and principles from `DESIGN.md`:

- Primary brand color: `#a03b00` / burnt sienna, plus `#792b00` for deeper primary action states.
- Main background: `#fbf9f2` and `#fbf9f3`.
- Raised card surface: `#ffffff`.
- Container surfaces: `#f5f4ed`, `#f0eee7`, `#eae8e1`, and `#e4e3dc`.
- Text: `#1b1c18` primary, `#584238` variant, `#8b7268` outline/secondary.
- Typography: Inter, with 14px default UI body, 18px long-form reading body, and measured label styles.
- Layout: 4px baseline grid, 24px gutters, 32px desktop margins, 280px left navigation language, and 1100px max content canvas.
- Elevation: tonal layering plus ambient shadows, especially lifted cards and floating composer input.

The implementation should translate these values into the existing CSS token system rather than adding a parallel styling framework.

## Target Architecture

The target Chat workbench is a three-zone composition:

1. **Left Suiyuan navigation / thread entry**
   - Use a warmer, wider, more legible navigation treatment.
   - Active state uses a sienna indicator and primary-fixed wash instead of the current blue/purple accent.
   - Existing navigation state, route handling, badges, and thread selection behavior remain unchanged.

2. **Centered Chat canvas**
   - The main conversation sits on a warm `surface-bright` canvas.
   - Content is constrained for reading comfort, with message cards aligned within a centered work area.
   - Intro state, active thread state, scroll behavior, timeline materialization, streaming, reasoning, tool calls, approvals, and code previews continue to use current data sources.

3. **Right Runtime inspector**
   - Runtime remains visible and resizable when open.
   - It visually recedes into an inspector panel using softer container surfaces.
   - Diff and activity views stay dense enough for debugging and review.

## Component Design

### App Shell

Replace the current dark blue/purple shell feel with the Suiyuan light work surface. The shell should feel calm and high-productivity, not like a marketing page. Use existing shell components and CSS files, but revise tokens, active nav treatment, borders, panel backgrounds, and top-command surfaces.

The shell must preserve:

- Existing route selection.
- Theme toggle behavior.
- Locale toggle behavior.
- Update banner behavior.
- Page loading fallback.
- Focus visibility and keyboard navigation.

### Chat Header

The Chat header should become a quieter contextual bar. It should surface thread/project/model controls without competing with the message canvas. It should use Suiyuan label styling, softer borders, and pill-like action controls where appropriate.

Header feedback and approval error output remain visible and must not be visually muted below error affordance.

### Timeline And Messages

Messages should appear as readable cards on the centered canvas:

- Assistant messages use raised white cards with subtle outline and ambient shadow.
- User messages use a quieter filled surface, right-aligned or visually distinct from assistant output.
- Tool, reasoning, approval, code, diff, image, and directive content inherit Suiyuan surfaces but keep their functional affordances.
- Long code/diff content must prioritize legibility over decorative warmth.

The timeline must preserve:

- Streaming text behavior.
- Scroll anchoring and scroll-to-bottom controls.
- Timeline materialization.
- Code preview links.
- Markdown, image preview, Mermaid, and directive chip behavior.
- Approval interactions and failure reporting.

### Composer

The composer becomes the focal object at the bottom of the canvas:

- Rounded high-radius white input container.
- Toolbar row for attachments, model/project pills, mode controls, and send/interrupt actions.
- Sienna primary send action.
- Clear disabled, blocked, sending, interruptible, drag-over, and attachment-preview states.
- A bottom feather or tonal separation may be used so the composer remains legible over scrolling content.

The composer must preserve:

- Enter-to-send behavior with IME guard.
- Paste and drag/drop attachment handling.
- Attachment preview and removal.
- Project action blocked state.
- Provider/model controls.
- Interrupt action routing.

### Runtime Inspector

The runtime panel should look like an auxiliary inspector:

- Softer container background.
- Dense but calm toolbar controls.
- Diff groups and activity items as compact cards/rows.
- Resize splitter affordance remains visible and keyboard-operable.

The runtime panel must preserve:

- Diff grouping, collapse, save/open/locate actions.
- Activity log, activity stats, and transfer/status rendering.
- Runtime side panel open/close and width management.
- Empty states that explain missing data instead of silently hiding it.

## Responsive And Accessibility Requirements

1. Desktop layout keeps the B3 workbench structure, with left navigation, centered canvas, and optional runtime inspector.
2. Narrow widths collapse or reduce side surfaces before message content becomes unreadable.
3. Text must not overlap controls, badges, buttons, or cards.
4. Buttons and icon-only controls keep accessible names and visible focus states.
5. Keyboard workflows remain intact for composer, runtime resize, menus, dialogs, and global Escape interrupt.
6. The visual style may be soft, but contrast must remain suitable for long coding and review sessions.

## Data Flow And Error Handling

The redesign is presentation-first:

- `useClientStore` ownership of thread, runtime, composer, provider, project, and attachment state remains unchanged.
- `backendApi.js` and `wailsBridge.js` contracts remain unchanged.
- Existing fail-fast behavior for missing project/cwd/thread/provider data remains visible.
- UI states should not invent default data to make a screen look complete.
- Any newly introduced layout helper must receive explicit props rather than reading global state opportunistically.

## Implementation Boundaries

Primary edit targets are expected to be:

- `frontend-app/src/styles.css`
- `frontend-app/src/AppShell.css`
- `frontend-app/src/AppChrome.css`
- `frontend-app/src/pages/chat/ChatPage.css`
- `frontend-app/src/pages/chat/ChatPageWorkbench.css`
- `frontend-app/src/pages/chat/ChatMessages.css`
- `frontend-app/src/pages/chat/ChatTimeline.css`
- `frontend-app/src/pages/chat/ChatReasoning.css`
- `frontend-app/src/pages/chat/composer/ComposerDock.css`
- `frontend-app/src/pages/chat/runtime/RuntimePanel.css`

React component edits are allowed only when CSS alone cannot represent the B3 composition safely. If component edits are needed, keep public props and store calls stable. Extract small presentational helpers only when it reduces complexity in the touched component.

Implementation must follow the repository's LSP workflow before source edits. The current brainstorming turn did not expose the LSP tools in the callable toolset, so the implementation plan must explicitly obtain LSP evidence for location, understanding, impact, source reading, and diagnostics before editing React/CSS behavior.

## Verification Plan

Automated checks for frontend changes:

```bash
cd frontend-app
npm run lint
npm test
npm run build
```

Focused verification:

- Run affected Chat/component tests when touched.
- Verify intro Chat, active Chat with timeline, runtime-open, runtime-closed, and narrow viewport states.
- Exercise send, interrupt, drag/drop, attachment preview, approval, code preview, runtime resize, focus traversal, and scroll-to-bottom.
- Visually compare the result against the Suiyuan Stitch direction and `DESIGN.md` tokens.

Repository housekeeping:

- Do not commit `.superpowers/brainstorm` visual companion artifacts.
- Keep `DESIGN.md` as the exported design-system source for this design pass.

## Acceptance Criteria

1. The app shell and Chat workflow visibly match the Suiyuan light, warm, tactile minimalist direction.
2. Chat remains usable for real coding sessions: readable messages, legible code/diff blocks, visible runtime state, and clear composer actions.
3. Existing data flow and fail-fast behavior remain unchanged.
4. Desktop and narrow viewport layouts have no overlapping controls or clipped essential text.
5. Frontend lint, tests, and build pass.
6. Visual companion artifacts are excluded from commits.
