import { spawn } from 'node:child_process';
import { randomBytes } from 'node:crypto';
import { existsSync } from 'node:fs';
import net from 'node:net';
import path from 'node:path';
import { setTimeout as sleep } from 'node:timers/promises';
import { fileURLToPath } from 'node:url';
import {
  createMCPFrameReader,
  encodeMCPFrame,
} from './ui-test-mcp-framing.mjs';
import {
  UI_TEST_LIMITS,
  UI_TEST_TOOLS,
} from '../src/devtools/uiTestContract.js';

const DEFAULT_PORT = 5177;
const DEFAULT_HOST = '127.0.0.1';
const DEFAULT_TIMEOUT_MS = 60000;
const DEFAULT_RPC_TIMEOUT_MS = 15000;
const DEFAULT_STOP_TIMEOUT_MS = 5000;
const INPUT_TEXT = 'MCP UI test input';
const REQUIRED_SNAPSHOT_KEYS = [
  'availableActions',
  'currentThreadId',
  'hasRunningTurn',
  'inputTextLength',
  'route',
  'visibleErrors',
];
const REQUIRED_DIAGNOSTIC_KEYS = [
  'bridgeErrors',
  'consoleErrors',
  'readyState',
  'unhandledErrors',
  'url',
  'warningEntries',
];
const FORBIDDEN_RUNTIME_PATTERNS = [
  /\bthread[/.]start\b/i,
  /\bturn[/.]start\b/i,
  /\bprovider[/.](session[/.])?(acquire|resume)\b/i,
  /\bsession[/.](acquire|resume)\b/i,
  /\bwails\b.*\b(thread[/.]start|turn[/.]start|provider|backend|callAPI)\b/i,
  /\bbackend\b.*\b(thread[/.]start|turn[/.]start|provider|callAPI)\b/i,
  /\bcallAPI\b/i,
];

export function repoRootFromScript(metaURL = import.meta.url) {
  return path.resolve(path.dirname(fileURLToPath(metaURL)), '..', '..');
}

export function acceptanceConfig(env = process.env, repoRoot = repoRootFromScript()) {
  const frontendRoot = path.join(repoRoot, 'frontend-app');
  const scriptBaseURL = env.SUPER_DOLPHIN_UI_TEST_BASE_URL;
  const port = positiveInt(env.SUPER_DOLPHIN_UI_TEST_ACCEPTANCE_PORT, DEFAULT_PORT);
  const timeoutMs = positiveInt(env.SUPER_DOLPHIN_UI_TEST_ACCEPTANCE_TIMEOUT_MS, DEFAULT_TIMEOUT_MS);
  const rpcTimeoutMs = positiveInt(env.SUPER_DOLPHIN_UI_TEST_RPC_TIMEOUT_MS, DEFAULT_RPC_TIMEOUT_MS);
  const startVite = !scriptBaseURL;
  const baseURL = normalizeLoopbackURL(scriptBaseURL || `http://${DEFAULT_HOST}:${port}/`);
  const ownsProvidedUI = env.SUPER_DOLPHIN_UI_TEST_ACCEPTANCE_OWNS_UI === '1';
  const ownsUI = startVite || ownsProvidedUI;
  const frameMode = env.SUPER_DOLPHIN_UI_TEST_MCP_FRAME_MODE || 'content-length';
  if (frameMode !== 'content-length' && frameMode !== 'ndjson') {
    throw new Error(`unsupported SUPER_DOLPHIN_UI_TEST_MCP_FRAME_MODE: ${frameMode}`);
  }
  return {
    repoRoot,
    frontendRoot,
    baseURL,
    startVite,
    ownsUI,
    port,
    timeoutMs,
    rpcTimeoutMs,
    frameMode,
    token: env.SUPER_DOLPHIN_UI_TEST_ACCEPTANCE_TOKEN || randomBytes(32).toString('hex'),
  };
}

