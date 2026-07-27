import { spawn } from 'node:child_process';
import { randomUUID } from 'node:crypto';
import { readFile } from 'node:fs/promises';
import path from 'node:path';
import { setTimeout as sleep } from 'node:timers/promises';
import { fileURLToPath } from 'node:url';
import WebSocket from 'ws';

import { terminateProcessTree } from './managed-command.mjs';

const DEFAULT_HTTP_ADDR = '127.0.0.1:4512';
const DEFAULT_VITE_URL = 'http://127.0.0.1:5175';
const DEFAULT_TIMEOUT_MS = 180000;
const DEFAULT_RPC_TIMEOUT_MS = 30000;
const WAILS_WS_COOKIE_NAME = 'super_dolphin_wails_ws';

export function repoRootFromScript(metaURL = import.meta.url) {
  return path.resolve(path.dirname(fileURLToPath(metaURL)), '..', '..');
}

export function truthyEnv(value) {
  return value === '1' || value === 'true' || value === 'yes';
}

export function buildWebSocketURL(addr) {
  const value = (addr || '').trim();
  if (!value) throw new Error('SUPER_DOLPHIN_HTTP_ADDR is required');
  if (value.startsWith('ws://') || value.startsWith('wss://')) return withWSPath(value);
  if (value.startsWith('http://')) return withWSPath(`ws://${value.slice('http://'.length)}`);
  if (value.startsWith('https://')) return withWSPath(`wss://${value.slice('https://'.length)}`);
  return withWSPath(`ws://${value}`);
}

export function buildWebSocketOptions(wsURL, token) {
  const parsed = new URL(wsURL);
  const normalizedToken = String(token || '').trim();
  if (!normalizedToken || /[;\r\n]/u.test(normalizedToken)) {
    throw new Error('SUPER_DOLPHIN_WAILS_WS_TOKEN must be a non-empty cookie-safe value');
  }
  const originScheme = parsed.protocol === 'wss:' ? 'https:' : 'http:';
  return Object.freeze({
    headers: Object.freeze({
      Cookie: `${WAILS_WS_COOKIE_NAME}=${normalizedToken}`,
      Origin: `${originScheme}//${parsed.host}`,
    }),
  });
}

function withWSPath(url) {
  const parsed = new URL(url);
  if (parsed.pathname === '/' || parsed.pathname === '') parsed.pathname = '/wails/ws';
  return parsed.toString();
}

export function buildJSONRPCRequest(id, method, params = {}) {
  return { jsonrpc: '2.0', id, method, params };
}

export function smokeConfig(
  env = process.env,
  repoRoot = repoRootFromScript(),
  randomUUIDImpl = randomUUID,
  platform = process.platform,
) {
  const httpAddr = env.SUPER_DOLPHIN_HTTP_ADDR || DEFAULT_HTTP_ADDR;
  const viteURL = env.VITE_DEV_URL || DEFAULT_VITE_URL;
  return {
    repoRoot,
    cwd: env.SUPER_DOLPHIN_DESKTOP_SMOKE_CWD || repoRoot,
    httpAddr,
    viteURL,
    wsURL: env.SUPER_DOLPHIN_DESKTOP_SMOKE_WS_URL || buildWebSocketURL(httpAddr),
    wsToken: env.SUPER_DOLPHIN_WAILS_WS_TOKEN || randomUUIDImpl(),
    runner: env.SUPER_DOLPHIN_DESKTOP_SMOKE_RUNNER || path.join(repoRoot, 'run-new-ui-desktop.sh'),
    timeoutMs: positiveInt(env.SUPER_DOLPHIN_DESKTOP_SMOKE_TIMEOUT_MS, DEFAULT_TIMEOUT_MS),
    rpcTimeoutMs: positiveInt(env.SUPER_DOLPHIN_DESKTOP_SMOKE_RPC_TIMEOUT_MS, DEFAULT_RPC_TIMEOUT_MS),
    runTurnPath: truthyEnv(env.SUPER_DOLPHIN_DESKTOP_SMOKE_TURN),
    skipFrontendBuild: truthyEnv(env.SUPER_DOLPHIN_DESKTOP_SMOKE_SKIP_FRONTEND_BUILD),
    headlessDisplay: platform === 'linux' && truthyEnv(env.CI),
    provider: env.SUPER_DOLPHIN_DESKTOP_SMOKE_PROVIDER || 'codex',
  };
}

function positiveInt(value, fallback) {
  if (value == null || value === '') return fallback;
  const parsed = Number(value);
  if (!Number.isInteger(parsed) || parsed <= 0) {
    throw new Error(`expected positive integer, got ${value}`);
  }
  return parsed;
}

export function buildThreadStartParams(config) {
  return {
    provider: config.provider,
    cwd: config.cwd,
    name: `desktop smoke ${Date.now()}`,
    tool_surface_mode: 'chat',
    defer_spawn: true,
  };
}

