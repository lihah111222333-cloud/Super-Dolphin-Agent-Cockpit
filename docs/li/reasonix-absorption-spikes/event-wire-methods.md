# Event Wire Methods Spike

## Source Of Truth

- Backend typed constants live in `internal/platform/eventsurface/bind.go`; legacy compatibility methods such as `ui/thread/changed` and `ui/sidebar/changed` live in `internal/platform/eventsurface/legacy.go`.
- Compatibility expansion lives in `internal/platform/eventsurface/legacy.go`, including `workspace/run/` source-event handling.
- RPC push uses `eventsurface.ExpandNotifications`.
- Wails bridge uses `eventsurface.ExpandNotifications`.

## Required Method Set

The implementation must inventory every `Method*` constant from the whole `internal/platform/eventsurface` package, including bind constants and legacy compatibility methods. The raw provider allowlist is separate from compatibility/source-event prefixes; do not silently expand raw provider visibility while stabilizing frontend parsing.

## Decision

Create `internal/platform/eventsurface/methods.go` and `frontend-app/src/shared/api/eventWireMethods.js` from the same explicit list. Backend tests read the frontend list and fail if the two sets diverge.