export async function runUITestMCPAcceptance(config = acceptanceConfig()) {
  let viteProcess;
  let mcpClient;
  try {
    if (config.startVite) {
      await assertPortFree(DEFAULT_HOST, config.port);
      viteProcess = startVite(config);
      await waitForHTTP(config.baseURL, config.timeoutMs, viteProcess, 'Vite dev server');
    }

    mcpClient = startMCPServer(config);
    await runMCPAcceptanceFlow(mcpClient, config);
    console.log('UI test MCP acceptance passed');
  }
  finally {
    if (mcpClient && !mcpClient.stopped()) {
      await stopProcess(mcpClient.child, 'MCP server', DEFAULT_STOP_TIMEOUT_MS);
    }
    if (viteProcess) {
      await stopProcess(viteProcess, 'Vite dev server', DEFAULT_STOP_TIMEOUT_MS);
    }
  }
}

async function runMCPAcceptanceFlow(client, config) {
  const initialize = await client.request('initialize', {
    protocolVersion: '2024-11-05',
    capabilities: {},
    clientInfo: {
      name: 'super-dolphin-ui-test-mcp-acceptance',
      version: '0.1.0',
    },
  });
  assertPlainObject(initialize, 'initialize result');
  await client.notify('notifications/initialized', {});

  const listed = await client.request('tools/list', {});
  assertToolsListed(listed);

  const firstSnapshot = extractSnapshot(await callTool(client, 'ui_snapshot', {}));
  assertSnapshot(firstSnapshot, 'initial snapshot');

  const firstLogs = extractLogEntries(await callTool(client, 'ui_frontend_logs', { limit: 20 }), 'initial logs');
  assertNoForbiddenRuntimeCalls(firstLogs, 'initial frontend logs');

  await callTool(client, 'ui_action', {
    action: 'fill_composer',
    target: 'composer_input',
    text: INPUT_TEXT,
  });

  const secondSnapshot = extractSnapshot(await callTool(client, 'ui_snapshot', {}));
  assertSnapshot(secondSnapshot, 'snapshot after fill_composer');
  if (secondSnapshot.inputTextLength <= firstSnapshot.inputTextLength) {
    throw new Error(`fill_composer did not increase inputTextLength: before=${firstSnapshot.inputTextLength} after=${secondSnapshot.inputTextLength}`);
  }
  if (secondSnapshot.inputTextLength < INPUT_TEXT.length) {
    throw new Error(`fill_composer inputTextLength is too small: got ${secondSnapshot.inputTextLength}, want at least ${INPUT_TEXT.length}`);
  }

  if (config.ownsUI) {
    const submit = await callTool(client, 'ui_action', {
      action: 'submit_composer',
      target: 'composer_submit',
    });
    assertNoForbiddenRuntimeCalls([submit], 'submit_composer result');
  }
  else {
    await assertExternalSubmitRejected(client);
  }

  const diagnostics = extractDiagnostics(await callTool(client, 'ui_diagnostics', {}));
  assertDiagnosticsClean(diagnostics);
  assertNoForbiddenRuntimeCalls([diagnostics], 'diagnostics');

  const actionLogs = extractLogEntries(await callTool(client, 'ui_frontend_logs', {
    source: 'ui_test_mcp',
    limit: UI_TEST_LIMITS.maxLimit,
  }), 'action logs');
  assertActionLog(actionLogs, 'fill_composer');
  if (config.ownsUI) assertActionLog(actionLogs, 'submit_composer');
  assertNoForbiddenRuntimeCalls(actionLogs, 'ui_test_mcp frontend logs');

  await client.request('shutdown', {});
  await client.notify('exit', {});
  await waitForProcessExit(client.child, DEFAULT_STOP_TIMEOUT_MS, 'MCP server did not exit after shutdown/exit');
  await assertAfterStopFails(client);
}

