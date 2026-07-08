import { setTimeout as sleep } from 'node:timers/promises';
import { fileURLToPath } from 'node:url';

import {
  createMCPFrameReader,
  encodeMCPFrame,
} from './ui-test-mcp-framing.mjs';

const DEFAULT_BASE_URL = 'http://127.0.0.1:5175/';
const DEFAULT_PROTOCOL_VERSION = '2024-11-05';
const SERVER_INFO = Object.freeze({
  name: 'super-dolphin-ui-test-mcp',
  version: '0.1.0',
});
const UI_TEST_CONTRACT_URL = new URL('../src/devtools/uiTestContract.js', import.meta.url);
const ACCEPTANCE_GLOBAL = '__SUPER_DOLPHIN_UI_TEST_ACCEPTANCE__';

const ERROR_CODES = Object.freeze({
  PARSE_ERROR: -32700,
  INVALID_REQUEST: -32600,
  METHOD_NOT_FOUND: -32601,
  INVALID_PARAMS: -32602,
  INTERNAL_ERROR: -32603,
  LIFECYCLE_ERROR: -32000,
});

class JSONRPCError extends Error {
  constructor(code, message, data) {
    super(message);
    this.name = 'JSONRPCError';
    this.code = code;
    this.data = data;
  }
}

class ToolExecutionError extends Error {
  constructor(message, details = {}) {
    super(message);
    this.name = 'ToolExecutionError';
    this.details = details;
  }
}

export async function loadUITestContract(contractURL = UI_TEST_CONTRACT_URL) {
  return import(contractURL.href);
}

export function validateBaseURL(value = DEFAULT_BASE_URL) {
  let parsed;
  try {
    parsed = new URL(value);
  }
  catch (error) {
    throw new Error(`SUPER_DOLPHIN_UI_TEST_BASE_URL must be a valid URL: ${error.message}`);
  }

  if (parsed.protocol !== 'http:' && parsed.protocol !== 'https:') {
    throw new Error('SUPER_DOLPHIN_UI_TEST_BASE_URL must use http or https');
  }
  if (parsed.username || parsed.password) {
    throw new Error('SUPER_DOLPHIN_UI_TEST_BASE_URL must not include credentials');
  }

  const hostname = parsed.hostname.toLowerCase();
  if (!['127.0.0.1', 'localhost', '[::1]', '::1'].includes(hostname)) {
    throw new Error('SUPER_DOLPHIN_UI_TEST_BASE_URL must use 127.0.0.1, localhost, or [::1]');
  }
  return parsed;
}

export function createToolDefinitions(contract) {
  const checked = assertContract(contract);
  const limit = checked.UI_TEST_LIMITS;
  const routeNames = Object.keys(checked.UI_TEST_ROUTES);

  const definitions = {
    ui_snapshot: {
      name: 'ui_snapshot',
      description: 'Read a sanitized snapshot of the local Super Dolphin UI state.',
      inputSchema: strictSchema({}),
    },
    ui_action: {
      name: 'ui_action',
      description: 'Execute an allowlisted local UI action with fixed targets only.',
      inputSchema: strictSchema({
        action: { type: 'string', enum: checked.UI_TEST_ACTIONS },
        route: { type: 'string', enum: routeNames },
        target: { type: 'string', enum: checked.UI_TEST_TARGETS },
        text: { type: 'string', maxLength: limit.maxTextLength },
        waitState: { type: 'string', enum: checked.UI_TEST_WAIT_STATES },
        timeoutMs: { type: 'integer', minimum: 1, maximum: limit.maxTimeoutMs },
        expected: { type: ['number', 'string', 'boolean'] },
      }, ['action']),
    },
    ui_diagnostics: {
      name: 'ui_diagnostics',
      description: 'Read sanitized browser diagnostics from the UI test harness.',
      inputSchema: strictSchema({}),
    },
    ui_frontend_logs: {
      name: 'ui_frontend_logs',
      description: 'Read sanitized frontend log entries from the UI test harness.',
      inputSchema: strictSchema({
        level: { type: 'string' },
        source: { type: 'string' },
        since: { type: 'string' },
        limit: { type: 'integer', minimum: 1, maximum: limit.maxLimit },
      }),
    },
    ui_scenario_run: {
      name: 'ui_scenario_run',
      description: 'Run an allowlisted local UI test scenario through existing UI Test MCP primitives.',
      inputSchema: strictSchema({
        scenario: { type: 'string', enum: checked.UI_TEST_SCENARIO_IDS },
        route: { type: 'string', enum: routeNames },
        text: { type: 'string', maxLength: limit.maxTextLength },
        timeoutMs: { type: 'integer', minimum: 1, maximum: limit.maxTimeoutMs },
        logs: {
          type: 'object',
          additionalProperties: false,
          properties: {
            level: { type: 'string' },
            source: { type: 'string' },
            since: { type: 'string' },
            limit: { type: 'integer', minimum: 1, maximum: limit.maxLimit },
          },
        },
      }, ['scenario']),
    },
  };

  return checked.UI_TEST_TOOLS.map((toolName) => {
    if (!definitions[toolName]) throw new Error(`missing MCP tool definition for ${toolName}`);
    return definitions[toolName];
  });
}

