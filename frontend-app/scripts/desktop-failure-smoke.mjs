import { spawn } from 'node:child_process';
import { existsSync } from 'node:fs';
import { mkdir, readFile, rm, writeFile } from 'node:fs/promises';
import { createHash } from 'node:crypto';
import net from 'node:net';
import path from 'node:path';
import { setTimeout as sleep } from 'node:timers/promises';
import { fileURLToPath } from 'node:url';
import { chromium } from '@playwright/test';
import {
  commandFailureMessage,
  DESKTOP_FAILURE_CASE_IDS,
  DESKTOP_FAILURE_REPORT_REQUIREMENTS,
  DESKTOP_FAILURE_SMOKE_COMMAND,
  DESKTOP_FAILURE_SOURCE_PATHS,
  mergeDebugNamespace,
  resolveDesktopFailureSmokeTimeout,
} from './desktop-failure-contract.mjs';

export { DESKTOP_FAILURE_SOURCE_PATHS } from './desktop-failure-contract.mjs';

const DEFAULT_BACKEND_ADDR = '127.0.0.1:4514';
const DEFAULT_VITE_URL = 'http://127.0.0.1:5178';
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
  if (ids.length !== DESKTOP_FAILURE_CASE_IDS.length || new Set(ids).size !== ids.length
    || ids.some((caseId, index) => caseId !== DESKTOP_FAILURE_CASE_IDS[index])) {
    throw new Error(`desktop failure caseIds exact diff failed: ${JSON.stringify(ids)}`);
  }
  const terminal = cases[0];
  if (terminal.status !== 'runnable' || terminal.evidenceLayers.length !== 3
    || terminal.evidenceLayers[0] !== 'claude-raw-adapter' || terminal.evidenceLayers[1] !== 'codex-raw-adapter'
    || terminal.evidenceLayers[2] !== 'dto-wails-dom') {
    throw new Error('terminal-failed requires both raw provider adapters and real DOM evidence');
  }
  const prompt = cases[1];
  if (prompt.status !== 'runnable' || JSON.stringify(prompt.evidenceLayers) !== JSON.stringify(['go-rpc-websocket', 'prompt-history-action', 'real-chromium-dom'])
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
    timeoutMs: resolveDesktopFailureSmokeTimeout(env),
    chromeExecutable: resolveChromiumExecutable(env),
    backendBinary: path.join(repoRoot, '.tmp', 'desktop-failure-smoke', `failure-smoke-host-${process.pid}`),
    reportPath: env.SUPER_DOLPHIN_FAILURE_SMOKE_REPORT
      || path.join(repoRoot, '.tmp', 'desktop-failure-smoke', 'report.json'),
  };
}

export async function runDesktopFailureSmoke(config = desktopFailureSmokeConfig(), deps = {}) {
  validateDesktopFailureCases();
  await assertPortsFree([config.backendAddr, new URL(config.viteURL).host]);
  const spawnImpl = deps.spawn || spawn;
  let backend;
  let vite;
  let goBuild;
  let playwrightRun;
  let smokeError;
  try {
    await mkdir(path.dirname(config.backendBinary), { recursive: true });
    goBuild = await runCommand('go', [
      'build',
      '-o',
      config.backendBinary,
      './internal/ui/wails/testdata/failure_smoke_host',
    ], config.repoRoot, process.env, spawnImpl, config.timeoutMs);
    backend = startTrackedProcess(config.backendBinary, [
      `--addr=${config.backendAddr}`,
      `--project=${config.repoRoot}`,
    ], config.repoRoot, process.env, spawnImpl);
    vite = startTrackedProcess('npm', ['run', 'dev', '--', '--port', new URL(config.viteURL).port, '--strictPort'], config.frontendRoot, {
      ...process.env,
      SUPER_DOLPHIN_HTTP_ADDR: config.backendAddr,
    }, spawnImpl);
    await Promise.all([
      waitForHTTP(`http://${config.backendAddr}/healthz`, config.timeoutMs, backend.child),
      waitForHTTP(config.viteURL, config.timeoutMs, vite.child),
    ]);
    const playwright = path.join(config.frontendRoot, 'node_modules', '.bin', 'playwright');
    if (!existsSync(playwright)) throw new Error(`missing Playwright binary: ${playwright}`);
    playwrightRun = await runCommand(playwright, ['test', '--config', 'playwright.failure.config.js', '--reporter=json'], config.frontendRoot, {
      ...process.env,
      DEBUG: mergeDebugNamespace(process.env.DEBUG, 'pw:browser*'), PLAYWRIGHT_CHROMIUM_EXECUTABLE: config.chromeExecutable,
      SUPER_DOLPHIN_FAILURE_SMOKE_BASE_URL: config.viteURL,
    }, spawnImpl, config.timeoutMs);
  } catch (error) {
    smokeError = error;
  } finally {
    const [viteRun, backendRun] = await Promise.all([stopTrackedProcess(vite), stopTrackedProcess(backend)]);
    await rm(config.backendBinary, { force: true });
    if (smokeError) throw smokeError;
    const report = validateDesktopFailureReport(await buildDesktopFailureReport(config, deps.git, {
      goBuild,
      playwrightRun,
      viteRun,
      backendRun,
    }));
    await mkdir(path.dirname(config.reportPath), { recursive: true });
    await writeFile(config.reportPath, `${JSON.stringify(report, null, 2)}\n`, 'utf8');
    return report;
  }
}