function startVite(config) {
  const viteBin = path.join(config.frontendRoot, 'node_modules', 'vite', 'bin', 'vite.js');
  if (!existsSync(viteBin)) {
    throw new Error(`missing Vite binary: ${viteBin}; run npm install in frontend-app`);
  }
  console.log(`starting Vite dev server: ${config.baseURL}`);
  return spawn(process.execPath, [
    viteBin,
    '--host',
    DEFAULT_HOST,
    '--port',
    String(config.port),
    '--strictPort',
  ], {
    cwd: config.frontendRoot,
    env: {
      ...process.env,
      VITE_SUPER_DOLPHIN_UI_TEST_MCP: '1',
      SUPER_DOLPHIN_UI_TEST_ACCEPTANCE_TOKEN: config.token,
      SUPER_DOLPHIN_UI_TEST_ACCEPTANCE_OWNS_UI: '1',
    },
    stdio: ['ignore', 'pipe', 'pipe'],
  });
}

function startMCPServer(config) {
  const serverPath = path.join(config.frontendRoot, 'scripts', 'ui-test-mcp-server.mjs');
  if (!existsSync(serverPath)) {
    throw new Error(`missing MCP server: ${serverPath}`);
  }
  const env = {
    ...process.env,
    SUPER_DOLPHIN_UI_TEST_MCP: '1',
    SUPER_DOLPHIN_UI_TEST_BASE_URL: config.baseURL,
    SUPER_DOLPHIN_UI_TEST_ACCEPTANCE_TOKEN: config.token,
    SUPER_DOLPHIN_UI_TEST_ACCEPTANCE_OWNS_UI: config.ownsUI ? '1' : '0',
    SUPER_DOLPHIN_UI_TEST_ALLOW_SUBMIT: config.ownsUI ? '1' : '0',
  };

  console.log(`starting UI test MCP server for ${config.baseURL}`);
  const child = spawn(process.execPath, [serverPath], {
    cwd: config.frontendRoot,
    env,
    stdio: ['pipe', 'pipe', 'pipe'],
  });
  return createMCPClient(child, {
    mode: config.frameMode,
    timeoutMs: config.rpcTimeoutMs,
  });
}

function createMCPClient(child, { mode, timeoutMs }) {
  let nextID = 1;
  let stopped = false;
  const pending = new Map();
  const stderr = createTailBuffer('mcp stderr');
  const stdoutErrors = [];
  const reader = createMCPFrameReader({
    limits: UI_TEST_LIMITS,
    onMessage(message) {
      handleResponse(message);
    },
    onError(error) {
      const wrapped = error instanceof Error ? error : new Error(String(error));
      stdoutErrors.push(wrapped);
      rejectPending(wrapped);
    },
  });

  child.stdout.on('data', (chunk) => {
    try {
      feedFrameReader(reader, chunk);
    }
    catch (error) {
      stdoutErrors.push(error);
      rejectPending(error);
    }
  });
  child.stderr.on('data', (chunk) => stderr.push(chunk));
  child.once('error', (error) => {
    stopped = true;
    rejectPending(error);
  });
  child.once('exit', (code, signal) => {
    stopped = true;
    if (pending.size > 0) {
      rejectPending(new Error(`MCP server exited before response: exit=${code} signal=${signal || ''}\n${stderr.text()}`));
    }
  });

  function handleResponse(message) {
    if (!message || typeof message !== 'object' || Array.isArray(message)) {
      rejectPending(new Error(`MCP server returned non-object message: ${JSON.stringify(message)}`));
      return;
    }
    const key = String(message.id ?? '');
    const entry = pending.get(key);
    if (!entry) return;
    pending.delete(key);
    if (message.error) {
      entry.reject(new Error(`JSON-RPC ${message.error.code}: ${message.error.message}`));
      return;
    }
    entry.resolve(message.result);
  }

  function request(method, params = {}, options = {}) {
    if (stopped || child.exitCode != null || child.signalCode != null) {
      return Promise.reject(new Error('MCP server is not running'));
    }
    if (stdoutErrors.length > 0) return Promise.reject(stdoutErrors[0]);
    const id = nextID++;
    const payload = { jsonrpc: '2.0', id, method, params };
    const result = new Promise((resolve, reject) => {
      pending.set(String(id), { resolve, reject });
      writeFrame(child, payload, mode, (error) => {
        if (!error) return;
        pending.delete(String(id));
        reject(error);
      });
    });
    return withTimeout(result, options.timeoutMs || timeoutMs, `${method} timed out`);
  }

  async function notify(method, params = {}) {
    if (stopped || child.exitCode != null || child.signalCode != null) {
      throw new Error('MCP server is not running');
    }
    await new Promise((resolve, reject) => {
      writeFrame(child, { jsonrpc: '2.0', method, params }, mode, (error) => {
        if (error) reject(error);
        else resolve();
      });
    });
  }

  return {
    child,
    request,
    notify,
    stopped: () => stopped || child.exitCode != null || child.signalCode != null,
  };

  function rejectPending(error) {
    for (const entry of pending.values()) entry.reject(error);
    pending.clear();
  }
}