export function createUITestMCPServer(options = {}) {
  const contract = assertContract(options.contract);
  const env = options.env || process.env;
  assertProductionGate(env);

  const baseURL = validateBaseURL(env.SUPER_DOLPHIN_UI_TEST_BASE_URL || options.baseURL || DEFAULT_BASE_URL);
  const stdout = options.stdout || process.stdout;
  const stderr = options.stderr || process.stderr;
  const browserFactory = options.browserFactory || (() => defaultBrowserFactory(env));
  const toolDefinitions = createToolDefinitions(contract);
  const config = {
    allowSubmit: truthy(env.SUPER_DOLPHIN_UI_TEST_ALLOW_SUBMIT),
    acceptanceOwnsUI: truthy(env.SUPER_DOLPHIN_UI_TEST_ACCEPTANCE_OWNS_UI),
    acceptanceToken: env.SUPER_DOLPHIN_UI_TEST_ACCEPTANCE_TOKEN || '',
  };

  const state = {
    initialized: false,
    shutdown: false,
    stopped: false,
    browser: null,
    page: null,
    pageReady: null,
    cleanupStarted: false,
    exclusive: Promise.resolve(),
    waitAbortController: new AbortController(),
  };

  const reader = createMCPFrameReader({
    limits: contract.UI_TEST_LIMITS,
    onMessage: async (message, mode) => {
      const response = await handleMessage(message);
      if (response) stdout.write(encodeMCPFrame(response, mode));
    },
    onError: async (error, mode) => {
      const response = makeErrorResponse(ERROR_CODES.PARSE_ERROR, error.message, null);
      stdout.write(encodeMCPFrame(response, mode));
    },
  });

  async function handleMessage(message) {
    if (!isPlainObject(message)) {
      return makeErrorResponse(ERROR_CODES.INVALID_REQUEST, 'JSON-RPC request must be an object', null);
    }

    const id = responseID(message);
    if (message.jsonrpc !== '2.0') {
      return makeErrorResponse(ERROR_CODES.INVALID_REQUEST, 'JSON-RPC request must include jsonrpc: "2.0"', id);
    }
    if (typeof message.method !== 'string') {
      return makeErrorResponse(ERROR_CODES.INVALID_REQUEST, 'JSON-RPC request method must be a string', id);
    }

    const hasID = Object.hasOwn(message, 'id');
    const isNotification = !hasID;

    try {
      switch (message.method) {
        case 'initialize':
          return successResponse(message.id, handleInitialize(message.params), isNotification);
        case 'notifications/initialized':
          validateNoParams(message.params, 'notifications/initialized params', contract);
          return null;
        case 'ping':
          requireActiveLifecycle('ping');
          validateNoParams(message.params, 'ping params', contract);
          return successResponse(message.id, {}, isNotification);
        case 'shutdown':
          requireActiveLifecycle('shutdown');
          validateNoParams(message.params, 'shutdown params', contract);
          state.shutdown = true;
          await cleanup('shutdown');
          return successResponse(message.id, {}, isNotification);
        case 'exit':
          validateNoParams(message.params, 'exit params', contract);
          await stop('exit');
          return successResponse(message.id, {}, isNotification);
        case 'tools/list':
          requireActiveLifecycle('tools/list');
          validateNoParams(message.params, 'tools/list params', contract);
          return successResponse(message.id, { tools: toolDefinitions }, isNotification);
        case 'tools/call':
          requireActiveLifecycle('tools/call');
          return successResponse(message.id, await handleToolCall(message.params), isNotification);
        default:
          if (isNotification) return null;
          return makeErrorResponse(ERROR_CODES.METHOD_NOT_FOUND, `unknown JSON-RPC method: ${message.method}`, id);
      }
    }
    catch (error) {
      if (error instanceof JSONRPCError) {
        return makeErrorResponse(error.code, error.message, id, error.data);
      }
      return makeErrorResponse(ERROR_CODES.INTERNAL_ERROR, error.message || 'unexpected MCP server exception', id);
    }
  }

  function handleInitialize(params) {
    validateInitializeParams(params, contract);
    state.initialized = true;
    state.shutdown = false;
    return {
      protocolVersion: params?.protocolVersion || DEFAULT_PROTOCOL_VERSION,
      capabilities: { tools: {} },
      serverInfo: SERVER_INFO,
    };
  }

  async function handleToolCall(params) {
    const { name, args } = validateToolCallParams(params);
    return runExclusive(async () => {
      try {
        switch (name) {
          case 'ui_snapshot':
            return toolResult({ tool: name, snapshot: await readSnapshot() });
          case 'ui_diagnostics':
            return toolResult({ tool: name, diagnostics: await readDiagnostics() });
          case 'ui_frontend_logs':
            return toolResult({ tool: name, logs: await readFrontendLogs(args) });
          case 'ui_action':
            return runAction(args);
          case 'ui_scenario_run':
            return runScenario(args);
          default:
            throw invalidParams(`unknown UI test MCP tool: ${name}`);
        }
      }
      catch (error) {
        return toolError({
          tool: name,
          action: args.action || null,
          target: args.target || null,
          timeoutMs: args.timeoutMs || null,
          reason: error.message || 'UI test MCP tool failed',
        });
      }
    });
  }

  async function runAction(args) {
    const content = await executeAction(args);
    if (content?.error) return toolError(content.error);
    return toolResult(content);
  }

  async function executeAction(args) {
    switch (args.action) {
      case 'navigate':
        return executeNavigate(args);
      case 'fill_composer':
        return executeFillComposer(args);
      case 'submit_composer':
        return executeSubmitComposer(args);
      case 'wait_for':
        return executeWaitFor(args);
      default:
        throw invalidParams(`unknown UI test action: ${args.action}`);
    }
  }

  async function executeNavigate(args) {
    const page = await ensurePage();
    const targetURL = new URL(contract.UI_TEST_ROUTES[args.route], baseURL);
    await page.goto(targetURL.toString(), { waitUntil: 'domcontentloaded' });
    await recordActionLog('navigate', { route: args.route, path: targetURL.pathname });
    return {
      action: args.action,
      route: args.route,
      path: targetURL.pathname,
    };
  }

  async function executeFillComposer(args) {
    const page = await ensurePage();
    await page.locator('[data-testid="composer-input"]').fill(args.text);
    await recordActionLog('fill_composer', {
      target: 'composer_input',
      textLength: args.text.length,
    });
    return {
      action: args.action,
      target: 'composer_input',
      textLength: args.text.length,
    };
  }

  async function executeSubmitComposer(args) {
    if (!config.allowSubmit || !config.acceptanceOwnsUI || !config.acceptanceToken) {
      return {
        error: {
          tool: 'ui_action',
          action: args.action,
          target: 'composer_submit',
          timeoutMs: null,
          reason: 'submit_composer requires server-owned isolated acceptance mode',
        },
      };
    }

    const snapshot = await readSnapshot();
    if (!isActionEnabled(snapshot, 'submit_composer')) {
      return {
        error: {
          tool: 'ui_action',
          action: args.action,
          target: 'composer_submit',
          timeoutMs: null,
          reason: 'submit_composer is not enabled in the current UI state',
        },
      };
    }

    const page = await ensurePage();
    const tokenInput = { token: config.acceptanceToken };
    const verification = await page.evaluate((input) => (
      window.__SUPER_DOLPHIN_UI_TEST__.verifyIsolatedAcceptance(input)
    ), tokenInput);
    if (!verification?.isolated || !verification?.tokenMatched) {
      return {
        error: {
          tool: 'ui_action',
          action: args.action,
          target: 'composer_submit',
          timeoutMs: null,
          reason: 'isolated acceptance token was not verified by the page',
        },
      };
    }

    const result = await page.evaluate((input) => (
      window.__SUPER_DOLPHIN_UI_TEST__.submitComposerInIsolation(input)
    ), tokenInput);
    return {
      action: args.action,
      target: 'composer_submit',
      result,
    };
  }

  async function executeWaitFor(args) {
    const timeoutMs = args.timeoutMs;
    const started = Date.now();
    while (Date.now() - started <= timeoutMs) {
      const snapshot = await readSnapshot();
      if (waitConditionMatched(contract, snapshot, args)) {
        await recordActionLog('wait_for', {
          waitState: args.waitState,
          elapsedMs: Date.now() - started,
        });
        return {
          action: args.action,
          waitState: args.waitState,
          elapsedMs: Date.now() - started,
        };
      }
      await sleep(contract.UI_TEST_LIMITS.pollIntervalMs, undefined, {
        signal: state.waitAbortController.signal,
      });
    }
    throw new ToolExecutionError(`timed out waiting for ${args.waitState}`, {
      action: args.action,
      timeoutMs,
    });
  }

  async function runScenario(args) {
    try {
      return toolResult(await executeScenario(args));
    }
    catch (error) {
      return toolError({
        tool: 'ui_scenario_run',
        scenario: args.scenario,
        stepIndex: error.details?.stepIndex ?? null,
        code: error.details?.code || 'scenario_failed',
        action: error.details?.action || null,
        target: error.details?.target || null,
        timeoutMs: error.details?.timeoutMs ?? args.timeoutMs,
        reason: error.message || 'scenario failed',
      });
    }
  }

  async function executeScenario(args) {
    const steps = [];
    await scenarioDiagnosticsGate(args.scenario, steps, 'diagnostics_before');
    for (const step of scenarioSteps(args)) {
      await executeScenarioStep(args, step, steps);
    }
    const finalSnapshot = await readSnapshot();
    steps.push({ index: steps.length, name: 'snapshot_after', status: 'passed' });
    const diagnostics = await scenarioDiagnosticsGate(args.scenario, steps, 'diagnostics_after');
    const logs = await readFrontendLogs(args.logs);
    steps.push({ index: steps.length, name: 'logs_after', status: 'passed' });
    return {
      tool: 'ui_scenario_run',
      scenario: args.scenario,
      success: true,
      steps,
      finalSnapshot,
      diagnostics,
      logs,
    };
  }

  function scenarioSteps(args) {
    const text = args.text || 'MCP UI test input';
    if (args.scenario === 'chat_composer_probe') {
      return [
        { name: 'snapshot_before', kind: 'snapshot' },
        { name: 'navigate_chat', kind: 'action', args: { action: 'navigate', route: 'chat' } },
        { name: 'fill_composer', kind: 'action', args: { action: 'fill_composer', target: 'composer_input', text } },
        { name: 'wait_composer_text', kind: 'action', args: { action: 'wait_for', waitState: 'composer_text_length', expected: text.length, timeoutMs: args.timeoutMs } },
      ];
    }
    if (args.scenario === 'frontend_navigation_probe') {
      return [
        { name: 'snapshot_before', kind: 'snapshot' },
        { name: 'navigate_chat', kind: 'action', args: { action: 'navigate', route: 'chat' } },
        { name: 'fill_composer', kind: 'action', args: { action: 'fill_composer', target: 'composer_input', text } },
        { name: 'wait_composer_text', kind: 'action', args: { action: 'wait_for', waitState: 'composer_text_length', expected: text.length, timeoutMs: args.timeoutMs } },
        { name: 'navigate_observability', kind: 'action', args: { action: 'navigate', route: 'observability' } },
      ];
    }
    if (args.scenario === 'observability_logs_probe') {
      return [
        { name: 'snapshot_before', kind: 'snapshot' },
        { name: 'navigate_observability', kind: 'action', args: { action: 'navigate', route: 'observability' } },
      ];
    }
    if (args.scenario === 'settings_open_probe') {
      return [
        { name: 'snapshot_before', kind: 'snapshot' },
        { name: 'navigate_settings', kind: 'action', args: { action: 'navigate', route: 'settings' } },
      ];
    }
    if (args.scenario === 'open_route_probe') {
      return [
        { name: 'snapshot_before', kind: 'snapshot' },
        { name: `navigate_${args.route}`, kind: 'action', args: { action: 'navigate', route: args.route } },
      ];
    }
    throw invalidParams(`unknown UI test scenario: ${args.scenario}`);
  }

  async function executeScenarioStep(args, step, steps) {
    const index = steps.length;
    try {
      if (step.kind === 'snapshot') {
        await readSnapshot();
      }
      else if (step.kind === 'action') {
        const result = await executeAction(step.args);
        if (result?.error) {
          throw new ToolExecutionError(result.error.reason || 'scenario action failed', {
            stepIndex: index,
            code: 'scenario_action_rejected',
            action: step.args.action,
            target: step.args.target || null,
            timeoutMs: step.args.timeoutMs || args.timeoutMs,
          });
        }
      }
      else {
        throw new ToolExecutionError(`unknown scenario step kind: ${step.kind}`, {
          stepIndex: index,
          code: 'scenario_step_kind_unknown',
        });
      }
      steps.push({ index, name: step.name, status: 'passed' });
    }
    catch (error) {
      throw new ToolExecutionError(error.message || 'scenario step failed', {
        stepIndex: index,
        code: error.details?.code || 'scenario_step_failed',
        action: step.args?.action || null,
        target: step.args?.target || null,
        timeoutMs: step.args?.timeoutMs || args.timeoutMs,
      });
    }
  }

  async function scenarioDiagnosticsGate(scenario, steps, name) {
    const diagnostics = await readDiagnostics();
    const issueCounts = {
      consoleErrors: Array.isArray(diagnostics.consoleErrors) ? diagnostics.consoleErrors.length : 0,
      bridgeErrors: Array.isArray(diagnostics.bridgeErrors) ? diagnostics.bridgeErrors.length : 0,
      unhandledErrors: Array.isArray(diagnostics.unhandledErrors) ? diagnostics.unhandledErrors.length : 0,
    };
    const failingKeys = Object.entries(issueCounts)
      .filter(([, count]) => count > 0)
      .map(([key]) => key);
    if (failingKeys.length > 0) {
      throw new ToolExecutionError(`scenario ${scenario} diagnostics contain ${failingKeys.join(', ')}`, {
        stepIndex: steps.length,
        code: 'scenario_diagnostics_failed',
      });
    }
    steps.push({ index: steps.length, name, status: 'passed' });
    return diagnostics;
  }

  async function readSnapshot() {
    const page = await ensurePage();
    return page.evaluate(() => window.__SUPER_DOLPHIN_UI_TEST__.snapshot());
  }

  async function readDiagnostics() {
    const page = await ensurePage();
    return page.evaluate(() => window.__SUPER_DOLPHIN_UI_TEST__.diagnostics());
  }

  async function readFrontendLogs(filters) {
    const page = await ensurePage();
    return page.evaluate((input) => window.__SUPER_DOLPHIN_UI_TEST__.frontendLogs(input), filters);
  }

  async function recordActionLog(message, fields) {
    const page = await ensurePage();
    return page.evaluate((entry) => window.__SUPER_DOLPHIN_UI_TEST__.recordLog(entry), {
      level: 'info',
      source: 'ui_test_mcp',
      message,
      fields,
    });
  }

  async function ensurePage() {
    if (state.page) return state.page;
    if (!state.pageReady) {
      state.pageReady = (async () => {
        let browser;
        let page;
        try {
          browser = await browserFactory();
          page = await browser.newPage();
          if (config.acceptanceToken) {
            await page.addInitScript(({ globalName, token, isolated }) => {
              Object.defineProperty(window, globalName, {
                value: { token, isolated },
                configurable: false,
                enumerable: false,
                writable: false,
              });
            }, {
              globalName: ACCEPTANCE_GLOBAL,
              token: config.acceptanceToken,
              isolated: config.allowSubmit && config.acceptanceOwnsUI,
            });
          }
          await page.goto(baseURL.toString(), { waitUntil: 'domcontentloaded' });
          await waitForHarness(page);
          state.browser = browser;
          state.page = page;
          return page;
        }
        catch (error) {
          await closeIfPresent(page);
          await closeIfPresent(browser);
          state.pageReady = null;
          throw error;
        }
      })();
    }
    return state.pageReady;
  }

  async function waitForHarness(page) {
    if (typeof page.waitForFunction !== 'function') return;
    await page.waitForFunction((globalName) => {
      const harness = window[globalName];
      return Boolean(
        harness &&
        typeof harness.snapshot === 'function' &&
        typeof harness.frontendLogs === 'function' &&
        typeof harness.diagnostics === 'function' &&
        typeof harness.recordLog === 'function'
      );
    }, contract.UI_TEST_GLOBAL, {
      timeout: contract.UI_TEST_LIMITS.defaultTimeoutMs,
    });
  }

  function validateToolCallParams(params) {
    validateExactObject(params, ['name', 'arguments'], 'tools/call params', contract);
    if (typeof params.name !== 'string') throw invalidParams('tools/call params.name must be a string');
    wrapContractValidation(() => contract.assertKnownToolName(params.name));

    const args = params.arguments == null ? {} : params.arguments;
    if (!isPlainObject(args)) throw invalidParams('tools/call params.arguments must be an object');
    return { name: params.name, args: validateToolArguments(params.name, args) };
  }

  function validateToolArguments(name, args) {
    if (name === 'ui_snapshot' || name === 'ui_diagnostics') {
      validateExactObject(args, [], `${name} arguments`, contract);
      return {};
    }
    if (name === 'ui_frontend_logs') return validateFrontendLogArgs(args);
    if (name === 'ui_action') return validateActionArgs(args);
    if (name === 'ui_scenario_run') return validateScenarioArgs(args);
    throw invalidParams(`unknown UI test MCP tool: ${name}`);
  }

  function validateFrontendLogArgs(args) {
    validateExactObject(args, ['level', 'source', 'since', 'limit'], 'ui_frontend_logs arguments', contract);
    const normalized = {};
    for (const field of ['level', 'source', 'since']) {
      if (args[field] == null) continue;
      if (typeof args[field] !== 'string') throw invalidParams(`ui_frontend_logs ${field} must be a string`);
      normalized[field] = args[field];
    }
    if (args.limit != null) normalized.limit = wrapContractValidation(() => contract.normalizeLimit(args.limit));
    return normalized;
  }

  function validateActionArgs(args) {
    validateExactObject(
      args,
      ['action', 'route', 'target', 'text', 'waitState', 'timeoutMs', 'expected'],
      'ui_action arguments',
      contract,
    );
    if (typeof args.action !== 'string') throw invalidParams('ui_action action must be a string');
    wrapContractValidation(() => contract.assertKnownActionName(args.action));

    switch (args.action) {
      case 'navigate':
        requireOnlyActionFields(args, ['action', 'route']);
        return { action: args.action, route: normalizeRoute(contract, args.route) };
      case 'fill_composer':
        requireOnlyActionFields(args, ['action', 'target', 'text']);
        if (args.target != null) assertTarget(contract, args.target, 'composer_input');
        if (typeof args.text !== 'string') throw invalidParams('fill_composer text must be a string');
        if (args.text.length > contract.UI_TEST_LIMITS.maxTextLength) {
          throw invalidParams('fill_composer text exceeds maxTextLength');
        }
        return { action: args.action, target: 'composer_input', text: args.text };
      case 'submit_composer':
        requireOnlyActionFields(args, ['action', 'target']);
        if (args.target != null) assertTarget(contract, args.target, 'composer_submit');
        return { action: args.action, target: 'composer_submit' };
      case 'wait_for':
        return validateWaitForArgs(args);
      default:
        throw invalidParams(`unknown UI test action: ${args.action}`);
    }
  }

  function validateScenarioArgs(args) {
    validateExactObject(args, ['scenario', 'route', 'text', 'timeoutMs', 'logs'], 'ui_scenario_run arguments', contract);
    if (typeof args.scenario !== 'string') throw invalidParams('ui_scenario_run scenario must be a string');
    wrapContractValidation(() => contract.assertKnownScenarioName(args.scenario));

    const normalized = { scenario: args.scenario };
    normalized.timeoutMs = wrapContractValidation(() => contract.normalizeTimeoutMs(args.timeoutMs));

    if (args.route != null) {
      if (args.scenario !== 'open_route_probe') {
        throw invalidParams('ui_scenario_run route is only valid for open_route_probe');
      }
      normalized.route = normalizeRoute(contract, args.route);
    }
    else if (args.scenario === 'open_route_probe') {
      throw invalidParams('ui_scenario_run route is required for open_route_probe');
    }

    if (args.text != null) {
      if (!['chat_composer_probe', 'frontend_navigation_probe'].includes(args.scenario)) {
        throw invalidParams(`ui_scenario_run text is not valid for ${args.scenario}`);
      }
      if (typeof args.text !== 'string') throw invalidParams('ui_scenario_run text must be a string');
      if (args.text.length > contract.UI_TEST_LIMITS.maxTextLength) {
        throw invalidParams('ui_scenario_run text exceeds maxTextLength');
      }
      normalized.text = args.text;
    }

    normalized.logs = args.logs == null
      ? { source: 'ui_test_mcp', limit: Math.min(20, contract.UI_TEST_LIMITS.maxLimit) }
      : validateFrontendLogArgs(args.logs);
    return normalized;
  }

  function validateWaitForArgs(args) {
    requireOnlyActionFields(args, ['action', 'waitState', 'route', 'expected', 'timeoutMs']);
    if (typeof args.waitState !== 'string') throw invalidParams('wait_for waitState must be a string');
    wrapContractValidation(() => assertKnownValue(contract.UI_TEST_WAIT_STATES, args.waitState, 'wait_for waitState'));
    const timeoutMs = wrapContractValidation(() => contract.normalizeTimeoutMs(args.timeoutMs));
    const output = { action: args.action, waitState: args.waitState, timeoutMs };

    if (args.waitState === 'route') output.route = normalizeRoute(contract, args.route);
    if (args.waitState === 'composer_text_length') {
      if (!Number.isSafeInteger(args.expected) || args.expected < 0) {
        throw invalidParams('wait_for composer_text_length expected must be a non-negative integer');
      }
      output.expected = args.expected;
    }
    if (args.waitState === 'frontend_ready') {
      if (args.route != null || args.expected != null) {
        throw invalidParams('wait_for frontend_ready does not accept route or expected');
      }
    }
    return output;
  }

  function requireActiveLifecycle(method) {
    if (!state.initialized) {
      throw new JSONRPCError(ERROR_CODES.LIFECYCLE_ERROR, `${method} requires initialize first`);
    }
    if (state.shutdown && method !== 'exit') {
      throw new JSONRPCError(ERROR_CODES.LIFECYCLE_ERROR, `${method} cannot run after shutdown`);
    }
  }

  async function runExclusive(task) {
    const run = state.exclusive.then(task, task);
    state.exclusive = run.catch(() => {});
    return run;
  }

  async function processChunk(chunk) {
    if (state.stopped) return;
    await reader.push(chunk);
  }

  async function endInput() {
    await reader.end();
    await stop('eof');
  }

  async function handleSignal(signal) {
    writeStderr(stderr, `received ${signal}; stopping UI test MCP server`);
    await stop(signal);
  }

  async function cleanup() {
    if (state.cleanupStarted) return;
    state.cleanupStarted = true;
    state.waitAbortController.abort();
    await closeIfPresent(state.page);
    await closeIfPresent(state.browser);
    state.page = null;
    state.browser = null;
    state.pageReady = null;
  }

  async function stop(reason) {
    await cleanup(reason);
    state.stopped = true;
    if (typeof options.onStop === 'function') options.onStop(reason);
  }

  return {
    baseURL,
    config,
    handleMessage,
    processChunk,
    endInput,
    cleanup,
    stop,
    handleSignal,
    isStopped: () => state.stopped,
  };
}

