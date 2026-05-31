# Frontend UI Theme Redesign Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Refresh `frontend-app` into the approved Cosmic console visual style, add a local day/night theme switch, and remove macOS-style traffic-light buttons without changing backend APIs.

**Architecture:** Keep the existing React shell and backend data flow intact. Add local UI theme state in `App.jsx`, expose it through `data-theme` on `.sa-window`, and implement the dark/light Cosmic console appearance through CSS variables and focused selector updates in `styles.css`.

**Tech Stack:** React 19, Vite, Vitest, Testing Library, Zustand, lucide-react, browser `localStorage`, CSS custom properties.

---

## Tasks

### Task 1: Lock Theme Shell Behavior With Tests

Modify `frontend-app/src/App.test.jsx` to clear `window.localStorage` in the existing `beforeEach` and add tests asserting: no `.traffic-lights`, default `data-theme="dark"`, theme toggle switches light/dark, localStorage writes `super-dolphin-theme`, and backend `setPreference` calls do not increase during theme toggling. Run `cd frontend-app && npm test -- App.test.jsx`; expected red before implementation.

### Task 2: Implement Local Theme State and Remove macOS Controls

Modify `frontend-app/src/App.jsx`: import `Moon` and `Sun`, add `THEME_STORAGE_KEY`, `COLOR_THEMES`, `normalizeColorTheme`, and `useColorTheme()`. Use `data-theme={theme}` on `.sa-window`, pass `theme` and `toggleTheme` to `Titlebar`, remove `.traffic-lights`, and render a right-aligned accessible `.theme-toggle` with `白天模式`/`黑夜模式`. Run `cd frontend-app && npm test -- App.test.jsx`.

### Task 3: Apply Cosmic Console Theme Tokens and Titlebar Styles

Modify `frontend-app/src/styles.css`: replace root tokens with dark Cosmic console tokens, add `.sa-window[data-theme="light"]` overrides, restyle `body`, `.sa-window`, `.titlebar`, `.titlebar-brand`, `.brand-orb`, `.theme-toggle`, remove all `.traffic-lights` selectors, and update `.sa-body` height to subtract 52px. Run `cd frontend-app && npm test -- App.test.jsx`.

### Task 4: Theme Shared UI Surfaces Without Changing Data Flow

Modify `frontend-app/src/styles.css`: make `.sa-body`, `.sa-main`, page backgrounds transparent; theme `.nav-rail`, nav buttons, `.top-command`, shared button/select rules; replace hardcoded legacy chrome colors only where they block theme switching. Do not change semantic status colors or API/data flow. Run `cd frontend-app && npm test -- App.test.jsx SettingsPage.test.jsx`.

### Task 5: Full Frontend Verification

Run `cd frontend-app && npm test` and `cd frontend-app && npm run build`. Inspect `git diff -- frontend-app/src/App.jsx frontend-app/src/styles.css frontend-app/src/App.test.jsx` to confirm only intended UI/theme changes and no backend API changes.

---

## Self-Review

- Spec coverage: tests, local state, removal of macOS controls, Cosmic console dark/light styling, and full verification are covered.
- Placeholder scan: no unfinished markers or incomplete steps are present.
- Type consistency: `THEME_STORAGE_KEY`, `COLOR_THEMES`, `normalizeColorTheme`, `useColorTheme`, `theme`, and `toggleTheme` names are consistent.