export function buildFrontendFailureEvent() {
  const suffix = Date.now().toString(16);
  return {
    phase: 'frontend.rpc.failed',
    method: 'thread/start',
    trace_id: `desktop-smoke-${suffix}`,
    span_id: `desktop-smoke-span-${suffix}`,
    status: 'error',
    error: 'desktop smoke forced rejection',
    metadata: { component: 'desktop-smoke' },
  };
}

export async function runDesktopSmoke(config = smokeConfig(), deps = {}) {
  if (!config.skipFrontendBuild) {
    await runCommand('make', ['frontend-app-build'], config.repoRoot, deps.spawn || spawn);
  }
  const child = startDesktop(config, deps.spawn || spawn);
  try {
    await (deps.waitForHTTP || waitForHTTP)(`http://${config.httpAddr}/metrics`, config.timeoutMs, child);
    const client = await (deps.openWSRPC || openWSRPC)(
      config.wsURL,
      config.wsToken,
      config.rpcTimeoutMs,
      deps.WebSocket || WebSocket,
    );
    try {
      await runReadPathSmoke(client, config);
      const threadID = await runThreadStartSmoke(client, config);
      await runFrontendIngestSmoke(client);
      if (config.runTurnPath) await runTurnSmoke(client, config, threadID);
      console.log('desktop smoke passed');
    }
    finally {
      client.close();
    }
  }
  finally {
    await (deps.stopDesktop || stopDesktop)(child, deps);
  }
}

function startDesktop(config, spawnImpl) {
  const invocation = desktopRunnerInvocation(config);
  console.log(`starting desktop: ${invocation.command} ${invocation.args.join(' ')}`.trim());
  return spawnImpl(invocation.command, invocation.args, desktopSpawnOptions(config));
}

export function desktopSpawnOptions(config, platform = process.platform) {
  return {
    cwd: config.repoRoot,
    env: {
      ...process.env,
      SUPER_DOLPHIN_HTTP_ADDR: config.httpAddr,
      VITE_DEV_URL: config.viteURL,
      FRONTEND_DEVSERVER_URL: config.viteURL,
      SUPER_DOLPHIN_DESKTOP_SMOKE_ACTIVE: '1',
      SUPER_DOLPHIN_WAILS_WS_TOKEN: config.wsToken,
    },
    detached: platform !== 'win32',
    stdio: 'inherit',
  };
}

export function desktopRunnerInvocation(config) {
  if (config.headlessDisplay) {
    return Object.freeze({ command: 'xvfb-run', args: Object.freeze(['-a', config.runner]) });
  }
  return Object.freeze({ command: config.runner, args: Object.freeze([]) });
}

function runCommand(command, args, cwd, spawnImpl) {
  console.log(`running prerequisite: ${command} ${args.join(' ')}`);
  return new Promise((resolve, reject) => {
    const child = spawnImpl(command, args, { cwd, stdio: 'inherit' });
    child.once('error', reject);
    child.once('exit', (code, signal) => {
      if (code === 0) {
        resolve();
        return;
      }
      reject(new Error(`${command} ${args.join(' ')} failed: exit=${code} signal=${signal || ''}`));
    });
  });
}

export async function stopDesktop(child, deps = {}) {
  if (!child || child.exitCode != null || child.signalCode != null) return;
  await (deps.terminateProcessTree || terminateProcessTree)(child, {
    killGraceMs: deps.killGraceMs || 10_000,
  });
}

async function waitForHTTP(url, timeoutMs, child) {
  const deadline = Date.now() + timeoutMs;
  while (Date.now() < deadline) {
    if (child.exitCode != null || child.signalCode != null) {
      throw new Error(`desktop exited before readiness: exit=${child.exitCode} signal=${child.signalCode}`);
    }
    try {
      const response = await fetch(url);
      if (response.ok) {
        console.log(`backend ready: ${url}`);
        return;
      }
    }
    catch {
      // Keep polling until timeout; the child exit check above is the fail-fast path.
    }
    await sleep(500);
  }
  throw new Error(`timed out waiting for backend: ${url}`);
}

async function openWSRPC(wsURL, wsToken, timeoutMs, WebSocketImpl) {
  if (typeof WebSocketImpl !== 'function') {
    throw new Error('global WebSocket is required; run with Node.js 22 or newer');
  }
  const ws = new WebSocketImpl(wsURL, [], buildWebSocketOptions(wsURL, wsToken));
  const pending = new Map();
  let nextID = 1;
  await waitForWSOpen(ws, timeoutMs);
  ws.addEventListener('message', (event) => {
    const message = parseWSMessage(event.data);
    const entry = pending.get(String(message.id));
    if (!entry) return;
    pending.delete(String(message.id));
    if (message.error) {
      entry.reject(new Error(`${message.error.code}: ${message.error.message}`));
      return;
    }
    entry.resolve(message.result);
  });
  return {
    request(method, params = {}) {
      const id = nextID++;
      const payload = buildJSONRPCRequest(id, method, params);
      const result = new Promise((resolve, reject) => {
        pending.set(String(id), { resolve, reject });
        ws.send(JSON.stringify(payload));
      });
      return withTimeout(result, timeoutMs, `${method} timed out`);
    },
    close() {
      ws.close();
    },
  };
}