async function buildDesktopFailureReport(config, gitImpl, executions) {
  const git = gitImpl || ((args) => captureCommand('git', args, config.repoRoot));
  const sourceHashes = Object.fromEntries(await Promise.all(DESKTOP_FAILURE_SOURCE_PATHS.map(async (sourcePath) => {
    const content = await readFile(path.join(config.repoRoot, sourcePath));
    return [sourcePath, createHash('sha256').update(content).digest('hex')];
  })));
  const playwrightCases = playwrightCaseResults(executions.playwrightRun.stdout);
  return {
    schemaVersion: 2,
    generatedAt: new Date().toISOString(),
    subjectSha: (await git(['rev-parse', 'HEAD'])).trim(),
    subjectTreeSha: (await git(['rev-parse', 'HEAD^{tree}'])).trim(),
    controlId: 'T03-wails-integration',
    cwd: config.repoRoot,
    argv: DESKTOP_FAILURE_SMOKE_COMMAND,
    caseIds: DESKTOP_FAILURE_CASE_IDS,
    testCount: DESKTOP_FAILURE_CASE_IDS.length,
    status: 'covered',
    blockedCases: [],
    sourceHashes,
    execution: {
      goBuild: executionEvidence(executions.goBuild, config.repoRoot),
      playwright: {
        ...executionEvidence(executions.playwrightRun, config.frontendRoot),
        testCount: playwrightCases.length,
      },
      wailsHost: executionEvidence(executions.backendRun, config.repoRoot),
      vite: executionEvidence(executions.viteRun, config.frontendRoot),
    },
    cases: [
      {
        caseId: 'terminal-failed', result: playwrightCase(playwrightCases, 'terminal-failed').result,
        command: ['node', 'scripts/desktop-failure-smoke.mjs'],
        hops: playwrightCase(playwrightCases, 'terminal-failed').hops,
        domAssertions: playwrightCase(playwrightCases, 'terminal-failed').domAssertions,
        secretAssertions: ['dom-does-not-contain-raw-provider-secret', 'report-does-not-contain-raw-provider-secret'],
        execution: playwrightCase(playwrightCases, 'terminal-failed'),
      },
      {
        caseId: 'prompt-history-reject', result: playwrightCase(playwrightCases, 'prompt-history-reject').result,
        command: ['node', 'scripts/desktop-failure-smoke.mjs'],
        hops: playwrightCase(playwrightCases, 'prompt-history-reject').hops,
        domAssertions: playwrightCase(playwrightCases, 'prompt-history-reject').domAssertions,
        secretAssertions: ['dom-does-not-contain-raw-provider-secret', 'report-does-not-contain-raw-provider-secret'],
        execution: playwrightCase(playwrightCases, 'prompt-history-reject'),
      },
    ],
  };
}

function executionEvidence(execution, cwd) {
  return {
    argv: [execution.command, ...execution.args],
    cwd: path.relative(repoRootFromScript(), cwd) || '.',
    exitCode: execution.exitCode,
    signal: execution.signal,
    outputSha256: createHash('sha256').update(`${execution.stdout}\n${execution.stderr}`).digest('hex'),
  };
}

function playwrightCaseResults(output) {
  const report = JSON.parse(output);
  const cases = [];
  const walk = (suite) => {
    for (const spec of suite.specs || []) {
      const result = spec.tests?.[0]?.results?.[0];
      if (result) {
        const evidence = playwrightAttachmentEvidence(result, spec.title);
        cases.push({
          title: spec.title,
          status: result.status,
          duration: result.duration,
          hops: evidence.hops,
          domAssertions: evidence.domAssertions,
        });
      }
    }
    for (const child of suite.suites || []) walk(child);
  };
  for (const suite of report.suites || []) walk(suite);
  if (cases.length !== DESKTOP_FAILURE_CASE_IDS.length || cases.some(({ status }) => status !== 'passed')) {
    throw new Error('Playwright desktop failure cases did not produce two passed results');
  }
  return cases;
}

function playwrightCase(cases, caseId) {
  const result = cases.find(({ title }) => title.startsWith(caseId));
  if (!result) throw new Error(`Playwright case result is missing: ${caseId}`);
  return {
    title: result.title,
    status: result.status,
    result: result.status === 'passed' ? 'GREEN' : 'RED',
    durationMs: result.duration,
    hops: result.hops,
    domAssertions: result.domAssertions,
  };
}

function playwrightAttachmentEvidence(result, title) {
  const attachment = result.attachments?.find(({ name, contentType }) => (
    name === 't03-execution-evidence' && contentType === 'application/json'
  ));
  if (!attachment?.body) throw new Error(`Playwright execution evidence attachment is missing: ${title}`);
  const parsed = JSON.parse(Buffer.from(attachment.body, 'base64').toString('utf8'));
  if (!Array.isArray(parsed.hops) || parsed.hops.some((item) => typeof item !== 'string')
    || !Array.isArray(parsed.domAssertions) || parsed.domAssertions.some((item) => typeof item !== 'string')) {
    throw new Error(`Playwright execution evidence attachment is invalid: ${title}`);
  }
  return parsed;
}

