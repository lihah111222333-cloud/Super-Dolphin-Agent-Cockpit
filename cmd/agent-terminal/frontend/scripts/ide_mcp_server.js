#!/usr/bin/env node
// @ts-nocheck

import { randomUUID } from 'node:crypto';
import { promises as fs } from 'node:fs';
import path from 'node:path';
import process from 'node:process';

import { chromium } from 'playwright';
import { Server } from '@modelcontextprotocol/sdk/server/index.js';
import { StdioServerTransport } from '@modelcontextprotocol/sdk/server/stdio.js';
import { CallToolRequestSchema, ListToolsRequestSchema } from '@modelcontextprotocol/sdk/types.js';

const IDE_ACTIONS = Object.freeze(['open_at', 'search_goto', 'inspect', 'scroll', 'click', 'find_refs', 'run', 'test', 'build']);
const DEFAULT_URL = 'http://localhost:4173';
const DEFAULT_TMPDIR = '/tmp/ide-screenshots';
const SCREENSHOT_TTL_MS = 5 * 60 * 1000;
const VIEWPORT_WIDTH = 768;
const VIEWPORT_HEIGHT = 600;
const MAX_VIEWPORT_HEIGHT = 1200;  // ~2 tiles @ 768px = ~3200 tokens max
const STEP_TIMEOUT_MS = 15000;
const TOOL_NAME = 'ide';

const TOOL_INPUT_SCHEMA = {
  type: 'object',
  properties: {
    action: {
      type: 'string',
      enum: IDE_ACTIONS,
      description: 'Single browse-only action. Prefer steps[] for multi-step calls.',
    },
    steps: {
      type: 'array',
      description: 'One or more browse-only IDE actions executed in order.',
      items: {
        type: 'object',
        properties: {
          action: { type: 'string', enum: IDE_ACTIONS },
          target: { type: 'string' },
          path: { type: 'string' },
          file_path: { type: 'string' },
          query: { type: 'string' },
          command: { type: 'string' },
          cwd: { type: 'string' },
          symbol: { type: 'string' },
          line: { type: 'integer' },
          column: { type: 'integer' },
          selector: { type: 'string' },
          test_id: { type: 'string' },
          text: { type: 'string' },
          index: { type: 'integer' },
          direction: { type: 'string', enum: ['up', 'down'] },
          amount: { type: 'integer' },
          delta_y: { type: 'integer' },
          wait_ms: { type: 'integer', minimum: 0 },
        },
        required: ['action'],
        additionalProperties: true,
      },
      minItems: 1,
    },
    screenshot: {
      type: 'boolean',
      description: 'Capture a full-page PNG after the final IDE state. Defaults to true.',
    },
    wait_ms: {
      type: 'integer',
      minimum: 0,
      description: 'Optional extra wait before the final screenshot.',
    },
    __tool_call_meta: {
      type: 'object',
      description: 'Optional passthrough metadata for log correlation.',
      properties: {
        agent_id: { type: 'string' },
        thread_id: { type: 'string' },
      },
      additionalProperties: true,
    },
  },
  additionalProperties: true,
};

const TOOL_SPEC = {
  name: TOOL_NAME,
  description: 'Browse-only Visual IDE controller via Playwright. Executes IDE actions and returns a screenshot file path.',
  inputSchema: TOOL_INPUT_SCHEMA,
};

function parseCliArgs(argv) {
  const parsed = {
    url: DEFAULT_URL,
    debug: false,
    tmpdir: DEFAULT_TMPDIR,
  };

  for (let i = 0; i < argv.length; i += 1) {
    const token = argv[i];
    if (token === '--debug') {
      parsed.debug = true;
      continue;
    }
    if (token === '--url') {
      parsed.url = argv[i + 1] || DEFAULT_URL;
      i += 1;
      continue;
    }
    if (token === '--tmpdir') {
      parsed.tmpdir = argv[i + 1] || DEFAULT_TMPDIR;
      i += 1;
      continue;
    }
  }

  return parsed;
}

const cli = parseCliArgs(process.argv.slice(2));

