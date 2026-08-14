# Repository Map

## Runtime and commands

- Current UI: `frontend-app`, React 19 + Vite 8, served locally with `npm run dev` on `http://127.0.0.1:5175/`.
- Desktop host: `run-new-ui-desktop.sh` starts the Vite UI and `cmd/agent-terminal`; this prototype does not change the host.
- Required frontend verification: `npm run lint`, `npm test`, and `npm run build` from `frontend-app`.

## Relevant architecture

- `frontend-app/src/pages/chat/ChatPage.jsx` owns the chat surface composition and mounts the conversation, intro, thread rail, and runtime/agent side panels.
- `frontend-app/src/pages/chat/thread/Conversation.jsx` owns message flow, scrolling, and composer placement.
- `frontend-app/src/pages/chat/ChatPage.css` and `ChatPageWorkbench.css` own the chat layout and current intro/composer/message styling.
- `frontend-app/src/AppShellWorkbench.css` provides the outer `.chat-page` size and workbench background.

## Existing design and implementation patterns

- Styling uses repository CSS variables (`--surface`, `--bg`, `--text-pri`, motion and z-index tokens) and scoped page classes; no new UI framework is needed.
- Decorative UI is mounted inside `ChatPage` with pointer events disabled so chat interactions keep their existing ownership.
- The reference specimen uses a sky/light field plus multiple animated wave layers, with day/night palettes and reduced-motion-compatible CSS animation.
- Existing chat content already uses transparent timeline layers and a floating/docked composer, so an atmosphere layer can sit behind the current DOM without changing message behavior.

## Data and integration boundaries

- Real thread/message/composer state remains owned by the Zustand client store and existing Wails RPC adapters.
- Backend availability may fail in browser-only Vite mode; that failure remains visible and is not mocked or hidden.
- The prototype is visual only: it adds no DTOs, RPC calls, persisted preferences, or new external assets.

## Verification tools

- Vitest + Testing Library for chat DOM/accessibility regression coverage.
- ESLint and Vite production build for source/build validity.
- Repository LSP peer for symbol location, impact analysis, exact reads, edits, and diagnostics.
- In-app Browser screenshots and DOM snapshots at desktop and compact widths for visual/interaction evidence.
