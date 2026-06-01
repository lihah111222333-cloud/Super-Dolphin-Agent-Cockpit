# Super Dolphin Frontend App

Independent React/Vite client UI for the Super Agent desktop-style shell.

This folder is intentionally separate from `cmd/agent-terminal/frontend` so the original Wails/Vite client can keep using its existing startup scripts unchanged.

```bash
cd frontend-app
npm install
npm run dev
```

For the full local desktop/web setup, run this from the repository root:

```bash
./scripts/dev-dual-frontends.sh
```

The script keeps the original client available, starts the existing `/frontend`
web client on a separate port, and launches this app through the Wails desktop
host with its own RPC and bridge ports.

The design follows the provided dark macOS-style screenshots: title bar, narrow left navigation, dense black workspace, chat thread rail, runtime log panel, capability pages, workflow pages, skill cards, shared files, memory center, and settings.