export async function runStdioMCPServer(options = {}) {
  const contract = options.contract || await loadUITestContract();
  const stdin = options.stdin || process.stdin;
  const proc = options.processRef || process;
  let processExitScheduled = false;
  const server = createUITestMCPServer({
    ...options,
    contract,
    onStop(reason) {
      if (typeof options.onStop === 'function') options.onStop(reason);
      if (['exit', 'eof', 'SIGINT', 'SIGTERM'].includes(reason)) {
        scheduleProcessExit();
      }
    },
  });

  const onData = (chunk) => {
    void server.processChunk(chunk).catch((error) => {
      writeStderr(options.stderr || process.stderr, `failed to process MCP frame: ${error.message}`);
      void server.stop('frame-processing-error');
    });
  };
  const onEnd = () => {
    void server.endInput();
  };
  const onSIGINT = () => {
    void server.handleSignal('SIGINT');
  };
  const onSIGTERM = () => {
    void server.handleSignal('SIGTERM');
  };

  stdin.on('data', onData);
  stdin.on('end', onEnd);
  if (typeof stdin.resume === 'function') stdin.resume();
  proc.once('SIGINT', onSIGINT);
  proc.once('SIGTERM', onSIGTERM);

  return server;

  function scheduleProcessExit() {
    if (processExitScheduled) return;
    processExitScheduled = true;
    globalThis.setTimeout(() => {
      if (typeof proc.exit === 'function') {
        proc.exit(0);
        return;
      }
      proc.exitCode = 0;
    }, 0);
  }
}