function normalizeMeta(meta) {
  if (!meta || typeof meta !== 'object' || Array.isArray(meta)) {
    return { agent_id: '', thread_id: '' };
  }
  return {
    agent_id: stringOrEmpty(meta.agent_id),
    thread_id: stringOrEmpty(meta.thread_id),
  };
}

function stringOrEmpty(value) {
  return typeof value === 'string' ? value.trim() : '';
}

function serializeError(error) {
  if (!error) return { message: 'unknown error' };
  return {
    name: error.name || 'Error',
    message: error.message || String(error),
    ...(cli.debug && error.stack ? { stack: error.stack } : {}),
  };
}

function writeStderrLog(level, event, fields = {}, meta = {}) {
  const payload = {
    ts: new Date().toISOString(),
    level,
    event,
    agent_id: stringOrEmpty(meta.agent_id),
    thread_id: stringOrEmpty(meta.thread_id),
    ...fields,
  };
  process.stderr.write(`${JSON.stringify(payload)}\n`);
}

function clamp(value, min, max) {
  return Math.min(Math.max(value, min), max);
}

function toPositiveInteger(value, fallback = 0) {
  const num = Number(value);
  if (!Number.isFinite(num)) return fallback;
  return Math.max(0, Math.trunc(num));
}

function ensureSteps(args) {
  if (Array.isArray(args.steps) && args.steps.length > 0) {
    return args.steps;
  }
  if (typeof args.action === 'string' && args.action.trim()) {
    return [args];
  }
  throw new Error('ide requires steps[] or a top-level action');
}

function normalizeToolArgs(rawArgs) {
  const args = rawArgs && typeof rawArgs === 'object' && !Array.isArray(rawArgs) ? rawArgs : {};
  const steps = ensureSteps(args).map((step, index) => {
    const action = stringOrEmpty(step?.action);
    if (!IDE_ACTIONS.includes(action)) {
      throw new Error(`unsupported ide action at steps[${index}]: ${action || '<empty>'}`);
    }
    return {
      ...step,
      action,
      wait_ms: toPositiveInteger(step?.wait_ms, 0),
    };
  });

  return {
    ...args,
    steps,
    screenshot: args.screenshot !== false,
    wait_ms: toPositiveInteger(args.wait_ms, 0),
    __tool_call_meta: normalizeMeta(args.__tool_call_meta),
  };
}

class IdeBrowserSession {
  constructor(options) {
    this.options = options;
    this.browser = null;
    this.context = null;
    this.page = null;
    this.launching = null;
  }

  async ensureReady(meta) {
    if (this.page && !this.page.isClosed()) {
      await this.ensureIdePage(meta);
      return this.page;
    }
    if (!this.launching) {
      this.launching = this.launch(meta).finally(() => {
        this.launching = null;
      });
    }
    await this.launching;
    await this.ensureIdePage(meta);
    return this.page;
  }

  async launch(meta) {
    writeStderrLog('info', 'browser.launch.start', {
      headless: !this.options.debug,
      url: this.options.url,
    }, meta);

    this.browser = await chromium.launch({
      headless: !this.options.debug,
    });
    this.context = await this.browser.newContext({
      viewport: {
        width: VIEWPORT_WIDTH,
        height: VIEWPORT_HEIGHT,
      },
      deviceScaleFactor: 1,
    });
    this.page = await this.context.newPage();

    this.page.on('pageerror', (error) => {
      writeStderrLog('error', 'page.error', { error: serializeError(error) }, meta);
    });
    this.page.on('console', (message) => {
      if (!this.options.debug) return;
      writeStderrLog('debug', 'page.console', {
        type: message.type(),
        text: message.text(),
      }, meta);
    });

    writeStderrLog('info', 'browser.launch.ready', {}, meta);
  }

