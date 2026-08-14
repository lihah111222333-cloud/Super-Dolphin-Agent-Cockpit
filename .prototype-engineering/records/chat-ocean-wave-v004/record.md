# Prototype Record

## Changes from parent

- Applied one shared ocean-scoped frosted-glass material to user bubbles, assistant bubbles, and the composer card.
- Set the surface opacity to exactly 67% with `rgb(from var(--surface) r g b / 67%)` in both themes.
- Overrode the composer glass background variable locally so the existing global `!important` glass-card rule resolves to the same 67% material without affecting other pages.
- Added a CSS regression assertion covering all three selectors, the exact opacity, the composer variable, and the blur/saturation filter.

## Assumptions

- “透明度百分之67” means the material is 67% opaque (alpha `0.67`), not 67% transparent.
- The requested glass treatment is scoped to the ocean chat prototype and should not alter global message or composer styling elsewhere.
- The existing theme surface token remains the color source so contrast follows the active light/dark theme.

## Evidence

- `.prototype-engineering/evidence/chat-ocean-wave-v004/verification.md` records the running browser’s computed background and backdrop-filter values for all three targets in dark and light themes.
- Focused regression run passed: 2 files and 18 tests.
- LSP diagnostics reported no findings for the modified stylesheet and test.
- Repository frontend gates passed: lint, 247 test files / 3575 tests, and production build.

## Findings

- A shared `var(--surface)` material produces consistent 0.67 alpha in both themes while preserving theme-specific color.
- The composer’s prior 0.58 alpha came from `GlassCardPolish.css`, whose `background: var(--glass-card-background) !important` defeated a normal scoped background declaration.
- Overriding `--glass-card-background` at the ocean composer node makes that existing rule resolve correctly and avoids widening the change to the global glass system.

## Known gaps

- The in-app browser session did not expose screenshot capture, so this revision’s new evidence is computed-style output rather than a PNG; the user-provided marked screenshots remain the visual baseline.
- This prototype revision validates the requested material treatment only; it does not productionize the broader ocean experiment.

## Recommendation

Continue the ocean prototype with this 67% glass material as the accepted local baseline, keeping further visual tuning scoped to subsequent prototype revisions.
