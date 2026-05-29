# 2026-05-28 Memory Center Neo-Brutalist & Paper Stack Visual Design Spec

This document details the visual design specifications for refactoring the **Memory Center (记忆中心)** to align with the **Neo-Brutalist & Paper Stack (新粗野纸张物理堆叠与物理位移)** aesthetic.

## Goal Description

The current Memory Center UI follows a standard flat grid pattern that lacks visual depth and tactile feedback. This refactoring will upgrade the visual layer to a tactile, premium Wabi-Sabi Neo-Brutalist sandstone style. It simulates natural paper stacking, procedural grain textures, physical spring hover micro-interactions, and bold geometric grids—all while remaining strictly 100% zero-dependency (no heavy external component/style libraries) and fully backward-compatible with all existing Wails/Vitest/Playwright tests.

---

## Visual Design Specification

### 1. Sandstone & Paper Grain Texture (Procedural Noise)
To establish a premium tactile surface, a tiny SVG fractal noise turbulence filter will be dynamically applied as a CSS background overlay to the page body and memory cards:
```css
body::before {
  content: "";
  position: fixed;
  top: 0; left: 0; width: 100vw; height: 100vh;
  pointer-events: none;
  z-index: 9999;
  opacity: 0.055;
  background-image: url("data:image/svg+xml,%3Csvg viewBox='0 0 200 200' xmlns='http://www.w3.org/2000/svg'%3E%3Cfilter id='noise'%3E%3CfeTurbulence type='fractalNoise' baseFrequency='0.78' numOctaves='3' stitchTiles='stitch'/%3E%3C/filter%3E%3Crect width='100%25' height='100%25' filter='url(%23noise)'/%3E%3C/svg%3E");
}
```
This mimics rough, premium matte paper/sandstone, creating a unified background grain without downloading external image files.

### 2. 3D Stacked Folders (Similar Memory Consolidation Panel)
The similarity warning alert (`.mc-similar-bar`) will be styled to mimic a real 3D cardboard folder stack:
* **Visual Stack**: Using CSS `:before` and `:after` pseudoelements with physical boundaries and offset coordinates:
  - Sheet 1: `transform: translate(4px, 4px) rotate(0.8deg);`
  - Sheet 2: `transform: translate(8px, 8px) rotate(-0.5deg);`
* **Tab Design**: A mock folder tab sticking out from the top boundary carrying the label `SIMILAR DETECTED` in a sharp mono font.
* **Outline**: Sharp, highly-legible `1.5px solid var(--mc-border)` boundaries with high-contrast offsets instead of blurry, saturated colorful gradient bars.

### 3. Bento Box 2.0 Grid (Numerical Monoliths)
The top KPI metrics (Total Memories, Memory Health, Auto Dream Status) will be laid out in an asymmetrical Bento Grid:
* **Numerical Monoliths**: Display large indicators (e.g. `totalEntries` value) in an ultra-large `52px` monospace font (Hack / Source Code Pro) with tight letter spacing (`-2px`).
* **Sub-Pill Metadata**: Discrete borders for metadata chips using Sage Green accents in light/dark modes.

### 4. Memory Card Tactile Physics (Inertial Hover & Click)
Each individual memory card (`.mc-entry-card`) will receive a physical-feeling spring micro-interaction:
* **Micro-Rotation on Hover**:
  - Cards will lift up and tilt slightly on mouse hover, mimicking grabbing a piece of cardstock.
  - Left-hand cards tilt clockwise (`rotate(0.8deg)`), right-hand cards tilt counter-clockwise (`rotate(-0.6deg)`).
  - Hover CSS: `transform: translateY(-6px) rotate(0.8deg); box-shadow: var(--br-shadow-hover);`
  - Transition curve: `transition: transform 0.25s cubic-bezier(0.175, 0.885, 0.32, 1.275), box-shadow 0.25s ease;` (anticipatory overshoot spring).
* **Click Press Physics**:
  - Buttons and interactive items will depress into the page when clicked:
  - CSS `:active`: `transform: translate(2px, 2px); box-shadow: var(--br-shadow-active);`

### 5. Color Palette Refinement (Zero Hardcoded Hexes)
All colors remain strictly scoped within the page using CSS custom variables to support light and dark system themes seamlessly:
* **Cream-White (Light)**: Base `#f5f3ed` (米色), Card Preferences `#fdfcf7`, Card Projects `#f4f6f0`.
* **Graphite-Black (Dark)**: Base `#0d0f0e`, Card Preferences `#181a19`, Card Projects `#151a17`.
* **Accents**: Sage Green `#3d664f` (light) / `#6eb38a` (dark).

---

## Proposed Changes

### [Component: Agent Terminal Frontend Styles]

#### [MODIFY] [memory-center.css](file:///Users/mac/Desktop/agnet/Super-Dolphin/cmd/agent-terminal/frontend/vue-app/styles/memory-center.css)
* Complete implementation of Wabi-Sabi Neo-Brutalist layout styles.
* Injection of the SVG noise backdrop filter to the root selector.
* Styling for `.mc-similar-bar` with stacked layers, shadows, and physical border folder tab shapes.
* Styling for `.mc-entry-card` with cubic-bezier inertial hover translations, 3D paper rotation offsets, and active pressing physics.
* Integration of the numeric typography layout rules.

#### [MODIFY] [MemoryCenterPage.js](file:///Users/mac/Desktop/agnet/Super-Dolphin/cmd/agent-terminal/frontend/vue-app/pages/MemoryCenterPage.js)
* Keep existing Vue logic (similarity consolidations, dream switches, delete triggers, API pipelines) completely untouched.
* Minor visual hierarchy restructuring inside the HTML template:
  - Add visual wrappers for similar cards to support the folder tabs and stacked sheets without altering the DOM tree.
  - Maintain all `data-testid` attributes exactly as-is so E2E selectors do not break.

---

## Verification Plan

### Automated Tests
1. **Front-End Unit Checks**:
   `npx vitest run`
   Must output 100% passed for all 1394 frontend assertions.
2. **E2E Playwright Operations Checks**:
   `npx playwright test tests/e2e/memory-center-operations.spec.js`
   Must pass 100% on chromium showing that visual refactor does not alter functionality.
3. **Size Check**:
   `node scripts/size-guard.cjs`
   Must pass size budget constraints.

### Manual Verification
1. Open local preview page [http://localhost:8765/mockup_memory_center_brutalist.html](http://localhost:8765/mockup_memory_center_brutalist.html) and interact with it.
2. Build and launch Go server via `make run-plain` and toggle light/dark modes to verify sandbox-scoped CSS variable matching.
