import { spawn } from 'node:child_process';
import { existsSync } from 'node:fs';
import { mkdir, readFile, rm, writeFile } from 'node:fs/promises';
import { createHash } from 'node:crypto';
import net from 'node:net';
import path from 'node:path';
import { setTimeout as sleep } from 'node:timers/promises';
import { fileURLToPath } from 'node:url';
import { chromium } from '@playwright/test';

const DEFAULT_BACKEND_ADDR = '127.0.0.1:4514';
const DEFAULT_VITE_URL = 'http://127.0.0.1:5178';
const DEFAULT_TIMEOUT_MS = 180000;

export const DESKTOP_FAILURE_CASES = Object.freeze([
  Object.freeze({
    caseId: 'terminal-failed',
    status: 'runnable',
    evidenceLayers: ['claude-raw-adapter', 'codex-raw-adapter', 'dto-wails-dom'],
  }),
  Object.freeze({
    caseId: 'prompt-history-reject',
    status: 'runnable',
    evidenceLayers: ['go-rpc-websocket', 'prompt-history-action', 'real-chromium-dom'],
    fixtureContract: Object.freeze({
      method: 'thread/promptHistory',
      preserve: ['draft', 'cursor'],
      visibleError: true,
      retryRecovery: true,
    }),
  }),
]);

export function validateDesktopFailureCases(cases = DESKTOP_FAILURE_CASES) {
  const ids = cases.map((entry) => entry.caseId);
  if (ids.length !== 2 || new Set(ids).size !== 2 || ids[0] !== 'terminal-failed' || ids[1] !== 'prompt-history-reject') {
    throw new Error(`desktop failure caseIds exact diff failed: ${JSON.stringify(ids)}`);
  }
  const terminal = cases[0];
  if (terminal.status !== 'runnable' || terminal.evidenceLayers.length !== 3
    || terminal.evidenceLayers[0] !== 'claude-raw-adapter' || terminal.evidenceLayers[1] !== 'codex-raw-adapter') {
    throw new Error('terminal-failed requires both raw provider adapters and real DOM evidence');
  }
  const prompt = cases[1];
  if (prompt.status !== 'runnable' || prompt.evidenceLayers?.length !== 3
    || prompt.fixtureContract?.method !== 'thread/promptHistory'
    || !prompt.fixtureContract?.visibleError || !prompt.fixtureContract?.retryRecovery) {
    throw new Error('prompt-history-reject requires three executable evidence layers and retry recovery');
  }
  return cases;
}

export function repoRootFromScript(metaURL = import.meta.url) {
  return path.resolve(path.dirname(fileURLToPath(metaURL)), '..', '..');
}

export function resolveChromiumExecutable(env = process.env, exists = existsSync) {
  const candidates = [
    env.PLAYWRIGHT_CHROMIUM_EXECUTABLE,
    chromium.executablePath(),
    '/Applications/Google Chrome.app/Contents/MacOS/Google Chrome',
    '/Applications/Microsoft Edge.app/Contents/MacOS/Microsoft Edge',
    '/usr/bin/google-chrome',
  ].filter(Boolean);
  const executable = candidates.find((candidate) => exists(candidate));
  if (!executable) throw new Error(`PLAYWRIGHT_CHROMIUM_EXECUTABLE is required; checked ${candidates.join(', ')}`);
  return executable;
}

export function desktopFailureSmokeConfig(env = process.env, repoRoot = repoRootFromScript()) {
  return {
    repoRoot,
    frontendRoot: path.join(repoRoot, 'frontend-app'),
    backendAddr: env.SUPER_DOLPHIN_FAILURE_SMOKE_BACKEND_ADDR || DEFAULT_BACKEND_ADDR,
    viteURL: env.SUPER_DOLPHIN_FAILURE_SMOKE_VITE_URL || DEFAULT_VITE_URL,
    timeoutMs: positiveInt(env.SUPER_DOLPHIN_FAILURE_SMOKE_TIMEOUT_MS, DEFAULT_TIMEOUT_MS),
    chromeExecutable: resolveChromiumExecutable(env),
    backendBinary: path.join(repoRoot, '.tmp', 'desktop-failure-smoke', `failure-smoke-host-${process.pid}`),
    reportPath: env.SUPER_DOLPHIN_FAILURE_SMOKE_REPORT
      || path.join(repoRoot, '.tmp', 'desktop-failure-smoke', 'report.json'),
  };
}

