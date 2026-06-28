# Super Dolphin Frontend App

Independent React/Vite client UI for the Super Agent desktop-style shell.

This folder is the current new UI for the desktop dev flow. It is intentionally
separate from `cmd/agent-terminal/frontend`, which remains the legacy
Vue/package-embed frontend path.
For package/embed builds, `make frontend-app-build` builds this app and copies
`frontend-app/dist` into `cmd/agent-terminal/frontend/dist` for Go embed.

```bash
cd frontend-app
npm install
npm run dev
```

Direct `npm run dev` and the root desktop script use polling file watches by
default to avoid Linux `ENOSPC` watcher limits. To use native file events
instead, set either `SUPER_DOLPHIN_VITE_USE_POLLING=0` or
`CHOKIDAR_USEPOLLING=0`. If both variables are set, they must resolve to the
same strict boolean value (`1/0`, `true/false`, `yes/no`, or `on/off`).

For the full local desktop setup, run one of these from the repository root:

```bash
# macOS
./run-new-ui-desktop.sh

# Windows PowerShell
.\run-new-ui-desktop.ps1
```

The selected root script starts this app's Vite server, waits for it to become ready, then launches `cmd/agent-terminal`
with `FRONTEND_DEVSERVER_URL` so the Wails desktop host proxies to `frontend-app`.

The design follows the provided dark macOS-style screenshots: title bar, narrow left navigation, dense black workspace, chat thread rail, runtime log panel, capability pages, workflow pages, skill cards, shared files, memory center, and settings.