function validateInitializeParams(params, contract) {
  if (params == null) return;
  validatePlainObject(params, 'initialize params');
  const allowed = ['protocolVersion', 'capabilities', 'clientInfo'];
  validateExactObject(params, allowed, 'initialize params', contract);
  if (params.protocolVersion != null && typeof params.protocolVersion !== 'string') {
    throw invalidParams('initialize protocolVersion must be a string');
  }
  if (params.capabilities != null && !isPlainObject(params.capabilities)) {
    throw invalidParams('initialize capabilities must be an object');
  }
  if (params.clientInfo != null && !isPlainObject(params.clientInfo)) {
    throw invalidParams('initialize clientInfo must be an object');
  }
}

function validateNoParams(params, label, contract) {
  if (params == null) return;
  validateExactObject(params, [], label, contract);
}

function validateExactObject(value, allowedKeys, label, contract) {
  validatePlainObject(value, label);
  if (contract && typeof contract.validateExactKeys === 'function') {
    wrapContractValidation(() => contract.validateExactKeys(value, allowedKeys, label));
    return;
  }
  const extra = Object.keys(value).filter((key) => !allowedKeys.includes(key));
  if (extra.length > 0) throw invalidParams(`${label} contains unknown field: ${extra[0]}`);
}

function validatePlainObject(value, label) {
  if (!isPlainObject(value)) throw invalidParams(`${label} must be an object`);
}

