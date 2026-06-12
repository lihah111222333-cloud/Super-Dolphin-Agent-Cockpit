# Screenshot UI Redesign QA

Final result: passed against the real desktop HTTP host.

## Scope

- Worktree: `D:\project\Super-Dolphin-worktrees\screenshot-ui-redesign`
- Branch: `codex/screenshot-ui-redesign`
- Reference targets: uploaded Super Dolphin empty chat and active chat screenshots.
- Changed surface: React/Vite `frontend-app` application shell, chat page, composer, style tests, related fixtures, and a narrow Windows startup script fix for invalid PATH entries.

## Browser Checks

- Desktop viewport `1600x1250` on `http://127.0.0.1:5176`.
- Confirmed `.app-sidebar` renders at `324px` wide with the Super Dolphin bitmap logo, new chat button, primary navigation, project tree, secondary entries, and Settings.
- Confirmed old `.nav-rail` and `.titlebar` are not rendered in the new shell.
- Confirmed active chat state renders the new top header, right-aligned blue user bubble, assistant card, and bottom composer.
- Desktop screenshot: `frontend-app/design-qa-active-chat.png`.
- Mobile viewport `390x844`.
- Confirmed `documentElement.scrollWidth` and `body.scrollWidth` stay at `390px`, avoiding horizontal overflow.
- Confirmed composer wraps inside the viewport and does not overlap the header.
- Mobile loaded screenshot: `frontend-app/design-qa-mobile-chat-loaded.png`.

## Backend Host Check

- Started with `run-new-ui-desktop.ps1` from the isolated worktree.
- Confirmed `http://127.0.0.1:4512/metrics` returns HTTP 200.
- Confirmed listeners on `127.0.0.1:4512` (desktop HTTP bridge), `127.0.0.1:5175` (frontend-app Vite), and `127.0.0.1:55433` (local PostgreSQL).
- Browser validation on `http://127.0.0.1:4512/` confirmed no `连接后端失败` banner.
- Browser validation clicked `新会话`, confirmed `让我们从 Super-Dolphin 开始!`, filled the composer, opened `聊天操作`, and toggled the runtime side panel.

## Automated Verification

- `npm run lint`
- `npm test`
- `npm run build`
- `npx react-doctor@latest --verbose --diff`
- `npx playwright test tests/e2e/desktop-ux.spec.js --config=playwright.desktop.config.js`
