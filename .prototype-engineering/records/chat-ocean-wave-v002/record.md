# Prototype Record

## Changes from parent

Removed the active-conversation full-canvas background and backdrop filter while retaining the intro-specific treatment and local message/composer surfaces.

## Assumptions

The visible horizontal seam was caused by the active `.conversation` overlay rather than wave geometry or the timeline itself.

## Evidence

Real browser computed styles changed from an 88% opaque background plus blur to transparent with no blur in both themes. Focused tests passed 36 assertions and LSP diagnostics were clean.

## Findings

The overlay was the direct cause. Removing it restores a continuous sky and ocean field without reducing message/composer readability.

## Known gaps

New user feedback expands the experiment to AI-running animation state, notice placement, Agents board layout, and wave-pattern clipping; those require a boundary-version child prototype.

## Recommendation

Continue with a boundary-change child prototype covering the four new browser comments.