function writeFrame(child, payload, mode, callback) {
  let frame;
  try {
    frame = encodeMCPFrame(payload, mode);
  }
  catch (error) {
    callback(error);
    return;
  }
  child.stdin.write(frame, callback);
}

function feedFrameReader(reader, chunk) {
  if (typeof reader === 'function') {
    reader(chunk);
    return;
  }
  for (const method of ['push', 'write', 'feed', 'append']) {
    if (typeof reader?.[method] === 'function') {
      reader[method](chunk);
      return;
    }
  }
  throw new Error('createMCPFrameReader returned no push/write/feed/append method');
}

async function callTool(client, name, args) {
  const result = await callToolRaw(client, name, args);
  if (result.isError) {
    throw new Error(`${name} returned tool error: ${JSON.stringify(result.structuredContent || result.content || result)}`);
  }
  if (!Object.hasOwn(result, 'structuredContent')) {
    throw new Error(`${name} response is missing structuredContent`);
  }
  return result.structuredContent;
}

async function callToolRaw(client, name, args) {
  const result = await client.request('tools/call', { name, arguments: args });
  assertPlainObject(result, `${name} tool result`);
  if (typeof result.isError !== 'boolean') {
    throw new Error(`${name} tool result is missing boolean isError`);
  }
  return result;
}

async function assertExternalSubmitRejected(client) {
  const result = await callToolRaw(client, 'ui_action', {
    action: 'submit_composer',
    target: 'composer_submit',
  });
  if (!result.isError) {
    throw new Error('submit_composer unexpectedly succeeded against caller-provided UI without ownership opt-in');
  }
}

function assertToolsListed(result) {
  assertPlainObject(result, 'tools/list result');
  if (!Array.isArray(result.tools)) throw new Error('tools/list result must include tools array');
  const names = result.tools.map((tool) => {
    assertPlainObject(tool, 'tools/list tool entry');
    if (typeof tool.name !== 'string') throw new Error(`tool entry missing string name: ${JSON.stringify(tool)}`);
    return tool.name;
  });
  for (const expected of UI_TEST_TOOLS) {
    if (!names.includes(expected)) {
      throw new Error(`tools/list missing ${expected}; got ${names.join(', ')}`);
    }
  }
}

function extractSnapshot(structured) {
  const snapshot = structured?.snapshot || structured;
  assertPlainObject(snapshot, 'ui_snapshot structuredContent');
  return snapshot;
}

function assertSnapshot(snapshot, label) {
  assertExactKeys(snapshot, REQUIRED_SNAPSHOT_KEYS, label);
  if (typeof snapshot.route !== 'string') throw new Error(`${label}.route must be a string`);
  if (typeof snapshot.inputTextLength !== 'number') throw new Error(`${label}.inputTextLength must be a number`);
  if (typeof snapshot.hasRunningTurn !== 'boolean') throw new Error(`${label}.hasRunningTurn must be a boolean`);
  if (!Array.isArray(snapshot.visibleErrors)) throw new Error(`${label}.visibleErrors must be an array`);
  if (!Array.isArray(snapshot.availableActions)) throw new Error(`${label}.availableActions must be an array`);
}

