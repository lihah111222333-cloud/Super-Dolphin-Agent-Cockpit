import { chromium } from 'playwright';
import { mkdir, writeFile } from 'node:fs/promises';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

import { discoverBusinessFlows } from './agentic-e2e-discovery.mjs';
import { DEFAULT_AGENTIC_GOAL, decideNextAction, normalizeGoal } from './agentic-e2e-planner.mjs';
import { renderDiscoveryMarkdown, summarizeDiscovery } from './agentic-e2e-reporter.mjs';
import {
  agenticE2ESandboxForRun,
  prepareAgenticE2ESandbox,
  snapshotAgenticE2ESandbox,
} from './agentic-e2e-sandbox.mjs';
import {
  assertAgenticE2EMockWailsClean,
  installAgenticE2EMockWails,
  readAgenticE2EMockWailsState,
} from './agentic-e2e-wails-mock.mjs';

const DEFAULT_BASE_URL = 'http://127.0.0.1:5176';
const DEFAULT_MAX_STEPS = 12;

export function repoRootFromScript(metaURL = import.meta.url) {
  return path.resolve(path.dirname(fileURLToPath(metaURL)), '..', '..');
}

export function agenticE2EConfig(env = process.env, repoRoot = repoRootFromScript(), argv = []) {
  const cli = parseAgenticE2EArgs(argv);
  const runID = normalizeRunID(env.SUPER_DOLPHIN_AGENTIC_E2E_RUN_ID || new Date().toISOString());
  const goal = normalizeGoal({
    id: cli.goal || env.SUPER_DOLPHIN_AGENTIC_E2E_GOAL || DEFAULT_AGENTIC_GOAL.id,
    composerText: cli.composerText || env.SUPER_DOLPHIN_AGENTIC_E2E_COMPOSER_TEXT || DEFAULT_AGENTIC_GOAL.composerText,
  });
  const outputBaseDir = cli.outputDir || env.SUPER_DOLPHIN_AGENTIC_E2E_OUTPUT_DIR || path.join(repoRoot, '.tmp', 'agentic-e2e', runID);
  const sandbox = agenticE2ESandboxForRun(repoRoot, runID);
  return {
    repoRoot,
    runID,
    baseURL: normalizeURL(cli.baseURL || env.SUPER_DOLPHIN_AGENTIC_E2E_BASE_URL || env.SUPER_DOLPHIN_DESKTOP_UX_BASE_URL || DEFAULT_BASE_URL),
    outputDir: path.join(outputBaseDir, normalizeRunID(goal.id)),
    maxSteps: positiveInt(cli.maxSteps || env.SUPER_DOLPHIN_AGENTIC_E2E_MAX_STEPS, DEFAULT_MAX_STEPS),
    headless: cli.headed === undefined ? !truthyEnv(env.SUPER_DOLPHIN_AGENTIC_E2E_HEADED) : !cli.headed,
    chromiumExecutable: normalizeString(env.PLAYWRIGHT_CHROMIUM_EXECUTABLE),
    mockWails: cli.mockWails === undefined ? truthyEnv(env.SUPER_DOLPHIN_AGENTIC_E2E_MOCK_WAILS) : cli.mockWails,
    sandbox,
    goal,
  };
}