function positiveInt(value, fallback) {
  if (value == null || value === '') return fallback;
  const parsed = Number(value);
  if (!Number.isInteger(parsed) || parsed <= 0) throw new Error(`expected positive integer, got ${value}`);
  return parsed;
}

export async function runDesktopFailureSmoke(config = desktopFailureSmokeConfig(), deps = {}) {
  validateDesktopFailureCases();
  await assertPortsFree([config.backendAddr, new URL(config.viteURL).host]);
  const spawnImpl = deps.spawn || spawn;
  let backend;
  let vite;
  try {
    await mkdir(path.dirname(config.backendBinary), { recursive: true });
    await runCommand('go', [
      'build',
      '-o',
      config.backendBinary,
      './internal/ui/wails/testdata/failure_smoke_host',
    ], config.repoRoot, process.env, spawnImpl, config.timeoutMs);
    backend = spawnImpl(config.backendBinary, [
      `--addr=${config.backendAddr}`,
      `--project=${config.repoRoot}`,
    ], {
      cwd: config.repoRoot,
      stdio: 'inherit',
      detached: true,
    });
    vite = spawnImpl('npm', ['run', 'dev', '--', '--port', new URL(config.viteURL).port, '--strictPort'], {
      cwd: config.frontendRoot,
      env: { ...process.env, SUPER_DOLPHIN_HTTP_ADDR: config.backendAddr },
      stdio: 'inherit',
      detached: true,
    });
    await Promise.all([
      waitForHTTP(`http://${config.backendAddr}/healthz`, config.timeoutMs, backend),
      waitForHTTP(config.viteURL, config.timeoutMs, vite),
    ]);
    const playwright = path.join(config.frontendRoot, 'node_modules', '.bin', 'playwright');
    if (!existsSync(playwright)) throw new Error(`missing Playwright binary: ${playwright}`);
    await runCommand(playwright, ['test', '--config', 'playwright.failure.config.js'], config.frontendRoot, {
      ...process.env,
      PLAYWRIGHT_CHROMIUM_EXECUTABLE: config.chromeExecutable,
      SUPER_DOLPHIN_FAILURE_SMOKE_BASE_URL: config.viteURL,
    }, spawnImpl, config.timeoutMs);
    const report = validateDesktopFailureReport(await buildDesktopFailureReport(config, deps.git));
    await mkdir(path.dirname(config.reportPath), { recursive: true });
    await writeFile(config.reportPath, `${JSON.stringify(report, null, 2)}\n`, 'utf8');
    return report;
  } finally {
    await Promise.all([stopProcessTree(vite), stopProcessTree(backend)]);
    await rm(config.backendBinary, { force: true });
  }
}