function extractDiagnostics(structured) {
  const diagnostics = structured?.diagnostics || structured;
  assertPlainObject(diagnostics, 'ui_diagnostics structuredContent');
  return diagnostics;
}

function assertDiagnosticsClean(diagnostics) {
  assertExactKeys(diagnostics, REQUIRED_DIAGNOSTIC_KEYS, 'ui_diagnostics');
  for (const key of ['consoleErrors', 'bridgeErrors', 'unhandledErrors']) {
    if (!Array.isArray(diagnostics[key])) throw new Error(`ui_diagnostics.${key} must be an array`);
    if (diagnostics[key].length > 0) {
      throw new Error(`ui_diagnostics.${key} is not empty: ${JSON.stringify(diagnostics[key])}`);
    }
  }
  if (!Array.isArray(diagnostics.warningEntries)) {
    throw new Error('ui_diagnostics.warningEntries must be an array');
  }
  assertWarningsAllowed(diagnostics.warningEntries);
}

function assertWarningsAllowed(warnings) {
  if (warnings.length === 0) return;
  const rawPattern = process.env.SUPER_DOLPHIN_UI_TEST_EXPECTED_WARNING_RE;
  if (!rawPattern) {
    throw new Error(`ui_diagnostics.warningEntries is not empty and no SUPER_DOLPHIN_UI_TEST_EXPECTED_WARNING_RE was provided: ${JSON.stringify(warnings)}`);
  }
  const pattern = new RegExp(rawPattern);
  const unexpected = warnings.filter((entry) => !pattern.test(JSON.stringify(entry)));
  if (unexpected.length > 0) {
    throw new Error(`ui_diagnostics.warningEntries contained unexpected warnings: ${JSON.stringify(unexpected)}`);
  }
}

function extractLogEntries(structured, label) {
  let entries = structured;
  if (structured && typeof structured === 'object' && !Array.isArray(structured)) {
    entries = structured.logs || structured.entries || structured.logEntries;
  }
  if (!Array.isArray(entries)) {
    throw new Error(`${label} response must include a log array`);
  }
  for (const entry of entries) assertPlainObject(entry, `${label} entry`);
  return entries;
}

function assertActionLog(entries, action) {
  const found = entries.some((entry) => {
    const source = String(entry.source || '');
    const message = String(entry.message || entry.event || '');
    const serialized = JSON.stringify(entry);
    return source === 'ui_test_mcp' && (message.includes(action) || serialized.includes(action));
  });
  if (!found) {
    throw new Error(`missing ui_test_mcp ${action} action log: ${JSON.stringify(entries)}`);
  }
}

function assertNoForbiddenRuntimeCalls(values, label) {
  const serialized = JSON.stringify(values);
  const matched = FORBIDDEN_RUNTIME_PATTERNS.find((pattern) => pattern.test(serialized));
  if (matched) {
    throw new Error(`${label} observed forbidden product runtime call pattern ${matched}: ${serialized}`);
  }
}

function assertPlainObject(value, label) {
  if (!value || typeof value !== 'object' || Array.isArray(value)) {
    throw new Error(`${label} must be a plain object: ${JSON.stringify(value)}`);
  }
}

function assertExactKeys(value, expectedKeys, label) {
  const actual = Object.keys(value).sort();
  const expected = [...expectedKeys].sort();
  if (actual.join('\0') !== expected.join('\0')) {
    throw new Error(`${label} keys mismatch: got ${actual.join(', ')}, want ${expected.join(', ')}`);
  }
}