function requireOnlyActionFields(args, allowedKeys) {
  const extra = Object.keys(args).filter((key) => !allowedKeys.includes(key) && args[key] != null);
  if (extra.length > 0) throw invalidParams(`${args.action} does not accept field: ${extra[0]}`);
}

function normalizeRoute(contract, route) {
  if (typeof route !== 'string') throw invalidParams('route must be a string');
  if (!Object.hasOwn(contract.UI_TEST_ROUTES, route)) {
    throw invalidParams(`unknown UI test route: ${route}`);
  }
  return route;
}

function assertTarget(contract, target, expected) {
  wrapContractValidation(() => contract.assertKnownTargetName(target));
  if (target !== expected) throw invalidParams(`${target} is not valid for ${expected}`);
}

function waitConditionMatched(contract, snapshot, args) {
  if (args.waitState === 'frontend_ready') return Boolean(snapshot);
  if (args.waitState === 'route') {
    const routePath = contract.UI_TEST_ROUTES[args.route];
    return snapshot.route === routePath || snapshot.route === args.route;
  }
  if (args.waitState === 'composer_text_length') return snapshot.inputTextLength === args.expected;
  return false;
}

function assertKnownValue(values, value, label) {
  if (!values.includes(value)) throw new Error(`${label} is unknown: ${value}`);
}

