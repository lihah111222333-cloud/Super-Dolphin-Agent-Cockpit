# Frontend UI Theme Redesign Design

**Date:** 2026-05-31
**Scope:** `frontend-app` only
**Approved direction:** Cosmic console

## Goal

Refresh the `frontend-app` interface into a bold branded Cosmic console style, add a local day/night theme switch, and remove the macOS-style traffic-light buttons without changing backend APIs or page behavior.

## Current Context

The current frontend shell is concentrated in:

- `frontend-app/src/App.jsx` — app shell, titlebar, navigation rail, pages, settings, and most UI markup.
- `frontend-app/src/styles.css` — global design tokens and component styling.
- `frontend-app/src/entities/client/model/useClientStore.js` — client state and backend bootstrap/data flows.
- `frontend-app/src/App.test.jsx` and `frontend-app/src/SettingsPage.test.jsx` — connected frontend tests.

Implementation must preserve unrelated local work and avoid broad refactors.

## Non-goals

- Do not change backend interfaces, API method names, request parameters, or response normalization.
- Do not move navigation routes or alter `navItems` semantics.
- Do not introduce a new runtime dependency for theme switching or animation.
- Do not rewrite page data flow or move UI state into backend preferences.
- Do not keep or replace the macOS traffic-light buttons with another fake window-control pattern.

## Visual Direction

Use the approved **Cosmic console** style:

- Dark theme: deep black-blue base, purple/blue highlights, glass-like surfaces, fine translucent borders, and subtle radial background depth.
- Light theme: cool white/light-blue base, ink text, low-saturation purple/blue accents, and clear panel boundaries.
- Preserve engineering-tool readability and information density. The redesign should feel branded and modern without becoming a marketing landing page.

## UI Architecture

### App Shell

`App.jsx` should keep the existing page selection and backend flow. The root shell will expose the selected theme through a DOM attribute:

```jsx
<div className="sa-window" data-theme={theme} data-testid="frontend-app">
```

CSS variables in `styles.css` will use the root theme attribute to switch colors.

### Titlebar

The `Titlebar` component will stop rendering `.traffic-lights` and its red/yellow/green children. It will instead render:

- Brand/title area: `Super Agent`.
- Theme toggle button on the right.

The theme toggle should be accessible and explicit:

- In dark mode: button label `白天模式` and `aria-label="切换到白天模式"`.
- In light mode: button label `黑夜模式` and `aria-label="切换到黑夜模式"`.

It may use existing `lucide-react` icons such as `Sun` and `Moon`; no new dependency is needed.

### Navigation Rail

Keep the current 76px rail and existing `navItems`. Style changes should be CSS-only:

- Dark theme active item: blue/purple glow and readable light text.
- Light theme active item: cool light surface with dark text and blue/purple accent.
- Hover states should remain subtle and not shift layout.

### Main Content

Keep page layouts intact. Update shared visual tokens and component selectors so existing pages inherit the new style:

- Backgrounds: `--bg`, `--bg-elevated`, radial shell gradients.
- Surfaces: `--panel`, `--panel-2`, `--panel-3`, optional glass-like tokens.
- Text: `--ink`, `--muted`, `--faint` with sufficient contrast in both themes.
- Lines: `--line`, `--line-strong` with translucent blue-gray behavior.
- Accents: blue/purple primary colors plus existing semantic green/orange/red.

Buttons, inputs, selects, textareas, nav items, panels, and page chrome should consume these variables. Avoid hardcoded legacy gray/black values where they block theme switching.

## Theme State and Persistence

Theme state stays purely in the frontend:

- Add a lightweight hook or local state in `App.jsx`, for example `useColorTheme()`.
- Read `localStorage.getItem('super-dolphin-theme')` on initialization.
- Accept only `light` and `dark` values.
- Default to `dark` if no valid value exists.
- On toggle, update React state and `localStorage`.

Do not use `setPreference`, `getPreference`, `callBackend`, or `useClientStore` for this UI preference.

## Interaction and Motion

Use CSS transitions only:

- Theme button hover: slight lift, brighter border/background.
- Nav hover: subtle glow and background change.
- Active nav item: stable visual emphasis without moving layout.
- Panels/buttons: preserve existing click targets and disabled states.

No GSAP dependency should be added for this app-shell redesign. The project is a dense product interface, not a standalone landing page; CSS transitions are sufficient and lower-risk.

## Accessibility and Contrast

- The theme toggle must be a real `<button>` with descriptive `aria-label`.
- Text and button labels must remain readable in both themes.
- Do not encode theme solely through color; the toggle label changes as well.
- Avoid invisible text on high-saturation backgrounds.
- Avoid emoji and cheap meta-labels.

## Testing Requirements

Update frontend tests to lock the behavior:

1. Rendering no longer includes `.traffic-lights`.
2. With no saved theme, root app has `data-theme="dark"`.
3. Clicking the theme toggle changes root `data-theme` to `light` and writes `super-dolphin-theme=light` to `localStorage`.
4. Clicking again returns to `dark` and writes `super-dolphin-theme=dark`.
5. Theme switching does not call backend preference APIs.

Existing settings tests should continue to pass.

## Verification

Before claiming completion, run from `frontend-app`:

```bash
npm test
npm run build
```

## Risks and Constraints

- `App.jsx` is large. Keep changes surgical: add only the theme hook, pass props to `Titlebar`, and adjust titlebar markup.
- `styles.css` contains many hardcoded colors. Prefer token-level changes plus focused selector updates instead of attempting a full CSS rewrite.
- LocalStorage is browser-local UI state; invalid stored values should fall back to dark without backend involvement.