function normalizeLoopbackURL(value) {
  const parsed = new URL(value);
  if (parsed.protocol !== 'http:') throw new Error(`only http:// loopback URLs are allowed: ${value}`);
  if (parsed.username || parsed.password) throw new Error(`credentials are not allowed in SUPER_DOLPHIN_UI_TEST_BASE_URL: ${value}`);
  const hostname = parsed.hostname.replace(/^\[(.*)\]$/, '$1');
  if (!['127.0.0.1', 'localhost', '::1'].includes(hostname)) {
    throw new Error(`only loopback UI URLs are allowed, got host ${parsed.hostname}`);
  }
  return parsed.toString();
}

function positiveInt(value, fallback) {
  if (value == null || value === '') return fallback;
  const parsed = Number(value);
  if (!Number.isInteger(parsed) || parsed <= 0) throw new Error(`expected positive integer, got ${value}`);
  return parsed;
}

async function assertPortFree(host, port) {
  await new Promise((resolve, reject) => {
    const server = net.createServer();
    server.once('error', (error) => reject(new Error(`port ${port} is already in use on ${host}: ${error.message}`)));
    server.listen(port, host, () => {
      server.close(resolve);
    });
  });
}

async function waitForHTTP(url, timeoutMs, child, label) {
  const deadline = Date.now() + timeoutMs;
  const tail = attachProcessLogTail(child, label);
  while (Date.now() < deadline) {
    if (child.exitCode != null || child.signalCode != null) {
      throw new Error(`${label} exited before readiness: exit=${child.exitCode} signal=${child.signalCode || ''}\n${tail.text()}`);
    }
    try {
      const response = await fetch(url);
      if (response.ok) return;
    }
    catch {
      // Vite is still starting; process exit above is the fail-fast path.
    }
    await sleep(250);
  }
  throw new Error(`timed out waiting for ${label}: ${url}\n${tail.text()}`);
}

function attachProcessLogTail(child, label) {
  const tail = createTailBuffer(label);
  child.stdout?.on('data', (chunk) => tail.push(chunk));
  child.stderr?.on('data', (chunk) => tail.push(chunk));
  return tail;
}

function createTailBuffer(label, maxBytes = 16384) {
  let value = '';
  return {
    push(chunk) {
      value += Buffer.from(chunk).toString('utf8');
      if (value.length > maxBytes) value = value.slice(value.length - maxBytes);
    },
    text() {
      return value ? `${label}:\n${value}` : `${label}: <empty>`;
    },
  };
}

async function withTimeout(promise, timeoutMs, message) {
  let timer;
  const timeout = new Promise((_, reject) => {
    timer = setTimeout(() => reject(new Error(message)), timeoutMs);
  });
  return Promise.race([promise, timeout]).finally(() => clearTimeout(timer));
}

async function stopProcess(child, label, timeoutMs) {
  if (!child || child.exitCode != null || child.signalCode != null) return;
  child.kill('SIGTERM');
  try {
    await waitForProcessExit(child, timeoutMs, `${label} did not exit after SIGTERM`);
  }
  catch {
    if (child.exitCode == null && child.signalCode == null) child.kill('SIGKILL');
    await waitForProcessExit(child, timeoutMs, `${label} did not exit after SIGKILL`);
  }
}

function waitForProcessExit(child, timeoutMs, message) {
  if (child.exitCode != null || child.signalCode != null) return Promise.resolve();
  return withTimeout(new Promise((resolve) => child.once('exit', resolve)), timeoutMs, message);
}

async function assertAfterStopFails(client) {
  let failed = false;
  try {
    await client.request('ping', {}, { timeoutMs: 1000 });
  }
  catch {
    failed = true;
  }
  if (!failed) throw new Error('MCP request unexpectedly succeeded after server stop');
}

export function isMain(metaURL, argv1) {
  return argv1 ? path.resolve(fileURLToPath(metaURL)) === path.resolve(argv1) : false;
}

if (isMain(import.meta.url, process.argv[1])) {
  runUITestMCPAcceptance().catch((error) => {
    console.error(`UI test MCP acceptance failed: ${error.message}`);
    process.exitCode = 1;
  });
}
