# A8 Migration / Build Guard

## Scope

- Verify that the official Wails frontend package is the React client under `cmd/agent-terminal/frontend`.
- Verify that the desktop embed path builds from `cmd/agent-terminal/frontend/dist`.
- Keep `make build-plain` usable as a final local gate.

## Files Reviewed / Changed

- `cmd/agent-terminal/frontend/index.html`
- `cmd/agent-terminal/frontend/src/main.jsx`
- `cmd/agent-terminal/frontend/vite.config.js`
- `cmd/agent-terminal/frontend.go`
- `Makefile`

## Changes

- Added `BUILD_PKGS` to the Makefile, derived from `go list -f '{{if .GoFiles}}{{.ImportPath}}{{end}}' ./...`.
- Excluded the top-level `/scripts` command collection from broad Go builds.
- Reused the filtered package list in `build` and `build-plain`.

## Commands Run

```bash
make build-plain
```

## Result

- Pass.
- `make build-plain` executed:
  - `./scripts/test_with_guard.sh --guard-only`
  - `cd cmd/agent-terminal/frontend && npm run build`
  - `go build $(BUILD_PKGS)`

## Findings

- P0: none.
- P1: none.
- P2: existing Vite warnings remain for large chunks and mixed dynamic/static imports; these warnings predate this integration and do not fail the build.
- P3: chunk-splitting cleanup can be handled separately from the Wails/React API integration.
