# Prototype Record

## Changes from parent

- Rebased the accepted ocean chat prototype onto the latest `origin/main` and resolved generated project-map conflicts by full regeneration.
- Kept the atmosphere, AI active/idle mapping, overlay geometry, and 67%-opaque frosted glass on the real production Chat page with no alternate route or data path.
- Updated the current frontend production-size baseline and two stale upstream test assertions discovered by the full integration gate.

## Assumptions

- The user’s instruction to settle and merge the prototype authorizes making the accepted ocean presentation the default Chat presentation on `main`.
- Productionization should retain the existing store, runtime, theme, layout, and backend contracts unchanged.
- The existing Vite chunk-size warning is repository-wide and does not block this UI-only productionization.

## Evidence

- `.prototype-engineering/evidence/chat-ocean-wave-v005/productionization.md` records integration, hardening, LSP, project-map, test, lint, and build evidence.
- The full frontend suite passed with 247 test files and 3584 tests after integration fixes.
- The production build completed after transforming 7134 modules.

## Findings

- The ocean implementation applies cleanly to the latest ChatPage structure and does not require a feature flag, mock, backend change, or persisted preference.
- Existing runtime truth remains the sole source of the ocean active state.
- The only rebase conflicts were generated project-map outputs; business source merged automatically.
- The two full-suite failures were stale assertions against current upstream layout contracts, not regressions in the ocean selectors or behavior.

## Known gaps

- Vite continues to report pre-existing chunks above 650 kB; bundle splitting is outside this presentation change.
- The ocean presentation is now the default rather than a user-selectable preference; adding an appearance preference would require a separate product decision and persistence contract.

## Recommendation

Productionize this revision on `main`; future ocean adjustments should be treated as normal production UI changes with the same frontend and visual regression gates.
