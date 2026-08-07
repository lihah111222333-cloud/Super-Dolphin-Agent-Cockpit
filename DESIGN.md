---
name: Super Dolphin Agent
colors:
  surface: '#f5f7fa'
  surface-dim: '#dae0eb'
  surface-bright: '#f5f7fa'
  surface-container-lowest: '#ffffff'
  surface-container-low: '#f2f4f9'
  surface-container: '#eceff5'
  surface-container-high: '#e2e7f0'
  surface-container-highest: '#dae0eb'
  on-surface: '#171a23'
  on-surface-variant: '#4c5568'
  inverse-surface: '#2a2e3a'
  inverse-on-surface: '#f2f4fa'
  outline: '#8a93a8'
  outline-variant: '#d3d9e5'
  surface-tint: '#4f5dd8'
  primary: '#3d4ac0'
  on-primary: '#ffffff'
  primary-container: '#6672e8'
  on-primary-container: '#e2e7ff'
  inverse-primary: '#8f9ff8'
  secondary: '#5d6474'
  on-secondary: '#ffffff'
  secondary-container: '#e9edf4'
  on-secondary-container: '#4c5568'
  tertiary: '#4a5162'
  on-tertiary: '#ffffff'
  tertiary-container: '#54627a'
  on-tertiary-container: '#dfe3ec'
  error: '#ba1a1a'
  on-error: '#ffffff'
  error-container: '#ffdad6'
  on-error-container: '#93000a'
  primary-fixed: '#e2e7ff'
  primary-fixed-dim: '#b9c2f8'
  on-primary-fixed: '#1e2660'
  on-primary-fixed-variant: '#3d4ac0'
  secondary-fixed: '#e9edf4'
  secondary-fixed-dim: '#c3c9d6'
  on-secondary-fixed: '#171a23'
  on-secondary-fixed-variant: '#4c5568'
  tertiary-fixed: '#e9edf4'
  tertiary-fixed-dim: '#c3c9d6'
  on-tertiary-fixed: '#171a23'
  on-tertiary-fixed-variant: '#4c5568'
  background: '#f5f7fa'
  on-background: '#171a23'
  surface-variant: '#dae0eb'
typography:
  display-lg:
    fontFamily: Inter
    fontSize: 40px
    fontWeight: '600'
    lineHeight: 52px
    letterSpacing: -0.02em
  headline-md:
    fontFamily: Inter
    fontSize: 24px
    fontWeight: '500'
    lineHeight: 32px
    letterSpacing: -0.01em
  body-lg:
    fontFamily: Inter
    fontSize: 18px
    fontWeight: '400'
    lineHeight: 28px
  body-md:
    fontFamily: Inter
    fontSize: 14px
    fontWeight: '400'
    lineHeight: 22px
  label-sm:
    fontFamily: Inter
    fontSize: 12px
    fontWeight: '500'
    lineHeight: 16px
    letterSpacing: 0.02em
  label-xs:
    fontFamily: Inter
    fontSize: 11px
    fontWeight: '600'
    lineHeight: 14px
rounded:
  sm: 0.25rem
  DEFAULT: 0.5rem
  md: 0.75rem
  lg: 1rem
  xl: 1.5rem
  full: 9999px
spacing:
  base: 4px
  gutter: 24px
  margin-desktop: 32px
  sidebar-width: 280px
  content-max-width: 1100px
---

## Brand & Style
Super Dolphin Agent embodies a **Sophisticated Minimalist** aesthetic tailored for high-productivity AI interactions. The brand personality is intellectual, calm, and precise, expressed through a cool "Airy Command Center" light palette that mirrors the dark "AI Command Center" theme—one design language, two luminance environments.

The design style sits at the intersection of **Corporate Modern** and **Tactile Minimalism**. It avoids the sterility of pure flat design by using subtle tonal layering and soft, diffused "custom-shadows" to suggest depth without the heaviness of traditional skeuomorphism. The interface is designed to feel like a "canvas"—a quiet, reliable space where the user's thoughts and AI's outputs take center stage. The target audience includes developers, researchers, and creative professionals who value precision and clarity.

## Colors
The color system uses a cool, instrument-grade neutral palette with a single indigo-violet accent family shared with the dark theme.