  async ensureIdePage(meta) {
    const ideVisible = await this.page.getByTestId('lsp-ide-page').isVisible().catch(() => false);
    if (ideVisible) {
      return;
    }

    writeStderrLog('info', 'page.goto.start', { url: this.options.url }, meta);
    await this.page.goto(this.options.url, { waitUntil: 'domcontentloaded', timeout: STEP_TIMEOUT_MS });
    await this.page.getByTestId('sidebar-nav').waitFor({ state: 'visible', timeout: STEP_TIMEOUT_MS });
    await this.page.getByTestId('nav-ide').click();
    await this.page.getByTestId('lsp-ide-page').waitFor({ state: 'visible', timeout: STEP_TIMEOUT_MS });
    await this.page.waitForTimeout(150);
    writeStderrLog('info', 'page.goto.ready', {}, meta);
  }

  async close(meta = {}) {
    const page = this.page;
    const context = this.context;
    const browser = this.browser;

    this.page = null;
    this.context = null;
    this.browser = null;

    await Promise.allSettled([
      page?.close?.(),
      context?.close?.(),
      browser?.close?.(),
    ]);

    writeStderrLog('info', 'browser.closed', {}, meta);
  }
}

const session = new IdeBrowserSession({
  url: cli.url,
  debug: cli.debug,
  tmpdir: cli.tmpdir,
});

async function cleanupOldScreenshots(tmpdir, meta) {
  await fs.mkdir(tmpdir, { recursive: true });
  const entries = await fs.readdir(tmpdir, { withFileTypes: true });
  const now = Date.now();
  let removed = 0;

  for (const entry of entries) {
    if (!entry.isFile() || !entry.name.endsWith('.png')) continue;
    const fullPath = path.join(tmpdir, entry.name);
    const stat = await fs.stat(fullPath).catch(() => null);
    if (!stat) continue;
    if (now - stat.mtimeMs <= SCREENSHOT_TTL_MS) continue;
    await fs.unlink(fullPath).catch(() => { });
    removed += 1;
  }

  if (removed > 0) {
    writeStderrLog('info', 'screenshot.cleanup', { removed, tmpdir }, meta);
  }
}

async function waitForIdeToSettle(page, extraWaitMs = 0) {
  await page.getByTestId('lsp-status-bar').waitFor({ state: 'visible', timeout: STEP_TIMEOUT_MS });
  await page.waitForTimeout(150);
  await page.getByTestId('lsp-code-loading').waitFor({ state: 'hidden', timeout: 1200 }).catch(() => { });
  if (extraWaitMs > 0) {
    await page.waitForTimeout(extraWaitMs);
  }
}

async function resolveLocator(page, step) {
  if (stringOrEmpty(step.test_id)) {
    return page.getByTestId(step.test_id);
  }
  if (stringOrEmpty(step.selector)) {
    return page.locator(step.selector);
  }
  if (Number.isInteger(step.line) && step.line > 0) {
    return page.getByTestId(`lsp-line-${step.line}`);
  }
  if (Number.isInteger(step.index) && step.index >= 0) {
    if (step.action === 'search_goto') {
      return page.getByTestId(`lsp-result-${step.index}`);
    }
    return page.getByTestId(`lsp-result-${step.index}`);
  }
  if (stringOrEmpty(step.text)) {
    return page.getByText(step.text, { exact: step.exact === true });
  }
  return null;
}

async function clickLocator(locator, step) {
  const target = locator.first();
  await target.waitFor({ state: 'visible', timeout: STEP_TIMEOUT_MS });
  await target.scrollIntoViewIfNeeded().catch(() => { });
  await target.click({ button: step.button || 'left', timeout: STEP_TIMEOUT_MS });
}