export async function runAgenticE2E(config = agenticE2EConfig()) {
  assertGoalRuntimeSupported(config);
  await mkdir(config.outputDir, { recursive: true });
  if (config.mockWails || config.goal.requiresSandbox) {
    await prepareAgenticE2ESandbox(config);
  }
  const browser = await chromium.launch({
    headless: config.headless,
    ...(config.chromiumExecutable ? { executablePath: config.chromiumExecutable } : {}),
  });
  const page = await browser.newPage();
  const consoleMessages = [];
  const networkRequests = [];
  const pageErrors = [];
  const steps = [];
  let discoveredFlows = [];

  if (config.mockWails) await installAgenticE2EMockWails(page, { sandbox: config.sandbox });
  page.on('pageerror', (error) => {
    pageErrors.push(error.message || String(error));
  });
  page.on('console', (message) => {
    consoleMessages.push({
      type: message.type(),
      text: message.text(),
      location: message.location(),
    });
  });
  page.on('requestfinished', (request) => {
    networkRequests.push({ method: request.method(), url: request.url(), resourceType: request.resourceType() });
  });
  page.on('response', (response) => {
    networkRequests.push({ status: response.status(), url: response.url(), resourceType: response.request().resourceType() });
  });
  page.on('requestfailed', (request) => {
    networkRequests.push({
      method: request.method(),
      url: request.url(),
      resourceType: request.resourceType(),
      failure: request.failure()?.errorText || 'request failed',
    });
  });

  try {
    for (let stepIndex = 0; stepIndex < config.maxSteps; stepIndex += 1) {
      const facts = await collectPageFacts(page, consoleMessages);
      discoveredFlows = mergeDiscoveredFlows(discoveredFlows, discoverBusinessFlows(facts));
      const action = decideNextAction(facts, config.goal);
      steps.push({ step: stepIndex + 1, facts: compactFacts(facts), action });
      await writeStepEvidence(config.outputDir, stepIndex + 1, facts, action);

      if (action.type === 'done') {
        await assertCapturedRuntimeClean(page, config, pageErrors, networkRequests);
        await writeFinalEvidence(config.outputDir, page, steps, consoleMessages, networkRequests, discoveredFlows, await runtimeEvidence(page, config, pageErrors));
        return { success: true, steps, outputDir: config.outputDir };
      }
      if (action.type === 'fail') {
        throw new Error(action.reason || 'agentic e2e planner failed');
      }
      await performAction(page, action, config);
      await waitForReadiness(page, readinessForAction(action));
    }
    throw new Error(`agentic e2e exceeded max steps: ${config.maxSteps}`);
  }
  catch (error) {
    await writeFailureEvidence(config.outputDir, page, steps, consoleMessages, networkRequests, error, discoveredFlows, await runtimeEvidence(page, config, pageErrors));
    throw error;
  }
  finally {
    await browser.close();
  }
}

export async function collectPageFacts(page, consoleMessages = []) {
  const consoleErrors = consoleMessages
    .filter((message) => message.type === 'error')
    .map((message) => message.text);
  const structuralFacts = await page.evaluate(() => {
    function visibleByTestId(testId) {
      return isVisible(document.querySelector(`[data-testid="${testId}"]`));
    }
    function inputValueByTestId(testId) {
      const element = document.querySelector(`[data-testid="${testId}"]`);
      return element && 'value' in element ? String(element.value || '') : '';
    }
    function isVisible(element) {
      if (!element) return false;
      const style = window.getComputedStyle(element);
      if (style.visibility === 'hidden' || style.display === 'none') return false;
      const rect = element.getBoundingClientRect();
      return rect.width > 0 && rect.height > 0;
    }
    return {
      hasFrontendApp: visibleByTestId('frontend-app'),
      hasChatPage: visibleByTestId('chat-page'),
      composerVisible: visibleByTestId('composer-input'),
      composerValue: inputValueByTestId('composer-input'),
      chatActionsMenuVisible: visibleByTestId('chat-actions-menu'),
      projectMenuVisible: isVisible(document.querySelector('.project-dropdown[role="menu"]')),
      attachmentCount: document.querySelectorAll('.attachment-pill').length,
      runtimePanelVisible: visibleByTestId('runtime-panel'),
      observabilityPageVisible: visibleByTestId('observability-page'),
      recentLogsVisible: visibleByTestId('observability-recent-logs'),
      settingsPageVisible: visibleByTestId('settings-page'),
      settingsApiKeyVisible: isVisible(document.querySelector('#settings-sf-key')),
      settingsApiKeyValue: String(document.querySelector('#settings-sf-key')?.value || ''),
      settingsVideoNoticeVisible: visibleByTestId('settings-video-notice'),
      mockWailsCallMethods: Array.isArray(window.__AGENTIC_E2E_MOCK_WAILS__?.calls)
        ? window.__AGENTIC_E2E_MOCK_WAILS__.calls.map((call) => String(call?.method || '')).filter(Boolean)
        : [],
    };
  }).catch(() => ({
    hasFrontendApp: false,
    hasChatPage: false,
    composerVisible: false,
    composerValue: '',
    chatActionsMenuVisible: false,
    projectMenuVisible: false,
    attachmentCount: 0,
    runtimePanelVisible: false,
    observabilityPageVisible: false,
    recentLogsVisible: false,
    settingsPageVisible: false,
    settingsApiKeyVisible: false,
    settingsApiKeyValue: '',
    settingsVideoNoticeVisible: false,
    mockWailsCallMethods: [],
  }));
  const locators = {
    composer: page.getByTestId('composer-input'),
  };
  const summary = await domSummary(page);
  const summaryFacts = factsFromDOMSummary(summary);

  return {
    url: page.url(),
    title: await page.title().catch(() => ''),
    hasFrontendApp: structuralFacts.hasFrontendApp || summaryFacts.hasFrontendApp,
    hasChatPage: structuralFacts.hasChatPage || summaryFacts.hasChatPage,
    composerVisible: structuralFacts.composerVisible || summaryFacts.composerVisible,
    chatActionsMenuVisible: structuralFacts.chatActionsMenuVisible || summaryFacts.chatActionsMenuVisible,
    projectMenuVisible: structuralFacts.projectMenuVisible || summaryFacts.projectMenuVisible,
    attachmentCount: structuralFacts.attachmentCount,
    runtimePanelVisible: structuralFacts.runtimePanelVisible || summaryFacts.runtimePanelVisible,
    observabilityPageVisible: structuralFacts.observabilityPageVisible || summaryFacts.observabilityPageVisible,
    recentLogsVisible: structuralFacts.recentLogsVisible || summaryFacts.recentLogsVisible,
    settingsPageVisible: structuralFacts.settingsPageVisible || summaryFacts.settingsPageVisible,
    settingsApiKeyVisible: structuralFacts.settingsApiKeyVisible,
    settingsApiKeyValue: structuralFacts.settingsApiKeyValue,
    settingsVideoNoticeVisible: structuralFacts.settingsVideoNoticeVisible || summaryFacts.settingsVideoNoticeVisible,
    mockWailsCallMethods: structuralFacts.mockWailsCallMethods,
    composerValue: structuralFacts.composerValue || await locators.composer.inputValue({ timeout: 250 }).catch(() => ''),
    consoleErrors,
    accessibilitySnapshot: await accessibilitySnapshot(page),
    domSummary: summary,
  };
}

