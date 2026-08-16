# Glass material verification

Verified against the running app at `http://127.0.0.1:5175/` on 2026-08-14.

## Dark theme

| Target | Computed background | Computed backdrop filter |
| --- | --- | --- |
| User bubble | `color(srgb 0.0862745 0.0980392 0.12549 / 0.67)` | `blur(18px) saturate(1.18)` |
| Assistant bubble | `color(srgb 0.0862745 0.0980392 0.12549 / 0.67)` | `blur(18px) saturate(1.18)` |
| Composer card | `color(srgb 0.0862745 0.0980392 0.12549 / 0.67)` | `blur(18px) saturate(1.18)` |

## Light theme

| Target | Computed background | Computed backdrop filter |
| --- | --- | --- |
| User bubble | `color(srgb 1 1 1 / 0.67)` | `blur(18px) saturate(1.18)` |
| Assistant bubble | `color(srgb 1 1 1 / 0.67)` | `blur(18px) saturate(1.18)` |
| Composer card | `color(srgb 1 1 1 / 0.67)` | `blur(18px) saturate(1.18)` |

The app was restored to dark theme after verification. The in-app browser does not expose page screenshot capture in this session, so the evidence records reproducible computed styles instead of a new PNG.

## Automated checks

- Focused: 2 test files, 18 tests passed.
- LSP diagnostics: no diagnostics in `ChatOceanPrototype.css` or `ChatOceanPrototype.test.js`.
- Lint: passed.
- Full test: 247 test files, 3575 tests passed.
- Production build: passed; Vite transformed 7134 modules.