async function executeOpenAt(page, step, meta) {
  const target = stringOrEmpty(step.target) || stringOrEmpty(step.file_path) || stringOrEmpty(step.path);
  if (!target) {
    throw new Error('open_at requires target/file_path/path');
  }

  writeStderrLog('info', 'step.open_at', { target, line: step.line ?? null, symbol: step.symbol || '' }, meta);
  await page.getByTestId('lsp-file-path-input').fill(target);
  await page.getByTestId('lsp-open-btn').click();
  await waitForIdeToSettle(page, step.wait_ms);

  if (Number.isInteger(step.line) && step.line > 0) {
    const lineLocator = page.getByTestId(`lsp-line-${step.line}`);
    await lineLocator.waitFor({ state: 'visible', timeout: STEP_TIMEOUT_MS });
    await lineLocator.scrollIntoViewIfNeeded().catch(() => { });
    await lineLocator.click();
  }

  if (stringOrEmpty(step.symbol)) {
    const symbolFilter = page.getByTestId('lsp-symbol-filter');
    await symbolFilter.fill(step.symbol);
    const symbolLocator = page.locator('[data-testid^="lsp-symbol-"]').filter({ hasText: step.symbol }).first();
    await symbolLocator.waitFor({ state: 'visible', timeout: STEP_TIMEOUT_MS });
    await symbolLocator.click();
    await page.getByTestId('lsp-inspector-body').waitFor({ state: 'visible', timeout: STEP_TIMEOUT_MS });
  }

  return `open_at ${target}`;
}

async function executeSearchGoto(page, step, meta) {
  const query = stringOrEmpty(step.query) || stringOrEmpty(step.text) || stringOrEmpty(step.target);
  if (!query) {
    throw new Error('search_goto requires query');
  }

  writeStderrLog('info', 'step.search_goto', { query, index: step.index ?? 0 }, meta);
  await page.getByTestId('lsp-search-input').fill(query);
  await page.getByTestId('lsp-search-btn').click();
  await waitForIdeToSettle(page, step.wait_ms);

  const resultLocator = Number.isInteger(step.index) && step.index >= 0
    ? page.getByTestId(`lsp-result-${step.index}`)
    : page.locator('[data-testid^="lsp-result-"]').first();

  const resultCount = await page.locator('[data-testid^="lsp-result-"]').count();
  if (resultCount === 0) {
    throw new Error(`search_goto returned no result for query: ${query}`);
  }

  await resultLocator.waitFor({ state: 'visible', timeout: STEP_TIMEOUT_MS });
  await resultLocator.click();
  await waitForIdeToSettle(page, step.wait_ms);
  return `search_goto ${query}`;
}

async function executeInspect(page, step, meta) {
  writeStderrLog('info', 'step.inspect', {
    symbol: step.symbol || '',
    line: step.line ?? null,
    test_id: step.test_id || '',
    selector: step.selector || '',
  }, meta);

  if (stringOrEmpty(step.symbol)) {
    const symbolFilter = page.getByTestId('lsp-symbol-filter');
    await symbolFilter.fill(step.symbol);
    const symbolLocator = page.locator('[data-testid^="lsp-symbol-"]').filter({ hasText: step.symbol }).first();
    await symbolLocator.waitFor({ state: 'visible', timeout: STEP_TIMEOUT_MS });
    await symbolLocator.click();
  } else if (Number.isInteger(step.line) && step.line > 0) {
    const lineLocator = page.getByTestId(`lsp-line-${step.line}`);
    await lineLocator.waitFor({ state: 'visible', timeout: STEP_TIMEOUT_MS });
    await lineLocator.click();
  } else {
    const locator = await resolveLocator(page, step);
    if (!locator) {
      throw new Error('inspect requires symbol, line, selector, test_id, or text');
    }
    await clickLocator(locator, step);
  }

  await waitForIdeToSettle(page, step.wait_ms);
  await page.getByTestId('lsp-inspector-body').waitFor({ state: 'visible', timeout: STEP_TIMEOUT_MS });
  return `inspect ${step.symbol || step.test_id || step.selector || step.text || step.line || ''}`.trim();
}