export function normalizeDOMSummaryItem(item = {}) {
  const parentTestId = normalizeString(item.parentTestId);
  const testId = normalizeString(item.testId);
  return {
    tag: normalizeString(item.tag),
    role: normalizeString(item.role),
    testId,
    parentTestId,
    sourceTestId: parentTestId || testId,
    ariaLabel: normalizeString(item.ariaLabel),
    text: normalizeString(item.text),
    disabled: Boolean(item.disabled),
  };
}

export function mergeDiscoveredFlows(existing = [], next = []) {
  const byID = new Map(existing.map((flow, index) => {
    const validated = validateDiscoveredFlow(flow, `existing[${index}]`);
    return [validated.id, validated];
  }));
  for (const [index, flow] of next.entries()) {
    const validated = validateDiscoveredFlow(flow, `next[${index}]`);
    const current = byID.get(validated.id);
    if (!current) {
      byID.set(validated.id, validated);
      continue;
    }
    const actionKeys = new Set(current.actions.map(actionKey));
    for (const action of validated.actions) {
      const key = actionKey(action);
      if (!actionKeys.has(key)) {
        current.actions.push(action);
        actionKeys.add(key);
      }
    }
    current.page = { ...current.page, ...validated.page };
    current.result = validated.result || current.result;
  }
  return Array.from(byID.values());
}

export function readinessForAction(action = {}) {
  const name = normalizeString(action.target?.name || action.target?.value || action.reason);
  if (name.includes('链路追踪')) return { type: 'testId', value: 'observability-page' };
  if (name.includes('查询最新日志')) return { type: 'testId', value: 'observability-recent-logs' };
  if (action.target?.parentTestId === 'settings-video-card') return { type: 'testId', value: 'settings-video-notice' };
  if (action.expectRoute) return { type: 'urlPath', value: action.expectRoute };
  if (action.type === 'goto') return { type: 'testId', value: 'frontend-app' };
  return { type: 'stableDOM' };
}