async function buildDesktopFailureReport(config, gitImpl) {
  const git = gitImpl || ((args) => captureCommand('git', args, config.repoRoot));
  const sourcePaths = [
    'frontend-app/scripts/desktop-failure-smoke.mjs',
    'frontend-app/tests/e2e/desktop-failure.spec.js',
    'internal/ui/wails/testdata/failure_smoke_host/main.go',
    'internal/provider/claudecli/event_map.go',
    'internal/provider/codexapp/event_map.go',
    'internal/provider/unified/event_map.go',
    'internal/ui/wails/bridge.go',
  ];
  const sourceHashes = Object.fromEntries(await Promise.all(sourcePaths.map(async (sourcePath) => {
    const content = await readFile(path.join(config.repoRoot, sourcePath));
    return [sourcePath, createHash('sha256').update(content).digest('hex')];
  })));
  return {
    schemaVersion: 2,
    generatedAt: new Date().toISOString(),
    subjectSha: (await git(['rev-parse', 'HEAD'])).trim(),
    subjectTreeSha: (await git(['rev-parse', 'HEAD^{tree}'])).trim(),
    controlId: 'T03-wails-integration',
    cwd: config.repoRoot,
    argv: ['node', 'scripts/desktop-failure-smoke.mjs'],
    caseIds: ['terminal-failed', 'prompt-history-reject'],
    testCount: 2,
    status: 'covered',
    blockedCases: [],
    sourceHashes,
    cases: [
      {
        caseId: 'terminal-failed', result: 'GREEN',
        command: ['node', 'scripts/desktop-failure-smoke.mjs'],
        hops: ['claudecli.raw', 'claudecli.adapter', 'turndto.TurnOutputDelta', 'wails.EventBridge', 'chromium.DOM', 'codexapp.raw', 'codexapp.adapter', 'turndto.TurnCompleted', 'turn/terminal', 'chromium.DOM'],
        domAssertions: ['partial-response-visible', 'safe-terminal-visible', 'raw-secret-absent'],
        secretAssertions: ['dom-does-not-contain-raw-provider-secret', 'report-does-not-contain-raw-provider-secret'],
      },
      {
        caseId: 'prompt-history-reject', result: 'GREEN',
        command: ['node', 'scripts/desktop-failure-smoke.mjs'],
        hops: ['wails.rpc', 'thread/promptHistory', 'frontend.action', 'chromium.DOM', 'retry.control', 'wails.rpc', 'chromium.DOM'],
        domAssertions: ['draft-preserved', 'cursor-preserved', 'retry-click-recovers'],
        secretAssertions: ['dom-does-not-contain-raw-provider-secret', 'report-does-not-contain-raw-provider-secret'],
      },
    ],
  };
}

export function validateDesktopFailureReport(report) {
  const expected = DESKTOP_FAILURE_CASES.map(({ caseId }) => caseId);
  const actual = Array.isArray(report?.caseIds) ? report.caseIds : [];
  if (actual.length !== expected.length || new Set(actual).size !== actual.length
    || actual.some((caseId, index) => caseId !== expected[index])) {
    throw new Error(`desktop failure report caseIds exact diff failed: ${JSON.stringify(actual)}`);
  }
  if (report.schemaVersion !== 2 || !report.sourceHashes || Object.keys(report.sourceHashes).length < 7) {
    throw new Error('desktop failure report requires source-hashed v2 evidence');
  }
  if (report.testCount !== expected.length || !Number.isInteger(report.testCount) || report.testCount <= 0) {
    throw new Error(`desktop failure report testCount must equal ${expected.length}`);
  }
  if (report.status !== 'covered' || !Array.isArray(report.blockedCases) || report.blockedCases.length !== 0) {
    throw new Error('desktop failure report requires covered status with zero blocked cases');
  }
  if (!Array.isArray(report.cases) || report.cases.length !== expected.length) {
    throw new Error('desktop failure report requires per-case evidence');
  }
  for (const [index, evidence] of report.cases.entries()) {
    if (evidence?.caseId !== expected[index] || evidence.result !== 'GREEN'
      || JSON.stringify(evidence.command) !== JSON.stringify(['node', 'scripts/desktop-failure-smoke.mjs'])
      || !Array.isArray(evidence.hops) || evidence.hops.length < 6
      || !Array.isArray(evidence.domAssertions) || evidence.domAssertions.length < 3
      || !Array.isArray(evidence.secretAssertions) || evidence.secretAssertions.length !== 2) {
      throw new Error(`desktop failure report case evidence invalid: ${expected[index]}`);
    }
  }
  if (JSON.stringify(report).includes('t03-raw-provider-secret-do-not-persist')) {
    throw new Error('desktop failure report leaked raw provider secret');
  }
  return report;
}

async function assertPortsFree(values) {
  for (const value of values) await assertPortFree(value);
}