function isActionEnabled(snapshot, actionName) {
  if (!Array.isArray(snapshot?.availableActions)) return false;
  return snapshot.availableActions.some((entry) => {
    if (entry === actionName) return true;
    if (!isPlainObject(entry)) return false;
    const name = entry.action || entry.name;
    return name === actionName && entry.enabled !== false;
  });
}

function toolResult(structuredContent) {
  return {
    content: [{ type: 'text', text: JSON.stringify(structuredContent) }],
    structuredContent,
    isError: false,
  };
}

function toolError({ tool, action, target, timeoutMs, reason, ...details }) {
  const structuredContent = {
    error: {
      tool,
      ...details,
      action,
      target,
      timeoutMs,
      reason,
    },
  };
  return {
    content: [{ type: 'text', text: JSON.stringify(structuredContent) }],
    structuredContent,
    isError: true,
  };
}

function successResponse(id, result, isNotification) {
  if (isNotification) return null;
  return { jsonrpc: '2.0', id, result };
}

function makeErrorResponse(code, message, id, data) {
  const error = { code, message };
  if (data !== undefined) error.data = data;
  return { jsonrpc: '2.0', id, error };
}

function invalidParams(message) {
  return new JSONRPCError(ERROR_CODES.INVALID_PARAMS, message);
}