- **Primary:** A clear indigo blue-violet (#4f5dd8) used for brand identity, primary actions, and active states. It is the light-mode counterpart of the dark theme's #8f9ff8 and keeps both themes recognizably one product.
- **Surface Tones:** The background isn't pure white but a cool "Surface Bright" (#f5f7fa), reducing eye strain during long sessions. We use a hierarchy of "Surface Container" tones (#ffffff → #f2f4f9 → #eceff5 → #e2e7f0) to delineate functional zones (sidebar, cards, input field).
- **Secondary/Neutral:** Cool slate grays (#4c5568 / #8a93a8) are used for supporting navigation and body text, ensuring high legibility and a professional groundedness.
- **Accents:** "Fixed" variants (like #e2e7ff) are used for subtle backgrounds in active menu items or pills to provide soft contrast against the cool neutrals; companion hues cyan (#0891b2) and violet (#7c5cf0) appear only in gradients and focus highlights, never as large fills.

## Typography
The system uses **Inter** exclusively to maintain a utilitarian and systematic feel. The hierarchy is strictly enforced through weight and letter-spacing rather than excessive color changes.

- **Display & Headlines:** Use tighter letter-spacing and medium-to-semibold weights to create a "dense," authoritative look for the canvas's central questions.
- **Body:** Standardized at 14px for general UI and 18px for "Stage" text to ensure comfortable reading across long AI responses.
- **Labels:** Small caps or slightly tracked-out labels (11px-12px) are used for metadata, pill text, and secondary navigation to keep the interface feeling precise and "instrument-like."

## Layout & Spacing
The layout employs a **Fixed Grid with Side Navigation**.

- **Structure:** A permanent left sidebar (280px) houses primary navigation. On desktop, the main canvas is centered within the remaining viewport with a maximum content width of 1100px to prevent line lengths from becoming unreadable.
- **Rhythm:** A 4px baseline grid is used. Standard gutters are 24px, and page margins are 32px on desktop.
- **Responsive:** On mobile, the sidebar is replaced by a bottom navigation bar, and the top app bar becomes the primary anchor for branding and profile actions. Padding reduces to 16px to maximize the canvas area.

## Elevation & Depth
Depth is created through **Tonal Layering** supplemented by **Ambient Shadows** tinted with cool navy (rgba(30, 38, 68, …)) instead of pure black.

- **Level 0 (Background):** Surface Bright (#f5f7fa). Used for the main canvas area.
- **Level 1 (Sidebar/Containers):** Surface (#f5f7fa → #eef1f7) with a subtle `border-r` or `border` in Outline Variant (#d3d9e5).
- **Level 2 (Interactive Cards):** Surface Container Lowest (#ffffff) with a `custom-shadow` (0 20px 40px -10px rgba(30,38,68,0.08)). These should feel "lifted" off the page.
- **Level 3 (Floating Input):** Uses a more pronounced `input-shadow` (0 8px 30px rgba(30,38,68,0.06)) and a thick 24px gradient feather at the bottom of the screen to ensure the floating input always stays legible against scrolling content.

## Shapes
The shape language is "Hyper-Softened." While the base system is Level 2 (Rounded), specific components use exaggerated rounding for comfort and approachability.

- **Standard Buttons & Inputs:** Pill-shaped (rounded-full) or very high radius (24px+) to mimic physical, touch-friendly objects.
- **Cards & Bento Boxes:** 16px (rounded-2xl) to provide a modern, friendly structure.
- **Small UI Elements (Icons/Checkboxes):** 8px (rounded-lg) to maintain consistency with the softer geometry of the larger containers.

## Components
- **Buttons:** Primary buttons are pill-shaped with the Primary color background and white text. Subtle "scale-98" transforms and transition-colors are used for hover/active states to provide tactile feedback. Hero actions (new chat, send) may use the Primary→Violet gradient with a soft primary shadow, mirroring the dark theme's glow language at light-appropriate intensity.
- **Bento Cards:** Used for suggestions. Feature a white background, 1px border-variant, and a custom shadow. On hover, they should translate -4px vertically and pick up a primary-tinted border.
- **Floating Input:** A multi-line textarea contained in a rounded-3xl white box. It includes a "Toolbar" at the bottom for attachments and model selection pills. Focus state uses a primary-to-cyan gradient border.
- **Navigation Pills:** Sidebar items use a 4px left-border "Active Indicator" in the Primary color combined with a 10% opacity Primary-Fixed background to show the current selection.
- **Iconography:** Material Symbols Outlined, using 'FILL' 1 for active states and 'FILL' 0 for inactive states.
