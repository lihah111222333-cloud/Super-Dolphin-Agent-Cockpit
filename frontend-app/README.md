# Super Dolphin Frontend App

Independent React/Vite client UI for the Super Agent desktop-style shell.

This folder is intentionally separate from `cmd/agent-terminal/frontend` so the original Wails/Vite client can keep using its existing startup scripts unchanged.

```bash
cd frontend-app
npm install
npm run dev
```

## Tencent Cloud RUM

The app initializes Tencent Cloud RUM through `aegis-web-sdk` only when a RUM application ID is configured. Without an ID, the SDK is not loaded on the main application path.

```bash
VITE_TENCENT_RUM_ID=<rum-app-id> npm run dev
```

For local configuration, copy `.env.example` to `.env.local` and fill `VITE_TENCENT_RUM_ID`.

Optional Vite env:

- `VITE_TENCENT_RUM_UIN`: non-sensitive stable user identifier passed to Aegis, such as a hashed internal ID.
- `VITE_TENCENT_RUM_HOST_URL`: optional report host; the SDK default is `https://rumt-zh.com`.
- `VITE_TENCENT_RUM_TRACE_HEADER`: `traceparent`, `sw8`, `b3`, or `sentry-trace`; defaults to `traceparent`.
- `VITE_TENCENT_RUM_TRACE_URLS`: comma-separated URL patterns that should receive trace headers. Trace headers are not injected unless this whitelist is set. Use literal strings or `regex:<pattern>`, for example `regex:^https://api\\.example\\.com/`.
- `VITE_TENCENT_RUM_TRACE_IGNORE_URLS`: comma-separated URL patterns that should not receive trace headers; the same literal and `regex:<pattern>` syntax is supported.
- `VITE_TENCENT_RUM_ENABLED=true`: fail fast when `VITE_TENCENT_RUM_ID` is missing.

The RUM URL handler removes sensitive query values, hash fragments, local filesystem paths, and long numeric path IDs before reporting. Aegis trace header injection applies to fetch/XHR requests only; the Wails `/wails/ws` WebSocket bridge is not treated as a full frontend-to-backend trace path in this app.

For the full local desktop/web setup, run this from the repository root:

```bash
./scripts/dev-dual-frontends.sh
```

The script keeps the original client available, starts the existing `/frontend`
web client on a separate port, and launches this app through the Wails desktop
host with its own RPC and bridge ports.

The design follows the provided dark macOS-style screenshots: title bar, narrow left navigation, dense black workspace, chat thread rail, runtime log panel, capability pages, workflow pages, skill cards, shared files, memory center, and settings.