function responseID(message) {
  if (!isPlainObject(message) || !Object.hasOwn(message, 'id')) return null;
  return isValidID(message.id) ? message.id : null;
}

function isValidID(id) {
  return id === null || typeof id === 'string' || (typeof id === 'number' && Number.isFinite(id));
}

function isPlainObject(value) {
  return value != null && typeof value === 'object' && !Array.isArray(value) && Object.getPrototypeOf(value) === Object.prototype;
}

function assertContract(contract) {
  if (contract == null || typeof contract !== 'object') throw new Error('UI test MCP contract module is required');
  const arrayKeys = ['UI_TEST_TOOLS', 'UI_TEST_ACTIONS', 'UI_TEST_TARGETS', 'UI_TEST_WAIT_STATES', 'UI_TEST_SCENARIO_IDS'];
  for (const key of arrayKeys) {
    if (!Array.isArray(contract[key])) throw new Error(`UI test MCP contract missing ${key}`);
  }
  if (!isPlainObject(contract.UI_TEST_ROUTES)) throw new Error('UI test MCP contract missing UI_TEST_ROUTES');
  if (!isPlainObject(contract.UI_TEST_SCENARIOS)) throw new Error('UI test MCP contract missing UI_TEST_SCENARIOS');
  if (!isPlainObject(contract.UI_TEST_LIMITS)) throw new Error('UI test MCP contract missing UI_TEST_LIMITS');
  const functionKeys = [
    'assertKnownToolName',
    'assertKnownActionName',
    'assertKnownTargetName',
    'assertKnownScenarioName',
    'normalizeLimit',
    'normalizeTimeoutMs',
    'validateExactKeys',
  ];
  for (const key of functionKeys) {
    if (typeof contract[key] !== 'function') throw new Error(`UI test MCP contract missing ${key}`);
  }
  for (const key of ['maxLimit', 'maxTextLength', 'maxTimeoutMs', 'pollIntervalMs', 'maxFrameBytes', 'maxHeaderBytes', 'maxLineBytes']) {
    if (!Number.isSafeInteger(contract.UI_TEST_LIMITS[key]) || contract.UI_TEST_LIMITS[key] <= 0) {
      throw new Error(`UI test MCP contract UI_TEST_LIMITS.${key} must be a positive integer`);
    }
  }
  return contract;
}