async function executeScroll(page, step, meta) {
  const delta = Number.isFinite(Number(step.delta_y))
    ? Number(step.delta_y)
    : (() => {
      const amount = toPositiveInteger(step.amount, 240);
      return step.direction === 'up' ? amount * -1 : amount;
    })();

  writeStderrLog('info', 'step.scroll', { line: step.line ?? null, delta_y: delta }, meta);

  if (Number.isInteger(step.line) && step.line > 0) {
    const lineLocator = page.getByTestId(`lsp-line-${step.line}`);
    await lineLocator.waitFor({ state: 'visible', timeout: STEP_TIMEOUT_MS }).catch(() => { });
    await lineLocator.scrollIntoViewIfNeeded().catch(() => { });
  } else {
    await page.getByTestId('lsp-code-viewer').evaluate((node, value) => {
      node.scrollBy({ top: value, left: 0, behavior: 'instant' });
    }, delta);
  }

  await waitForIdeToSettle(page, step.wait_ms);
  return Number.isInteger(step.line) && step.line > 0 ? `scroll line ${step.line}` : `scroll ${delta}`;
}

async function executeClick(page, step, meta) {
  writeStderrLog('info', 'step.click', {
    test_id: step.test_id || '',
    selector: step.selector || '',
    text: step.text || '',
    line: step.line ?? null,
    index: step.index ?? null,
  }, meta);

  const locator = await resolveLocator(page, step);
  if (!locator) {
    throw new Error('click requires selector, test_id, text, line, or index');
  }
  await clickLocator(locator, step);
  await waitForIdeToSettle(page, step.wait_ms);
  return `click ${step.test_id || step.selector || step.text || step.line || step.index}`;
}

async function executeFindRefs(page, step, meta) {
  writeStderrLog('info', 'step.find_refs', {
    file_path: step.file_path || step.path || step.target || '',
    line: step.line ?? null,
    column: step.column ?? null,
    symbol: step.symbol || '',
  }, meta);

  await page.evaluate(async (payload) => {
    const bridge = globalThis.__AO_VISUAL_IDE__;
    if (!bridge?.findReferences) {
      throw new Error('visual ide findReferences bridge unavailable');
    }
    return bridge.findReferences(payload);
  }, {
    file_path: stringOrEmpty(step.file_path) || stringOrEmpty(step.path) || stringOrEmpty(step.target),
    line: Number.isInteger(step.line) && step.line > 0 ? step.line - 1 : undefined,
    column: Number.isInteger(step.column) && step.column > 0 ? step.column - 1 : undefined,
    symbol: stringOrEmpty(step.symbol),
  });

  await waitForIdeToSettle(page, step.wait_ms);
  return `find_refs ${step.file_path || step.path || step.target || step.symbol || step.line || ''}`.trim();
}

async function executeTerminalBridgeAction(page, step, meta, action) {
  writeStderrLog('info', `step.${action}`, {
    command: step.command || '',
    cwd: step.cwd || '',
  }, meta);

  await page.evaluate(async (payload) => {
    const bridge = globalThis.__AO_VISUAL_IDE__;
    if (bridge?.runAction) {
      return bridge.runAction(payload);
    }
    if (bridge?.setTerminalOutput) {
      return bridge.setTerminalOutput({
        command: payload.command || payload.action || '',
        stdout: '',
        stderr: 'P2 placeholder: awaiting bridge support',
        exit_code: null,
        duration: 0,
        status: 'placeholder',
        warning: 'bridge_pending',
      });
    }
    throw new Error('visual ide terminal bridge unavailable');
  }, {
    action,
    command: stringOrEmpty(step.command) || action,
    cwd: stringOrEmpty(step.cwd),
    warning: stringOrEmpty(step.warning),
  });

  await waitForIdeToSettle(page, step.wait_ms);
  return `${action} ${step.command || ''}`.trim();
}

async function executeStep(page, step, meta) {
  switch (step.action) {
    case 'open_at':
      return executeOpenAt(page, step, meta);
    case 'search_goto':
      return executeSearchGoto(page, step, meta);
    case 'inspect':
      return executeInspect(page, step, meta);
    case 'scroll':
      return executeScroll(page, step, meta);
    case 'click':
      return executeClick(page, step, meta);
    case 'find_refs':
      return executeFindRefs(page, step, meta);
    case 'run':
      return executeTerminalBridgeAction(page, step, meta, 'run');
    case 'test':
      return executeTerminalBridgeAction(page, step, meta, 'test');
    case 'build':
      return executeTerminalBridgeAction(page, step, meta, 'build');
    default:
      throw new Error(`unsupported ide action: ${step.action}`);
  }
}


