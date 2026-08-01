export const DESKTOP_FAILURE_SMOKE_COMMAND = Object.freeze([
  'node',
  'scripts/desktop-failure-smoke.mjs',
]);

export const DESKTOP_FAILURE_SMOKE_DEFAULT_TIMEOUT_MS = 180_000;

export function resolveDesktopFailureSmokeTimeout(env = process.env) {
  const raw = env.SUPER_DOLPHIN_FAILURE_SMOKE_TIMEOUT_MS;
  if (raw == null || raw === '') return DESKTOP_FAILURE_SMOKE_DEFAULT_TIMEOUT_MS;
  const parsed = Number(raw);
  if (!Number.isInteger(parsed) || parsed <= 0) {
    throw new Error(`SUPER_DOLPHIN_FAILURE_SMOKE_TIMEOUT_MS must be a positive integer, got ${raw}`);
  }
  return parsed;
}

export function mergeDebugNamespace(current, required) {
  const namespaces = String(current || '').split(',').map((value) => value.trim()).filter(Boolean);
  if (!namespaces.includes(required)) namespaces.push(required);
  return namespaces.join(',');
}

export function commandFailureMessage(input) {
  const { command, args, code, signal, stdout, stderr } = input;
  const output = `${stdout}\n${stderr}`
    .replace(/Authorization:\s*Bearer\s+[^\s"']+/giu, "Authorization: Bearer [redacted]")
    .replace(/t03-raw-provider-secret-do-not-persist/gu, '[redacted]')
    .trim();
  const diagnostic = output.length <= 4000
    ? output
    : `${output.slice(0, 2000)}\n...[truncated]...\n${output.slice(-2000)}`;
  const playwrightFailure = playwrightFailureSummary(output);
  return `${command} ${args.join(' ')} failed: exit=${code} signal=${signal || ''}${playwrightFailure ? `\n${playwrightFailure}` : ''}${diagnostic ? `\n${diagnostic}` : ''}`;
}

function playwrightFailureSummary(output) {
  try {
    const failure = firstPlaywrightFailure(JSON.parse(output).suites);
    if (!failure) return '';
    const browser = playwrightBrowserProcessSummary(failure.result);
    return `Playwright failure: ${failure.title}${failure.message ? `: ${failure.message}` : ''}${browser ? `\nPlaywright browser process:\n${browser}` : ''}`;
  } catch {
    return '';
  }
}

function playwrightBrowserProcessSummary(result) {
  const records = [...(result?.stdout || []), ...(result?.stderr || [])];
  const lines = records.flatMap((record) => String(record?.text || record || '').split('\n'))
    .map((line) => line.replace(/\u001b\[[0-9;]*m/gu, '').trim())
    .filter((line) => line.includes('pw:browser'));
  if (lines.length <= 20) return lines.join('\n');
  const important = lines.filter((line) => /FATAL|Check failed|signal=|process did exit|SIG[A-Z]+/u.test(line));
  const nonDBusErrors = lines.filter((line) => line.includes('[err]') && !/dbus|bus\.cc/iu.test(line));
  const selected = [...lines.slice(0, 4), ...important, ...nonDBusErrors, ...lines.slice(-4)]
    .filter((line, index, all) => all.indexOf(line) === index)
    .slice(0, 20);
  return [...selected, '...[browser log filtered]...'].join('\n');
}

function firstPlaywrightFailure(suites) {
  const pending = [...(suites || [])];
  while (pending.length > 0) {
    const suite = pending.shift();
    pending.push(...(suite.suites || []));
    for (const spec of suite.specs || []) {
      const result = (spec.tests || []).flatMap((test) => test.results || []).find((item) => item.status !== 'passed');
      if (result) return { title: spec.title, message: result.errors?.[0]?.message || '', result };
    }
  }
  return null;
}

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