function strictSchema(properties, required = []) {
  return {
    type: 'object',
    properties,
    required,
    additionalProperties: false,
  };
}

function assertProductionGate(env) {
  const productionMode = env.NODE_ENV === 'production' || env.MODE === 'production' || env.VITE_MODE === 'production';
  if (productionMode && env.SUPER_DOLPHIN_UI_TEST_MCP !== '1') {
    throw new Error('SUPER_DOLPHIN_UI_TEST_MCP=1 is required in production mode');
  }
}

function truthy(value) {
  return value === '1' || value === 'true' || value === 'yes';
}

export function browserLaunchOptions(env = process.env) {
  const executablePath = env.SUPER_DOLPHIN_UI_TEST_BROWSER_EXECUTABLE_PATH || env.SUPER_DOLPHIN_UI_TEST_BROWSER_EXECUTABLE || '';
  return executablePath ? { executablePath } : {};
}

async function defaultBrowserFactory(env = process.env) {
  const { chromium } = await import('@playwright/test');
  const launchOptions = browserLaunchOptions(env);
  return Object.keys(launchOptions).length > 0
    ? chromium.launch(launchOptions)
    : chromium.launch();
}

async function closeIfPresent(value) {
  if (value && typeof value.close === 'function') await value.close();
}

function wrapContractValidation(fn) {
  try {
    return fn();
  }
  catch (error) {
    throw invalidParams(error.message);
  }
}

function writeStderr(stderr, message) {
  if (stderr && typeof stderr.write === 'function') stderr.write(`[ui-test-mcp] ${message}\n`);
}

function isMain(metaURL) {
  return process.argv[1] && fileURLToPath(metaURL) === process.argv[1];
}

if (isMain(import.meta.url)) {
  runStdioMCPServer().catch((error) => {
    writeStderr(process.stderr, error.message);
    process.exitCode = 1;
  });
}
