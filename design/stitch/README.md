# Stitch design reference

This directory is the local, reviewable design source for the frontend redesign on
`codex/stitch-ui-redesign-20260720`.

KimiCode must inspect this file, `DESIGN_SYSTEM.md`, and every image under
`screens/` before editing `frontend-app`. The eight numbered screens are the
primary route references. Files prefixed with `reference-` are supporting images
from the same Stitch project and must not be treated as additional product routes.

## Source

- Stitch project: `Codex Style Main Console`
- Project ID: `16556859161700396548`
- Device type: desktop
- Design system: `Luminous Minimalist`
- Dump date: 2026-07-20

## Primary screens

| Route | Theme | Local image | Stitch screen ID |
| --- | --- | --- | --- |
| `/` | Light / Vibe | `screens/01-chat-vibe-light.png` | `5959839627de4f9899f8e00a44d03b14` |
| `/` | Dark | `screens/02-chat-dark.png` | `eaa8d7d9d8754330883e3406d844e1c4` |
| `/skills` | Light / Vibe | `screens/03-plugins-vibe-light.png` | `a3e251a250894467a8366d6e53c8c028` |
| `/skills` | Dark | `screens/04-plugins-dark.png` | `128e1528aed74facbc90feddfe8b1a59` |
| `/memory` | Light / Vibe | `screens/05-memory-vibe-light.png` | `9f89a786764b4c03881e7320a7de4b8d` |
| `/memory` | Dark | `screens/06-memory-dark.png` | `5730234c95774969b3740c06ff71d152` |
| `/prompts` | Light / Vibe | `screens/07-roles-vibe-light.png` | `c7e81dccca1f414abf42afccce1c8e7e` |
| `/prompts` | Dark | `screens/08-roles-dark.png` | `0adf668a3a604a8c84bf9041d17be005` |

## Supporting references

The ten `reference-*.png` files are the remaining screenshot assets returned by
Stitch. Their original title is `image.png`; the screen ID is preserved in each
filename so it can be cross-checked through Stitch if needed.

## Implementation rule

Use the screenshots as visual truth for hierarchy, spacing, proportions, surface
colors, contrast, and responsive intent. Do not paste generated HTML into the
application and do not replace the existing React/store/RPC architecture. When a
supporting image conflicts with a numbered route screen, the numbered screen and
`DESIGN_SYSTEM.md` take precedence.
