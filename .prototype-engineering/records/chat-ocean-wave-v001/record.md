# Prototype Record

## Changes from parent

This is the first prototype version. It adds a scoped `ChatOceanAtmosphere` visual layer, a dedicated CSS prototype stylesheet, and regression coverage without changing chat state, message transport, composer actions, or backend contracts.

## Assumptions

- The reference specimen's strongest transferable qualities are layered depth, cool water color, and a single luminous orb rather than its control panel or exact composition.
- Existing theme state and CSS tokens are sufficient for day/night mapping; no new persisted preference is needed to test the hypothesis.
- Decorative motion is acceptable only when it is pointer-transparent and fully disabled by reduced-motion preferences.

## Evidence

- Dark and light browser screenshots are attached under `.prototype-engineering/evidence/chat-ocean-wave-v001/`.
- Full frontend verification passed: lint, 247 test files / 3,556 tests, and production build.
- Focused prototype regression coverage passed: 2 files / 35 tests.
- LSP diagnostics reported no findings in all changed source/test files.
- Browser DOM inspection confirmed the atmosphere is accessibility-hidden and does not replace existing controls.

## Findings

- The ocean composition is visually legible in both themes and makes the welcome surface markedly more distinctive.
- Three independently moving wave bands remain readable behind translucent cards and composer surfaces.
- The same markup works for intro and active-thread modes; active conversation receives a quieter translucent background so content remains dominant.
- Existing backend failure behavior remains visible in standalone Vite mode, proving the visual layer does not mask fail-fast state.

## Known gaps

- Compact CSS behavior is covered by automated source assertions, but the in-app Browser viewport override stayed at 1280×720 after reset/reload/new-tab attempts, so no trustworthy compact screenshot was captured.
- The visual run used standalone Vite and therefore could not exercise a real Wails-backed active conversation; active mode is covered by the existing React fixture and assertions.
- TypeScript LSP cross-file definition/reference navigation returned empty after a `No Project` error even after narrowing to `frontend-app`; document symbols, exact reads, and diagnostics worked.
- The experiment has no feature flag, persisted intensity control, or performance telemetry and should not be treated as productionized.

## Recommendation

Continue the direction for product review. If the visual language is accepted, use a child prototype to test motion intensity and compact/mobile composition with a working viewport harness before any productionization decision.