function validateDiscoveredFlow(flow, context) {
  if (!isRecord(flow) || typeof flow.id !== 'string' || !flow.id.trim()) {
    throw new Error(`invalid discovered flow at ${context}: expected object with non-empty string id`);
  }
  const actions = flow.actions || [];
  if (!Array.isArray(actions)) {
    throw new Error(`invalid discovered action at ${context}: expected actions array`);
  }
  return {
    ...flow,
    actions: actions.map((action, index) => validateDiscoveredAction(action, `${context}.actions[${index}]`)),
  };
}

function validateDiscoveredAction(action, context) {
  if (!isRecord(action)) {
    throw new Error(`invalid discovered action at ${context}: expected object`);
  }
  for (const field of ['type', 'label', 'safety', 'reason']) {
    if (typeof action[field] !== 'string' || !action[field].trim()) {
      throw new Error(`invalid discovered action at ${context}: expected non-empty string ${field}`);
    }
  }
  return action;
}

function isRecord(value) {
  return typeof value === 'object' && value !== null && !Array.isArray(value);
}

function actionKey(action = {}) {
  return `${action.type}|${action.label}|${action.safety}|${action.reason}`;
}

export async function performAction(page, action, config) {
  switch (action.type) {
    case 'goto':
      await page.goto(new URL(action.path || '/', config.baseURL).toString());
      await page.waitForLoadState('domcontentloaded');
      await page.getByTestId('frontend-app').waitFor({ state: 'visible', timeout: 15000 }).catch(() => {});
      await Promise.race([
        page.getByTestId('chat-page').waitFor({ state: 'visible', timeout: 15000 }),
        page.getByTestId('observability-page').waitFor({ state: 'visible', timeout: 15000 }),
      ]).catch(() => {});
      return;
    case 'fill':
      await resolveLocator(page, action.target).fill(action.value);
      return;
    case 'click':
      await resolveLocator(page, action.target).click();
      return;
    default:
      throw new Error(`unsupported agentic e2e action: ${action.type}`);
  }
}

export function resolveLocator(page, target = {}) {
  if (target.type === 'testId') return page.getByTestId(target.value);
  if (target.type === 'role') return page.getByRole(target.role, { name: target.name, exact: Boolean(target.exact) });
  if (target.type === 'css') return page.locator(target.value);
  if (target.type === 'nestedRole') {
    return page.getByTestId(target.parentTestId).getByRole(target.role, { name: target.name });
  }
  throw new Error(`unsupported agentic e2e target: ${JSON.stringify(target)}`);
}

async function waitForReadiness(page, readiness) {
  if (readiness.type === 'testId') {
    await page.getByTestId(readiness.value).waitFor({ state: 'visible', timeout: 5000 }).catch(() => {});
    return;
  }
  if (readiness.type === 'stableDOM') {
    await page.waitForTimeout(100);
    const first = JSON.stringify(await domSummary(page));
    await page.waitForTimeout(100);
    const second = JSON.stringify(await domSummary(page));
    if (first !== second) await page.waitForTimeout(100);
  }
  if (readiness.type === 'urlPath') {
    await page.waitForURL((url) => normalizePathname(url.pathname) === normalizePathname(readiness.value), { timeout: 5000 });
  }
}

async function accessibilitySnapshot(page) {
  try {
    return await page.locator('body').ariaSnapshot({ timeout: 1000 });
  }
  catch (error) {
    return `aria snapshot unavailable: ${error.message}`;
  }
}

async function domSummary(page) {
  const summary = await page.evaluate(() => {
    function closestTestId(element) {
      const parent = element.parentElement?.closest?.('[data-testid]');
      return parent ? parent.getAttribute('data-testid') || '' : '';
    }
    return Array.from(document.querySelectorAll('button, input, textarea, select, [role], [data-testid], h1, h2, h3'))
      .slice(0, 180)
      .map((element) => ({
        tag: element.tagName.toLowerCase(),
        role: element.getAttribute('role') || '',
        testId: element.getAttribute('data-testid') || '',
        parentTestId: closestTestId(element),
        ariaLabel: element.getAttribute('aria-label') || '',
        text: (element.textContent || '').replace(/\s+/g, ' ').trim().slice(0, 100),
        disabled: Boolean(element.disabled || element.getAttribute('aria-disabled') === 'true'),
      }));
  }).catch((error) => [{ error: error.message }]);

  return summary.map((item) => item.error ? item : normalizeDOMSummaryItem(item));
}

