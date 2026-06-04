# Super Dolphin Frontend App

Independent React/Vite client UI for the Super Agent desktop-style shell.

This folder is the current new UI for the desktop dev flow. It is intentionally
separate from `cmd/agent-terminal/frontend`, which remains the legacy
Vue/package-embed frontend path.

```bash
cd frontend-app
npm install
npm run dev
```

For the full local desktop setup, run this from the repository root:

```bash
./run-new-ui-desktop.sh
```

The script starts this app's Vite server, waits for it to become ready, then launches `cmd/agent-terminal`
with `FRONTEND_DEVSERVER_URL` so the Wails desktop host proxies to `frontend-app`.

The design follows the provided dark macOS-style screenshots: title bar, narrow left navigation, dense black workspace, chat thread rail, runtime log panel, capability pages, workflow pages, skill cards, shared files, memory center, and settings.
