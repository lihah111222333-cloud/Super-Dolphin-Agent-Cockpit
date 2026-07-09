---
name: Suiyuan AI
colors:
  surface: '#fbf9f2'
  surface-dim: '#dbdad3'
  surface-bright: '#fbf9f3'
  surface-container-lowest: '#ffffff'
  surface-container-low: '#f5f4ed'
  surface-container: '#f0eee7'
  surface-container-high: '#eae8e1'
  surface-container-highest: '#e4e3dc'
  on-surface: '#1b1c18'
  on-surface-variant: '#584238'
  inverse-surface: '#30312c'
  inverse-on-surface: '#f2f1ea'
  outline: '#8b7268'
  outline-variant: '#e0c0b3'
  surface-tint: '#a43e03'
  primary: '#792b00'
  on-primary: '#ffffff'
  primary-container: '#c84d05'
  on-primary-container: '#ffc8b3'
  inverse-primary: '#ffb597'
  secondary: '#5d5f5d'
  on-secondary: '#ffffff'
  secondary-container: '#e2e3e0'
  on-secondary-container: '#636563'
  tertiary: '#424545'
  on-tertiary: '#ffffff'
  tertiary-container: '#5a5c5c'
  on-tertiary-container: '#d4d4d4'
  error: '#ba1a1a'
  on-error: '#ffffff'
  error-container: '#ffdad6'
  on-error-container: '#93000a'
  primary-fixed: '#ffdbcd'
  primary-fixed-dim: '#ffb597'
  on-primary-fixed: '#360f00'
  on-primary-fixed-variant: '#7d2d00'
  secondary-fixed: '#e2e3e0'
  secondary-fixed-dim: '#c6c7c4'
  on-secondary-fixed: '#1a1c1b'
  on-secondary-fixed-variant: '#454745'
  tertiary-fixed: '#e2e3e2'
  tertiary-fixed-dim: '#c6c7c6'
  on-tertiary-fixed: '#1a1c1c'
  on-tertiary-fixed-variant: '#454747'
  background: '#fbf9f2'
  on-background: '#1b1c18'
  surface-variant: '#e4e3dc'
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
Suiyuan embodies a **Sophisticated Minimalist** aesthetic tailored for high-productivity AI interactions. The brand personality is intellectual, calm, and utilitarian, yet infused with warmth through a "fidelity" color palette.

The design style sits at the intersection of **Corporate Modern** and **Tactile Minimalism**. It avoids the sterility of pure flat design by using subtle tonal layering and soft, diffused "custom-shadows" to suggest depth without the heaviness of traditional skeuomorphism. The interface is designed to feel like a "canvas"—a quiet, reliable space where the user's thoughts and AI's outputs take center stage. The target audience includes developers, researchers, and creative professionals who value precision and clarity.

## Colors
The color system is rooted in an earthy, high-fidelity palette that moves away from standard "tech blues."

- **Primary:** A rich, burnt sienna (#a03b00) used for brand identity, primary actions, and active states. It provides a warm, humanistic focal point.
- **Surface Tones:** The background isn't pure white but a "Surface Bright" (#fbf9f3), reducing eye strain during long sessions. We use a hierarchy of "Surface Container" tones to delineate functional zones (sidebar, cards, input field).
- **Secondary/Neutral:** Cool grays and deep charcoals are used for supporting navigation and body text, ensuring high legibility and a professional groundedness.
- **Accents:** High-fidelity "Fixed" variants (like #ffdbcd) are used for subtle backgrounds in active menu items or pills to provide soft contrast against the warm neutrals.

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
Depth is created through **Tonal Layering** supplemented by **Ambient Shadows**.

- **Level 0 (Background):** Surface Bright (#fbf9f3). Used for the main canvas area.
- **Level 1 (Sidebar/Containers):** Surface (#fbf9f3) with a subtle `border-r` or `border` in Outline Variant (#e0c0b3) at 30% opacity.
- **Level 2 (Interactive Cards):** Surface Container Lowest (#ffffff) with a `custom-shadow` (0 20px 40px -10px rgba(0,0,0,0.05)). These should feel "lifted" off the page.
- **Level 3 (Floating Input):** Uses a more pronounced `input-shadow` (0 8px 30px rgba(0,0,0,0.04)) and a thick 24px gradient feather at the bottom of the screen to ensure the floating input always stays legible against scrolling content.

## Shapes
The shape language is "Hyper-Softened." While the base system is Level 2 (Rounded), specific components use exaggerated rounding for comfort and approachability.

- **Standard Buttons & Inputs:** Pill-shaped (rounded-full) or very high radius (24px+) to mimic physical, touch-friendly objects.
- **Cards & Bento Boxes:** 16px (rounded-2xl) to provide a modern, friendly structure.
- **Small UI Elements (Icons/Checkboxes):** 8px (rounded-lg) to maintain consistency with the softer geometry of the larger containers.

## Components
- **Buttons:** Primary buttons are pill-shaped with the Primary color background and white text. Subtle "scale-98" transforms and transition-colors are used for hover/active states to provide tactile feedback.
- **Bento Cards:** Used for suggestions. Feature a white background, 1px border-variant, and a custom shadow. On hover, they should translate -4px vertically.
- **Floating Input:** A multi-line textarea contained in a rounded-3xl white box. It includes a "Toolbar" at the bottom for attachments and model selection pills.
- **Navigation Pills:** Sidebar items use a 4px left-border "Active Indicator" in the Primary color combined with a 10% opacity Primary-Fixed background to show the current selection.
- **Iconography:** Material Symbols Outlined, using 'FILL' 1 for active states and 'FILL' 0 for inactive states.
