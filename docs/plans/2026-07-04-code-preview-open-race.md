# Code Preview Open Race

## Problem

`openCodeFile` responses are applied unconditionally in both ChatPage and RuntimePanel.
If a user opens file A, then file B, and A resolves last, the stale A response can replace
the visible B preview. A later save can write the edited draft to the wrong source file.

## Scope

- Add request-generation guards to ChatPage code preview opens.
- Add the same guard to RuntimePanel code preview opens.
- Invalidate pending opens when the preview is closed or the project scope changes.
- Add regressions for out-of-order open responses.

## Verification

- Focused ChatPage and RuntimePanel tests must fail before the guard and pass after it.
- Run full frontend lint, test, and build before committing.