function waitForWSOpen(ws, timeoutMs) {
  return withTimeout(new Promise((resolve, reject) => {
    ws.addEventListener('open', resolve, { once: true });
    ws.addEventListener('error', () => reject(new Error('websocket connection failed')), { once: true });
  }), timeoutMs, 'websocket open timed out');
}

function parseWSMessage(data) {
  if (typeof data === 'string') return JSON.parse(data);
  if (data instanceof ArrayBuffer) return JSON.parse(Buffer.from(data).toString('utf8'));
  return JSON.parse(String(data));
}

async function withTimeout(promise, timeoutMs, message) {
  let timer;
  const timeout = new Promise((_, reject) => {
    timer = setTimeout(() => reject(new Error(message)), timeoutMs);
  });
  try {
    return await Promise.race([promise, timeout]);
  }
  finally {
    clearTimeout(timer);
  }
}

async function runReadPathSmoke(client, config) {
  await assertObject(client.request('ui/sidebar/get', { cwd: config.cwd }), 'ui/sidebar/get');
  await assertObject(client.request('ui/dashboard/get', { cwd: config.cwd, page: 'dags' }), 'ui/dashboard/get');
  await assertObject(client.request('observability/status', {}), 'observability/status');
}

async function runThreadStartSmoke(client, config) {
  const result = await assertObject(client.request('thread/start', buildThreadStartParams(config)), 'thread/start');
  const threadID = String(result.threadId || result.thread_id || result.thread?.id || '').trim();
  if (!threadID) throw new Error(`thread/start did not return a thread id: ${JSON.stringify(result)}`);
  return threadID;
}

async function runFrontendIngestSmoke(client) {
  const result = await assertObject(client.request('observability/frontend/ingest', { events: [buildFrontendFailureEvent()] }), 'observability/frontend/ingest');
  if (result.enabled !== false && Number(result.recorded || 0) < 1) {
    throw new Error(`frontend ingest did not record the smoke event: ${JSON.stringify(result)}`);
  }
}

async function runTurnSmoke(client, config, threadID) {
  const turn = await assertObject(client.request('turn/start', { thread_id: threadID, cwd: config.cwd, prompt: 'desktop smoke turn' }), 'turn/start');
  await assertObject(client.request('turn/interrupt', buildTurnInterruptParams(threadID, turn)), 'turn/interrupt');
}

export function buildTurnInterruptParams(threadID, turn, createRequestId = randomUUID) {
  const canonicalThreadID = typeof threadID === 'string' ? threadID.trim() : '';
  if (!canonicalThreadID) throw new Error('turn/interrupt requires thread_id');
  const expectedTurnID = typeof turn?.turn_id === 'string' ? turn.turn_id.trim() : '';
  if (!expectedTurnID) throw new Error('turn/start response requires turn_id before interrupt');
  const requestID = createRequestId();
  if (typeof requestID !== 'string' || !requestID.trim()) {
    throw new Error('turn/interrupt requires a generated request_id');
  }
  return {
    thread_id: canonicalThreadID,
    expected_turn_id: expectedTurnID,
    request_id: requestID.trim(),
    source: 'desktop_smoke',
  };
}

async function assertObject(promise, method) {
  const value = await promise;
  if (!value || typeof value !== 'object' || Array.isArray(value)) {
    throw new Error(`${method} returned non-object result: ${JSON.stringify(value)}`);
  }
  console.log(`rpc ok: ${method}`);
  return value;
}

export async function packageScriptIncludesSmoke(packageJSONPath = path.join(repoRootFromScript(), 'frontend-app', 'package.json')) {
  const pkg = JSON.parse(await readFile(packageJSONPath, 'utf8'));
  return pkg.scripts?.['smoke:desktop'] === 'npm run smoke:desktop:rpc'
    && pkg.scripts?.['smoke:desktop:rpc'] === 'node scripts/desktop-smoke.mjs';
}

export function isMain(metaURL, argv1) {
  return argv1 ? path.resolve(fileURLToPath(metaURL)) === path.resolve(argv1) : false;
}

if (isMain(import.meta.url, process.argv[1])) {
  runDesktopSmoke().catch((error) => {
    console.error(`desktop smoke failed: ${error.message}`);
    process.exitCode = 1;
  });
}