function factsFromDOMSummary(summary = []) {
  const testIds = new Set(summary.map((item) => item.testId).filter(Boolean));
  const roles = new Set(summary.map((item) => item.role).filter(Boolean));
  return {
    hasFrontendApp: testIds.has('frontend-app'),
    hasChatPage: testIds.has('chat-page'),
    composerVisible: testIds.has('composer-input'),
    chatActionsMenuVisible: testIds.has('chat-actions-menu'),
    projectMenuVisible: roles.has('menu'),
    runtimePanelVisible: testIds.has('runtime-panel'),
    observabilityPageVisible: testIds.has('observability-page'),
    recentLogsVisible: testIds.has('observability-recent-logs'),
    settingsPageVisible: testIds.has('settings-page'),
    settingsVideoNoticeVisible: testIds.has('settings-video-notice'),
  };
}

async function writeStepEvidence(outputDir, step, facts, action) {
  await writeJSON(path.join(outputDir, `step-${String(step).padStart(2, '0')}.json`), {
    facts: compactFacts(facts),
    action,
  });
  await writeFile(path.join(outputDir, `step-${String(step).padStart(2, '0')}-aria.md`), String(facts.accessibilitySnapshot || ''), 'utf8');
  await writeJSON(path.join(outputDir, `step-${String(step).padStart(2, '0')}-dom.json`), facts.domSummary || []);
}

export async function writeFinalEvidence(outputDir, page, steps, consoleMessages, networkRequests, discoveredFlows = [], runtime = {}) {
  await page.screenshot({ path: path.join(outputDir, 'final.png'), fullPage: true });
  const resultError = await writeResultEvidence(outputDir, { success: true, steps, consoleMessages, networkRequests, ...runtime }, discoveredFlows);
  if (resultError) throw resultError;
  await writeDiscoveryReports(outputDir, discoveredFlows);
}

export async function writeFailureEvidence(outputDir, page, steps, consoleMessages, networkRequests, error, discoveredFlows = [], runtime = {}) {
  await page.screenshot({ path: path.join(outputDir, 'failure.png'), fullPage: true }).catch(() => {});
  const payload = {
    success: false,
    error: error.message,
    steps,
    consoleMessages,
    networkRequests,
    ...runtime,
  };
  const resultError = await writeResultEvidence(outputDir, payload, discoveredFlows);
  if (resultError) throw error;
  try {
    await writeDiscoveryReports(outputDir, discoveredFlows);
  }
  catch (reportError) {
    await writeResultEvidence(outputDir, {
      ...payload,
      discoveryReportError: reportError.message,
    }, discoveredFlows);
    throw error;
  }
}

async function writeResultEvidence(outputDir, payload, discoveredFlows) {
  let discovery;
  let discoveryError = null;
  try {
    discovery = summarizeDiscovery({ flows: discoveredFlows });
  }
  catch (error) {
    discovery = { error: error.message };
    discoveryError = error;
  }
  await writeJSON(path.join(outputDir, 'result.json'), { ...payload, discovery });
  return discoveryError;
}

async function writeDiscoveryReports(outputDir, flows) {
  const summary = summarizeDiscovery({ flows });
  await writeJSON(path.join(outputDir, 'business-flow-discovery.json'), { summary, flows });
  await writeFile(path.join(outputDir, 'business-flow-discovery.md'), renderDiscoveryMarkdown({ summary, flows }), 'utf8');
}

async function writeJSON(filePath, value) {
  await writeFile(filePath, `${JSON.stringify(value, null, 2)}\n`, 'utf8');
}

function compactFacts(facts) {
  const { accessibilitySnapshot: _, domSummary: __, ...rest } = facts;
  return rest;
}

function normalizeURL(value) {
  const normalized = normalizeString(value);
  if (!normalized) throw new Error('agentic e2e base URL is required');
  return normalized.endsWith('/') ? normalized : `${normalized}/`;
}