async function collectPageState(page) {
  if (!page || page.isClosed()) {
    return {
      file_path: '',
      status: '',
      cursor: '',
      inspector: '',
      terminal: '',
      exit_code: null,
    };
  }

  const bridgeState = await page.evaluate(() => {
    try {
      return globalThis.__AO_VISUAL_IDE__?.getState?.() || null;
    } catch {
      return null;
    }
  });

  if (bridgeState && typeof bridgeState === 'object') {
    return bridgeState;
  }

  return page.evaluate(() => ({
    file_path: (document.querySelector('[data-testid="lsp-file-path-input"]')?.value || '').toString().trim(),
    status: (document.querySelector('[data-testid="lsp-status-text"]')?.textContent || '').toString().trim(),
    cursor: (document.querySelector('[data-testid="lsp-cursor-info"]')?.textContent || '').toString().trim(),
    inspector: (document.querySelector('[data-testid="lsp-inspector-label"]')?.textContent || '').toString().trim(),
    terminal: '',
    exit_code: null,
  }));
}


function buildSummary(pageState, notes, fallbackText = '') {
  return [
    pageState.file_path,
    pageState.cursor,
    pageState.inspector || pageState.status,
    notes[notes.length - 1] || '',
    fallbackText,
  ].filter(Boolean).join(' — ') || 'ide call completed';
}

async function captureScreenshot(page, meta, summaryText) {
  if (!page || page.isClosed()) {
    return {
      screenshot_path: '',
      summary: summaryText || 'screenshot unavailable',
      warning: 'screenshot_failed',
    };
  }

  await cleanupOldScreenshots(cli.tmpdir, meta);

  try {
    // AI mode: hide sidebar nav and scrollbars, keep only code + results
    await page.evaluate(() => {
      const idePage = document.querySelector('.lsp-ide-page');
      if (idePage) idePage.classList.add('lsp-ide-ai-mode');
      // Hide app chrome that AI doesn't need
      document.querySelectorAll('[data-testid="sidebar-nav"], .app-header, .sidebar').forEach(el => {
        el.style.setProperty('display', 'none', 'important');
      });
    });

    // Screenshot only the IDE page element, not the full app
    const ideElement = page.getByTestId('lsp-ide-page');
    const isVisible = await ideElement.isVisible().catch(() => false);

    // Fixed viewport screenshot — cap at MAX_VIEWPORT_HEIGHT to limit token cost
    // 768×1200 ≈ 2 tiles ≈ 3200 tokens; never exceed this regardless of content
    await page.setViewportSize({ width: VIEWPORT_WIDTH, height: VIEWPORT_HEIGHT });
    await page.waitForTimeout(80);
    const png = await page.screenshot({ type: 'png', fullPage: false });
    const filePath = path.join(cli.tmpdir, `ide-${randomUUID()}.png`);
    await fs.writeFile(filePath, png);

    writeStderrLog('info', 'screenshot.saved', {
      screenshot_path: filePath,
      bytes: png.byteLength,
      viewport_height: viewportHeight,
    }, meta);

    return {
      screenshot_path: filePath,
      summary: summaryText,
      bytes: png.byteLength,
    };
  } catch (error) {
    writeStderrLog('warn', 'screenshot.failed', { error: serializeError(error) }, meta);
    return {
      screenshot_path: '',
      summary: summaryText || 'screenshot unavailable',
      warning: 'screenshot_failed',
    };
  } finally {
    // Restore normal UI for human interaction
    await page.evaluate(() => {
      document.querySelector('.lsp-ide-page')?.classList.remove('lsp-ide-ai-mode');
      document.querySelectorAll('[data-testid="sidebar-nav"], .app-header, .sidebar').forEach(el => {
        el.style.removeProperty('display');
      });
    }).catch(() => { });
  }
}

