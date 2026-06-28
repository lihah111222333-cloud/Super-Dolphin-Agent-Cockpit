# Legacy Embedded Frontend

This directory is the legacy/package-embed frontend path for `cmd/agent-terminal`.

Use `frontend-app/` for current React/Vite UI work. `run-new-ui-desktop.sh` and
`run-new-ui-desktop.ps1` launch `cmd/agent-terminal` with `VITE_DEV_URL`, so the
desktop host proxies the current React UI from `frontend-app`.

Only edit or build this directory when a task explicitly targets the legacy Vue
frontend or the package-embed fallback path. Without a dev proxy,
`cmd/agent-terminal` serves embedded assets from this package's `dist/`.
