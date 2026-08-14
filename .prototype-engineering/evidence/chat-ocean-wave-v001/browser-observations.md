# Browser observations

- Reference inspected at `http://127.0.0.1:5173/frontend-showcase/#/items/css-ocean-wave`: the transferable visual system is a luminous sky/orb field, horizon line, and three layered wave bands with slow independent motion.
- Dark intro screenshot: `chat-ocean-dark.png`; the moon, horizon, wave contours, suggestion cards, composer, side-panel shortcut, and bottom workbench remain visually separated.
- Light intro screenshot: `chat-ocean-light.png`; the same structure maps to a clear daylight palette and keeps the heading/composer readable.
- The atmosphere is `aria-hidden`, pointer-transparent, and contains exactly three `.chat-ocean-wave` layers.
- Browser console warnings were limited to the expected Wails WebSocket/trace bridge failures because the visual check used the standalone Vite server without the Go desktop host.
- The in-app Browser viewport override reported success but retained a 1280×720 page viewport after reload and in a new tab. Compact behavior is therefore covered by the scoped `max-width: 760px` CSS and automated source assertion, but lacks a trustworthy compact screenshot in this run.
- The existing bootstrap failure state remained explicit; no fallback or mock backend was introduced.
