# P0/P1 Contract Alignment Design

Status: approved in conversation on 2026-07-11.

## Goal

Fix the confirmed P0/P1 frontend-backend alignment defects on `main@206633c197daf75969f9bb274ef1f289f32320df` without changing unrelated P2 behavior or introducing a new wire protocol.

## Scope

1. Strip the exact `_aoRequestId` frontend metadata field before strict native Wails RPC dispatch.
2. Make the Wails Method ID guard consume the production frontend `METHOD_IDS` source instead of a copied numeric map.
3. Make the RPC contract audit consume runtime method and payload facts instead of the shadow definitions in `backendApi.js`.
4. Normalize the existing Go UI snapshot status maps into the rich per-thread status objects already used by realtime patches.

## Architecture

The repair is intentionally compatibility-first. The Go wire payload remains stable; normalization happens at the existing frontend snapshot boundary. The Wails metadata fix keeps an explicit allowlist and does not accept arbitrary `_ao*` keys. Contract guards read production sources directly rather than adding another generated registry.

## Error Handling

Unknown RPC payload fields continue to fail fast. Only `_aoRequestId` is recognized as frontend metadata. Snapshot status validation must reject malformed map values instead of silently mixing strings and rich objects in the Zustand store.

## Testing

Each repair follows RED/GREEN:

- Native strict-handler regression test using a payload containing `_aoRequestId`.
- Method ID parser/guard tests that detect missing, unknown, duplicate, or changed frontend entries.
- RPC audit fixture proving that runtime-source drift fails even when the facade shadow remains unchanged.
- Snapshot fixture using the real Go wire shape and asserting the resulting rich status entry.

The final verification surface is `internal/ui/wails`, `internal/module/uistate`, the frontend RPC audit tests, relevant frontend store/bridge tests, then full frontend lint/test/build.

## Git Boundary

No commit, merge, or push occurs before user review. After approval, changes are split into four atomic commits with each fix committed together with its regression test, then refreshed against `origin/main`, merged into local `main`, reverified, and pushed.
