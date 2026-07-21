# Luminous Minimalist design system

This is the local implementation reference extracted from Stitch project
`16556859161700396548`.

## Visual direction

- Sophisticated minimalist, corporate modern, and tactile minimalism.
- Warm, calm, low-noise surfaces instead of generic technology blue.
- Depth comes from tonal layering, fine borders, and diffuse ambient shadows.
- Avoid large saturated gradients, excessive glass effects, and applying one
  branded surface treatment to unrelated components.

## Core colors

| Token | Value |
| --- | --- |
| Background | `#fbf9f2` |
| Surface bright | `#fbf9f3` |
| Surface lowest | `#ffffff` |
| Surface low | `#f5f4ed` |
| Surface container | `#f0eee7` |
| Surface high | `#eae8e1` |
| Surface highest | `#e4e3dc` |
| On surface | `#1b1c18` |
| On surface variant | `#584238` |
| Primary override | `#a03b00` |
| Primary | `#792b00` |
| Primary container | `#c84d05` |
| Primary fixed | `#ffdbcd` |
| Outline | `#8b7268` |
| Outline variant | `#e0c0b3` |
| Inverse surface | `#30312c` |
| Inverse on surface | `#f2f1ea` |
| Error | `#ba1a1a` |

Foreground colors must be explicit for their actual surface. Do not rely on an
unbounded `color: inherit` chain to obtain readable contrast.

## Typography

- Font family: Inter.
- Display: `40px / 600 / 52px`, letter spacing `-0.02em`.
- Headline: `24px / 500 / 32px`, letter spacing `-0.01em`.
- Large body: `18px / 400 / 28px`.
- Body: `14px / 400 / 22px`.
- Small label: `12px / 500 / 16px`, letter spacing `0.02em`.
- Extra-small label: `11px / 600 / 14px`.

## Layout and spacing

- Base spacing grid: `4px`.
- Desktop sidebar: `280px`.
- Main content maximum width: `1100px`.
- Desktop page margin: `32px`.
- Standard gutter: `24px`.
- Mobile page padding: `16px`.
- Desktop uses fixed side navigation and a centered content canvas.
- Mobile replaces the sidebar with bottom navigation.

## Shape and elevation

- Cards and bento surfaces: `16px` radius.
- Inputs and prominent controls: `24px+` radius.
- Primary actions: pill shape.
- Small controls and icons: `8px` radius.
- Interactive card shadow: `0 20px 40px -10px rgba(0, 0, 0, 0.05)`.
- Floating input shadow: `0 8px 30px rgba(0, 0, 0, 0.04)`.

## Component behavior

- Primary buttons use a warm primary background and explicit light text.
- Navigation selection uses a primary left indicator and a subtle warm fixed
  surface, not a large saturated block.
- Suggestion cards use a light surface, one-pixel outline, and restrained hover
  elevation.
- The chat composer is a rounded floating surface with attachment and model
  controls in its toolbar.
- Preserve visible focus, disabled, hover, active, keyboard, and touch states.
- Never use clipping, ellipsis, negative margins, or absolute positioning merely
  to conceal responsive overflow.