async function executeIdeTool(args) {
  const meta = normalizeMeta(args.__tool_call_meta);
  const page = await session.ensureReady(meta);
  const notes = [];

  for (const step of args.steps) {
    const note = await executeStep(page, step, meta);
    notes.push(note);
  }

  if (args.wait_ms > 0) {
    await page.waitForTimeout(args.wait_ms);
  }

  const pageState = await collectPageState(page);
  const summary = buildSummary(pageState, notes);
  const basePayload = args.screenshot
    ? await captureScreenshot(page, meta, summary)
    : { screenshot_path: '', summary, bytes: 0 };

  return {
    ...basePayload,
    terminal: (pageState.terminal || '').toString(),
    exit_code: pageState.exit_code ?? null,
  };
}


function toToolResult(payload, isError = false) {
  return {
    content: [
      {
        type: 'text',
        text: JSON.stringify(payload, null, 2),
      },
    ],
    structuredContent: payload,
    isError,
  };
}

const server = new Server(
  {
    name: 'visual-ide-mcp-server',
    version: '0.1.0',
  },
  {
    capabilities: {
      tools: {},
    },
  },
);

server.setRequestHandler(ListToolsRequestSchema, async () => ({
  tools: [TOOL_SPEC],
}));

server.setRequestHandler(CallToolRequestSchema, async (request) => {
  const toolName = stringOrEmpty(request?.params?.name);
  const rawArgs = request?.params?.arguments || {};

  if (toolName !== TOOL_NAME) {
    const payload = { error: `unknown tool: ${toolName || '<empty>'}` };
    return toToolResult(payload, true);
  }

  let args;
  let meta = { agent_id: '', thread_id: '' };

  try {
    args = normalizeToolArgs(rawArgs);
    meta = normalizeMeta(args.__tool_call_meta);
  } catch (error) {
    const payload = {
      screenshot_path: '',
      summary: 'invalid ide request',
      error: serializeError(error).message,
    };
    writeStderrLog('warn', 'tool.call.invalid', { error: serializeError(error) }, meta);
    return toToolResult(payload, true);
  }

  writeStderrLog('info', 'tool.call.start', {
    tool: toolName,
    step_count: args.steps.length,
    actions: args.steps.map((step) => step.action),
  }, meta);

  try {
    const payload = await executeIdeTool(args);
    writeStderrLog('info', 'tool.call.done', {
      tool: toolName,
      screenshot_path: payload.screenshot_path || '',
      bytes: payload.bytes || 0,
      warning: payload.warning || '',
    }, meta);
    return toToolResult(payload, false);
  } catch (error) {
    const pageState = await collectPageState(session.page).catch(() => ({ file_path: '', status: '', cursor: '', inspector: '' }));
    const summary = buildSummary(pageState, [], serializeError(error).message);
    const payload = await captureScreenshot(session.page, meta, summary);
    payload.error = serializeError(error).message;

    writeStderrLog('error', 'tool.call.failed', {
      tool: toolName,
      error: serializeError(error),
      screenshot_path: payload.screenshot_path || '',
      warning: payload.warning || '',
    }, meta);

    return toToolResult(payload, true);
  }
});

async function main() {
  await cleanupOldScreenshots(cli.tmpdir, {});
  writeStderrLog('info', 'server.start', {
    url: cli.url,
    debug: cli.debug,
    tmpdir: cli.tmpdir,
  });

  const transport = new StdioServerTransport();
  await server.connect(transport);
  writeStderrLog('info', 'server.ready', { tool: TOOL_NAME });
}

async function shutdown(signal) {
  writeStderrLog('info', 'server.shutdown', { signal });
  await session.close({}).catch(() => { });
}

process.on('SIGINT', () => {
  shutdown('SIGINT').finally(() => process.exit(0));
});
process.on('SIGTERM', () => {
  shutdown('SIGTERM').finally(() => process.exit(0));
});
process.on('beforeExit', () => {
  return shutdown('beforeExit');
});

main().catch((error) => {
  writeStderrLog('error', 'server.fatal', { error: serializeError(error) });
  process.exit(1);
});