export function assertPortFree(hostPort) {
  const index = hostPort.lastIndexOf(':');
  if (index <= 0) return Promise.reject(new Error(`host:port is required, got ${hostPort}`));
  const host = hostPort.slice(0, index);
  const port = Number(hostPort.slice(index + 1));
  if (!Number.isInteger(port) || port <= 0) return Promise.reject(new Error(`valid port is required, got ${hostPort}`));
  return new Promise((resolve, reject) => {
    const server = net.createServer();
    server.once('error', (error) => reject(new Error(`port ${port} is already in use on ${host}: ${error.message}`)));
    server.listen(port, host, () => server.close(resolve));
  });
}

async function waitForHTTP(url, timeoutMs, child) {
  const deadline = Date.now() + timeoutMs;
  while (Date.now() < deadline) {
    if (child.exitCode != null || child.signalCode != null) {
      throw new Error(`failure smoke process exited before readiness: exit=${child.exitCode} signal=${child.signalCode}`);
    }
    try {
      const response = await fetch(url);
      if (response.ok) return;
    } catch {
      // Polling is bounded by timeout and the child exit check above.
    }
    await sleep(250);
  }
  throw new Error(`timed out waiting for ${url}`);
}

function runCommand(command, args, cwd, env, spawnImpl, timeoutMs) {
  return new Promise((resolve, reject) => {
    const child = spawnImpl(command, args, { cwd, env, stdio: 'inherit' });
    let settled = false;
    let timedOut = false;
    const finish = (callback) => {
      if (settled) return;
      settled = true;
      clearTimeout(timer);
      callback();
    };
    const timer = setTimeout(() => {
      timedOut = true;
      void stopProcessTree(child).then(() => {
        finish(() => reject(new Error(`${command} ${args.join(' ')} timed out after ${timeoutMs}ms`)));
      }, (error) => {
        finish(() => reject(error));
      });
    }, timeoutMs);
    child.once('error', (error) => finish(() => reject(error)));
    child.once('exit', (code, signal) => {
      if (timedOut) return;
      if (code === 0) {
        finish(resolve);
        return;
      }
      finish(() => reject(new Error(`${command} ${args.join(' ')} failed: exit=${code} signal=${signal || ''}`)));
    });
  });
}

function captureCommand(command, args, cwd) {
  return new Promise((resolve, reject) => {
    const child = spawn(command, args, { cwd, stdio: ['ignore', 'pipe', 'pipe'] });
    let stdout = '';
    let stderr = '';
    child.stdout.on('data', (chunk) => { stdout += chunk; });
    child.stderr.on('data', (chunk) => { stderr += chunk; });
    child.once('error', reject);
    child.once('exit', (code) => {
      if (code === 0) resolve(stdout);
      else reject(new Error(`${command} ${args.join(' ')} failed: ${stderr}`));
    });
  });
}

async function stopProcessTree(child) {
  if (!child || child.pid == null || child.exitCode != null || child.signalCode != null) return;
  try {
    process.kill(-child.pid, 'SIGTERM');
  } catch {
    child.kill('SIGTERM');
  }
  await Promise.race([
    new Promise((resolve) => child.once('exit', resolve)),
    sleep(10000).then(() => {
      try {
        process.kill(-child.pid, 'SIGKILL');
      } catch {
        child.kill('SIGKILL');
      }
    }),
  ]);
}

export async function packageScriptIncludesFailureSmoke(packageJSONPath = path.join(repoRootFromScript(), 'frontend-app', 'package.json')) {
  const { readFile } = await import('node:fs/promises');
  const pkg = JSON.parse(await readFile(packageJSONPath, 'utf8'));
  return pkg.scripts?.['smoke:desktop:failure'] === 'node scripts/desktop-failure-smoke.mjs';
}

export function isMain(metaURL, argv1) {
  return argv1 ? path.resolve(fileURLToPath(metaURL)) === path.resolve(argv1) : false;
}

if (isMain(import.meta.url, process.argv[1])) {
  runDesktopFailureSmoke().then((report) => {
    console.log(`desktop failure smoke validated: status=${report.status} cases=${report.testCount} blocked=${report.blockedCases.length}`);
  }).catch((error) => {
    console.error(`desktop failure smoke failed: ${error.message}`);
    process.exitCode = 1;
  });
}