function parseAgenticE2EArgs(argv = []) {
  const parsed = {};
  for (let index = 0; index < argv.length; index += 1) {
    const arg = argv[index];
    if (arg === '--headed') {
      parsed.headed = true;
      continue;
    }
    if (arg === '--mock-wails') {
      parsed.mockWails = true;
      continue;
    }
    const equalIndex = arg.indexOf('=');
    const name = equalIndex === -1 ? arg : arg.slice(0, equalIndex);
    const inlineValue = equalIndex === -1 ? undefined : arg.slice(equalIndex + 1);
    if (name === '--goal') {
      const option = argumentValue(name, inlineValue, argv, index);
      parsed.goal = option.value;
      index = option.index;
      continue;
    }
    if (name === '--composer-text') {
      const option = argumentValue(name, inlineValue, argv, index);
      parsed.composerText = option.value;
      index = option.index;
      continue;
    }
    if (name === '--base-url') {
      const option = argumentValue(name, inlineValue, argv, index);
      parsed.baseURL = option.value;
      index = option.index;
      continue;
    }
    if (name === '--output-dir') {
      const option = argumentValue(name, inlineValue, argv, index);
      parsed.outputDir = option.value;
      index = option.index;
      continue;
    }
    if (name === '--max-steps') {
      const option = argumentValue(name, inlineValue, argv, index);
      parsed.maxSteps = option.value;
      index = option.index;
      continue;
    }
    throw new Error(`unsupported agentic e2e option: ${arg}`);
  }
  return parsed;
}

function argumentValue(name, inlineValue, argv, index) {
  if (inlineValue !== undefined) {
    const normalized = normalizeString(inlineValue);
    if (!normalized) throw new Error(`agentic e2e option ${name} requires a value`);
    return { value: normalized, index };
  }
  const nextValue = argv[index + 1];
  const normalized = normalizeString(nextValue);
  if (!normalized || normalized.startsWith('--')) throw new Error(`agentic e2e option ${name} requires a value`);
  return { value: normalized, index: index + 1 };
}

function normalizeRunID(value) {
  return normalizeString(value).replace(/[^a-zA-Z0-9_.-]+/g, '-').replace(/^-+|-+$/g, '') || 'run';
}

function normalizeString(value) {
  return String(value ?? '').trim();
}

function normalizePathname(value) {
  const normalized = normalizeString(value).replace(/\/+$/g, '');
  return normalized || '/';
}

function assertGoalRuntimeSupported(config) {
  if (config.goal?.requiresMockWails && !config.mockWails) {
    throw new Error(`agentic e2e goal ${config.goal.id} requires --mock-wails`);
  }
  if (config.goal?.requiresSandbox && !config.sandbox) {
    throw new Error(`agentic e2e goal ${config.goal.id} requires sandbox config`);
  }
}

async function assertCapturedRuntimeClean(page, config, pageErrors, networkRequests) {
  if (pageErrors.length > 0) throw new Error(`page errors detected: ${pageErrors.join('; ')}`);
  const networkFailures = capturedNetworkFailures(networkRequests);
  if (networkFailures.length > 0) {
    throw new Error(`network failures detected: ${networkFailures.map((item) => item.failure || `${item.status} ${item.url}`).join('; ')}`);
  }
  if (config.mockWails) {
    assertAgenticE2EMockWailsClean(await readAgenticE2EMockWailsState(page));
  }
}

async function runtimeEvidence(page, config, pageErrors) {
  const evidence = { pageErrors };
  if (config.mockWails || config.goal?.requiresSandbox) {
    evidence.sandbox = await snapshotAgenticE2ESandbox(config);
  }
  if (config.mockWails) {
    evidence.mockWails = await readAgenticE2EMockWailsState(page);
  }
  return evidence;
}

function capturedNetworkFailures(networkRequests) {
  return networkRequests.filter((request) => request.failure || Number(request.status || 0) >= 400);
}

function positiveInt(value, fallback) {
  if (value == null || value === '') return fallback;
  const parsed = Number(value);
  if (!Number.isInteger(parsed) || parsed <= 0) throw new Error(`expected positive integer, got ${value}`);
  return parsed;
}

function truthyEnv(value) {
  return value === '1' || value === 'true' || value === 'yes';
}

function isMain(metaURL, argv1) {
  return argv1 ? path.resolve(fileURLToPath(metaURL)) === path.resolve(argv1) : false;
}

if (isMain(import.meta.url, process.argv[1])) {
  runAgenticE2E(agenticE2EConfig(process.env, repoRootFromScript(), process.argv.slice(2))).then((result) => {
    console.log(`agentic e2e passed: ${result.outputDir}`);
  }).catch((error) => {
    console.error(`agentic e2e failed: ${error.message}`);
    process.exitCode = 1;
  });
}