export function validateDesktopFailureReport(report) {
  const expected = DESKTOP_FAILURE_CASES.map(({ caseId }) => caseId);
  const actual = Array.isArray(report?.caseIds) ? report.caseIds : [];
  if (actual.length !== expected.length || new Set(actual).size !== actual.length
    || actual.some((caseId, index) => caseId !== expected[index])) {
    throw new Error(`desktop failure report caseIds exact diff failed: ${JSON.stringify(actual)}`);
  }
  if (report.schemaVersion !== 2 || !report.sourceHashes
    || JSON.stringify(Object.keys(report.sourceHashes).sort()) !== JSON.stringify([...DESKTOP_FAILURE_SOURCE_PATHS].sort())) {
    throw new Error('desktop failure report requires source-hashed v2 evidence');
  }
  if (report.testCount !== expected.length || !Number.isInteger(report.testCount) || report.testCount <= 0) {
    throw new Error(`desktop failure report testCount must equal ${expected.length}`);
  }
  if (report.status !== 'covered' || !Array.isArray(report.blockedCases) || report.blockedCases.length !== 0) {
    throw new Error('desktop failure report requires covered status with zero blocked cases');
  }
  for (const [name, expectedCwd] of [['goBuild', '.'], ['playwright', 'frontend-app'], ['wailsHost', '.'], ['vite', 'frontend-app']]) {
    const execution = report.execution?.[name];
    if (!Array.isArray(execution?.argv) || execution.argv.length === 0 || execution.cwd !== expectedCwd
      || !Number.isInteger(execution.exitCode) && execution.exitCode !== null
      || execution.signal !== null && typeof execution.signal !== 'string'
      || !/^[0-9a-f]{64}$/u.test(execution.outputSha256 || '')) {
      throw new Error(`desktop failure report execution evidence invalid: ${name}`);
    }
  }
  if (report.execution.goBuild.exitCode !== 0 || report.execution.goBuild.signal !== null
    || report.execution.playwright.exitCode !== 0 || report.execution.playwright.signal !== null
    || report.execution.playwright.testCount !== expected.length) {
    throw new Error('desktop failure report command execution must prove Go and two Playwright cases passed');
  }
  if (!Array.isArray(report.cases) || report.cases.length !== expected.length) {
    throw new Error('desktop failure report requires per-case evidence');
  }
  for (const [index, evidence] of report.cases.entries()) {
    const requirement = DESKTOP_FAILURE_REPORT_REQUIREMENTS[expected[index]];
    if (evidence?.caseId !== expected[index] || evidence.result !== 'GREEN'
      || JSON.stringify(evidence.command) !== JSON.stringify(DESKTOP_FAILURE_SMOKE_COMMAND)
      || JSON.stringify(evidence.hops) !== JSON.stringify(requirement.hops)
      || JSON.stringify(evidence.domAssertions) !== JSON.stringify(requirement.domAssertions)
      || !Array.isArray(evidence.secretAssertions) || evidence.secretAssertions.length !== 2
      || evidence.execution?.status !== 'passed' || typeof evidence.execution.durationMs !== 'number') {
      throw new Error(`desktop failure report case evidence invalid: ${expected[index]}`);
    }
  }
  if (JSON.stringify(report).includes('t03-raw-provider-secret-do-not-persist')) {
    throw new Error('desktop failure report leaked raw provider secret');
  }
  return report;
}

function startTrackedProcess(command, args, cwd, env, spawnImpl) {
  const child = spawnImpl(command, args, { cwd, env, stdio: ['ignore', 'pipe', 'pipe'], detached: true });
  let stdout = '';
  let stderr = '';
  child.stdout?.on('data', (chunk) => { stdout += chunk; });
  child.stderr?.on('data', (chunk) => { stderr += chunk; });
  const exited = new Promise((resolve, reject) => {
    child.once('error', reject);
    child.once('exit', (exitCode, signal) => resolve({ command, args, exitCode, signal, stdout, stderr }));
  });
  return { child, exited };
}

async function stopTrackedProcess(process) {
  if (!process) return { command: '', args: [], exitCode: null, signal: null, stdout: '', stderr: '' };
  await stopProcessTree(process.child);
  return process.exited;
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
    const child = spawnImpl(command, args, { cwd, env, stdio: ['ignore', 'pipe', 'pipe'] });
    let stdout = '';
    let stderr = '';
    child.stdout?.on('data', (chunk) => { stdout += chunk; });
    child.stderr?.on('data', (chunk) => { stderr += chunk; });
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
        finish(() => resolve({ command, args, exitCode: 0, signal: null, stdout, stderr }));
        return;
      }
      finish(() => reject(new Error(commandFailureMessage({ command, args, code, signal, stdout, stderr }))));
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
