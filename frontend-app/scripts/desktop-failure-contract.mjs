export const DESKTOP_FAILURE_SMOKE_COMMAND = Object.freeze([
  'node',
  'scripts/desktop-failure-smoke.mjs',
]);

export const DESKTOP_FAILURE_CASE_IDS = Object.freeze([
  'terminal-failed',
  'prompt-history-reject',
]);

export const DESKTOP_FAILURE_SOURCE_PATHS = Object.freeze([
  'frontend-app/scripts/desktop-failure-contract.mjs',
  'frontend-app/scripts/desktop-failure-smoke.mjs',
  'frontend-app/tests/e2e/desktop-failure.spec.js',
  'frontend-app/playwright.failure.config.js',
  'frontend-app/package.json',
  'frontend-app/src/shared/api/wailsBridge.js',
  'internal/ui/wails/testdata/failure_smoke_host/main.go',
  'internal/provider/claudecli/event_map.go',
  'internal/provider/codexapp/event_map.go',
  'internal/provider/unified/event_map.go',
  'internal/ui/wails/bridge.go',
]);

export const DESKTOP_FAILURE_REPORT_REQUIREMENTS = Object.freeze({
  'terminal-failed': Object.freeze({
    hops: Object.freeze(['claudecli.raw', 'claudecli.adapter', 'turndto.TurnOutputDelta', 'wails.EventBridge', 'chromium.DOM', 'codexapp.raw', 'codexapp.adapter', 'turndto.TurnCompleted', 'turn/terminal', 'chromium.DOM']),
    domAssertions: Object.freeze(['partial-response-visible', 'safe-terminal-visible', 'raw-secret-absent', 'raw-private-path-absent', 'raw-stack-absent', 'legacy-remote-copy-absent']),
  }),
  'prompt-history-reject': Object.freeze({
    hops: Object.freeze(['wails.rpc', 'thread/promptHistory', 'frontend.action', 'chromium.DOM', 'retry.control', 'wails.rpc', 'chromium.DOM']),
    domAssertions: Object.freeze(['draft-preserved', 'cursor-preserved', 'retry-click-recovers']),
  }),
});
