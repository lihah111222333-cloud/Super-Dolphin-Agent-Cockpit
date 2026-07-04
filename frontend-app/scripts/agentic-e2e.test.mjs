import { describe, expect, it } from 'vitest';
import { chromium } from 'playwright';
import { mkdir, mkdtemp, readFile, rm } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import path from 'node:path';

import { agenticE2EConfig, collectPageFacts, mergeDiscoveredFlows, normalizeDOMSummaryItem, readinessForAction, writeFailureEvidence, writeFinalEvidence } from './agentic-e2e.mjs';
import {
  BLOCKED_ACTION_KEYWORDS,
  businessActionsFromDOMSummary,
  discoverBusinessFlows,
  safetyForAction,
} from './agentic-e2e-discovery.mjs';
import { decideNextAction, normalizeGoal } from './agentic-e2e-planner.mjs';
import { renderDiscoveryMarkdown, summarizeDiscovery } from './agentic-e2e-reporter.mjs';

describe('agentic e2e planner', () => {
  it('starts by opening the app root when the page is blank', () => {
    expect(decideNextAction({ url: 'about:blank' })).toEqual(expect.objectContaining({
      type: 'goto',
      path: '/',
    }));
  });

  it('fills the composer once the chat page is visible', () => {
    const action = decideNextAction({
      url: 'http://127.0.0.1:5176/',
      hasFrontendApp: true,
      hasChatPage: true,
      composerVisible: true,
      composerValue: '',
    }, { composerText: 'probe text' });

    expect(action).toEqual(expect.objectContaining({
      type: 'fill',
      value: 'probe text',
      target: { type: 'testId', value: 'composer-input' },
    }));
  });

  it('navigates to observability after filling the composer', () => {
    const action = decideNextAction({
      url: 'http://127.0.0.1:5176/',
      hasFrontendApp: true,
      hasChatPage: true,
      composerVisible: true,
      composerValue: 'probe text',
      chatActionsMenuVisible: false,
      runtimePanelVisible: false,
    }, { composerText: 'probe text' });

    expect(action.type).toBe('click');
    expect(action.target).toEqual({
      type: 'nestedRole',
      parentTestId: 'sidebar-secondary-nav',
      role: 'button',
      name: '链路追踪',
    });
  });

  it('finishes only after observability logs are visible', () => {
    expect(decideNextAction({
      url: 'http://127.0.0.1:5176/observability',
      hasFrontendApp: true,
      observabilityPageVisible: true,
      recentLogsVisible: true,
    }, { composerText: 'probe text' }).type).toBe('done');
  });

  it('fails fast on console errors', () => {
    const action = decideNextAction({
      url: 'http://127.0.0.1:5176/',
      hasFrontendApp: true,
      consoleErrors: ['boom'],
    });

    expect(action.type).toBe('fail');
    expect(action.reason).toContain('console errors detected');
  });
});

describe('agentic e2e business discovery', () => {
  it('discovers sidebar entries and safe query actions from DOM summary', () => {
    const flows = discoverBusinessFlows({
      url: 'http://127.0.0.1:5176/',
      title: '燧元',
      domSummary: [
        { tag: 'button', role: '', testId: '', ariaLabel: '链路追踪', text: '', disabled: false, sourceTestId: 'sidebar-secondary-nav' },
        { tag: 'button', role: '', testId: '', ariaLabel: 'Settings', text: '', disabled: false, sourceTestId: 'app-sidebar' },
        { tag: 'button', role: '', testId: '', ariaLabel: '', text: '查询最新日志', disabled: false },
      ],
    });

    expect(flows.map((flow) => flow.entry.label)).toContain('链路追踪');
    expect(flows.map((flow) => flow.entry.label)).toContain('Settings');
    expect(flows.flatMap((flow) => flow.actions).some((action) => action.label === '查询最新日志' && action.safety === 'allowed')).toBe(true);
  });

  it('discovers known navigation entries from baseline DOM summary shape', () => {
    const flows = discoverBusinessFlows({
      url: 'http://127.0.0.1:5176/',
      title: '燧元',
      domSummary: [
        { tag: 'button', role: '', testId: '', ariaLabel: '链路追踪', text: '', disabled: false },
        { tag: 'button', role: '', testId: '', ariaLabel: 'Settings', text: '', disabled: false },
        { tag: 'button', role: '', testId: '', ariaLabel: '', text: '查询最新日志', disabled: false },
      ],
    });

    expect(flows.map((flow) => flow.entry)).toEqual(expect.arrayContaining([
      expect.objectContaining({ label: '链路追踪', source: 'visible-navigation' }),
      expect.objectContaining({ label: 'Settings', source: 'visible-navigation' }),
    ]));
  });

  it('discovers Chinese navigation entries from aria labels and visible text', () => {
    const flows = discoverBusinessFlows({
      url: 'http://127.0.0.1:5176/',
      title: '燧元',
      domSummary: [
        { tag: 'button', role: '', testId: '', ariaLabel: '插件与技能', text: '插件', disabled: false },
        { tag: 'button', role: '', testId: '', ariaLabel: '设置', text: '设置', disabled: false },
        { tag: 'button', role: '', testId: '', ariaLabel: '链路追踪', text: '', disabled: false },
      ],
    });
    const labels = flows.map((flow) => flow.entry.label);

    expect(labels.some((label) => label === '插件与技能' || label === '插件')).toBe(true);
    expect(labels).toContain('设置');
    expect(labels).toContain('链路追踪');
  });

  it('does not emit disabled controls as candidate actions', () => {
    const actions = businessActionsFromDOMSummary([
      { tag: 'button', role: '', testId: '', ariaLabel: '', text: '查询最新日志', disabled: true },
    ]);

    expect(actions.some((action) => action.label === '查询最新日志')).toBe(false);
  });

  it('blocks mutating and provider-turn actions', () => {
    expect(BLOCKED_ACTION_KEYWORDS).toContain('删除');
    expect(safetyForAction({ label: '发送', type: 'click' })).toEqual(expect.objectContaining({
      safety: 'blocked',
      reason: expect.stringContaining('mutating or provider action'),
    }));
    expect(safetyForAction({ label: '查询最新日志', type: 'click' })).toEqual(expect.objectContaining({
      safety: 'allowed',
    }));
  });
});

describe('agentic e2e readiness', () => {
  it('waits for observability page after clicking its sidebar entry', () => {
    expect(readinessForAction({
      type: 'click',
      target: { type: 'nestedRole', parentTestId: 'sidebar-secondary-nav', role: 'button', name: '链路追踪' },
    })).toEqual({ type: 'testId', value: 'observability-page' });
  });

  it('waits for recent logs after querying observability', () => {
    expect(readinessForAction({
      type: 'click',
      target: { type: 'role', role: 'button', name: '查询最新日志' },
    })).toEqual({ type: 'testId', value: 'observability-recent-logs' });
  });
});

describe('agentic e2e discovery aggregation', () => {
  it('merges discovered flows by id without losing blocked actions', () => {
    const merged = mergeDiscoveredFlows([
      { id: 'flow-a', actions: [{ type: 'click', label: '查询', safety: 'allowed', reason: 'read-oriented action keyword: 查询' }] },
    ], [
      { id: 'flow-a', actions: [{ type: 'click', label: '删除', safety: 'blocked', reason: 'mutating or provider action keyword: 删除' }], result: { status: 'candidate', summary: 'second sample' } },
      { id: 'flow-b', actions: [], result: { status: 'candidate', summary: 'new flow' } },
    ]);

    expect(merged).toHaveLength(2);
    expect(merged.find((flow) => flow.id === 'flow-a').actions).toHaveLength(2);
    expect(merged.find((flow) => flow.id === 'flow-b').result.summary).toBe('new flow');
  });

  it('fails fast for discovered flows without non-empty ids', () => {
    expect(() => mergeDiscoveredFlows([{ id: '', actions: [] }], [])).toThrow(/invalid discovered flow/);
    expect(() => mergeDiscoveredFlows([], [{ actions: [] }])).toThrow(/invalid discovered flow/);
  });

  it('fails fast for discovered actions without semantic fields', () => {
    expect(() => mergeDiscoveredFlows([
      { id: 'flow-a', actions: [{ label: '查询', safety: 'allowed' }] },
    ], [])).toThrow(/invalid discovered action/);
  });
});

describe('agentic e2e evidence writing', () => {
  const validDiscoveryFlows = [{
    id: 'flow-a',
    actions: [{ type: 'click', label: '查询', safety: 'allowed', reason: 'read-oriented action keyword: 查询' }],
  }];

  it('preserves the original failure when discovery report validation fails', async () => {
    const outputDir = await mkdtemp(path.join(tmpdir(), 'agentic-e2e-'));
    const page = { screenshot: async () => {} };
    try {
      await expect(writeFailureEvidence(
        outputDir,
        page,
        [],
        [],
        [],
        new Error('original runner failure'),
        [{ id: 'flow-a', actions: [null] }],
      )).rejects.toThrow(/original runner failure/);

      const result = JSON.parse(await readFile(path.join(outputDir, 'result.json'), 'utf8'));
      expect(result.success).toBe(false);
      expect(result.error).toBe('original runner failure');
      expect(result.discovery.error).toMatch(/invalid discovery action/);
    }
    finally {
      await rm(outputDir, { recursive: true, force: true });
    }
  });

  it('records discovery report write errors without replacing the original failure', async () => {
    const outputDir = await mkdtemp(path.join(tmpdir(), 'agentic-e2e-'));
    const page = { screenshot: async () => {} };
    try {
      await mkdir(path.join(outputDir, 'business-flow-discovery.json'));

      await expect(writeFailureEvidence(
        outputDir,
        page,
        [],
        [],
        [],
        new Error('original runner failure'),
        validDiscoveryFlows,
      )).rejects.toThrow(/original runner failure/);

      const result = JSON.parse(await readFile(path.join(outputDir, 'result.json'), 'utf8'));
      expect(result.error).toBe('original runner failure');
      expect(result.discoveryReportError).toMatch(/EISDIR|illegal operation|directory/);
    }
    finally {
      await rm(outputDir, { recursive: true, force: true });
    }
  });

  it('keeps success evidence fail-fast for malformed discovery flows', async () => {
    const outputDir = await mkdtemp(path.join(tmpdir(), 'agentic-e2e-'));
    const page = { screenshot: async () => {} };
    try {
      await expect(writeFinalEvidence(
        outputDir,
        page,
        [],
        [],
        [],
        [{ id: 'flow-a', actions: [null] }],
      )).rejects.toThrow(/invalid discovery action/);
    }
    finally {
      await rm(outputDir, { recursive: true, force: true });
    }
  });

  it('keeps success evidence fail-fast for discovery report write errors after result is written', async () => {
    const outputDir = await mkdtemp(path.join(tmpdir(), 'agentic-e2e-'));
    const page = { screenshot: async () => {} };
    try {
      await mkdir(path.join(outputDir, 'business-flow-discovery.json'));

      await expect(writeFinalEvidence(
        outputDir,
        page,
        [],
        [],
        [],
        validDiscoveryFlows,
      )).rejects.toThrow(/EISDIR|illegal operation on a directory|is a directory/);

      const result = JSON.parse(await readFile(path.join(outputDir, 'result.json'), 'utf8'));
      expect(result.success).toBe(true);
      expect(result.discovery).toEqual({
        totalFlows: 1,
        allowedActions: 1,
        blockedActions: 0,
      });
    }
    finally {
      await rm(outputDir, { recursive: true, force: true });
    }
  });
});

describe('agentic e2e discovery report', () => {
  it('summarizes empty discovery by default', () => {
    expect(summarizeDiscovery()).toEqual({
      totalFlows: 0,
      allowedActions: 0,
      blockedActions: 0,
    });
  });

  it('summarizes discovered, executed, and blocked flows', () => {
    const summary = summarizeDiscovery({
      flows: [{
        id: 'visible-sidebar-secondary-nav-链路追踪',
        entry: { route: '/', label: '链路追踪', source: 'sidebar-secondary-nav' },
        page: { route: '/observability', heading: '链路追踪', testIds: ['observability-page'] },
        actions: [
          { type: 'click', label: '查询最新日志', safety: 'allowed', reason: 'read-oriented action keyword: 查询' },
          { type: 'click', label: '删除日志', safety: 'blocked', reason: 'mutating or provider action keyword: 删除' },
        ],
        result: { status: 'discovered', summary: 'Recent log table became visible' },
      }],
    });

    expect(summary.totalFlows).toBe(1);
    expect(summary.allowedActions).toBe(1);
    expect(summary.blockedActions).toBe(1);
  });

  it('renders a human-readable markdown report', () => {
    const markdown = renderDiscoveryMarkdown({
      summary: { totalFlows: 1, allowedActions: 1, blockedActions: 1 },
      flows: [{
        id: 'visible-sidebar-secondary-nav-链路追踪',
        entry: { route: '/', label: '链路追踪', source: 'sidebar-secondary-nav' },
        page: { route: '/observability', heading: '链路追踪', testIds: ['observability-page'] },
        actions: [{ type: 'click', label: '查询最新日志', safety: 'allowed', reason: 'read-oriented action keyword: 查询' }],
        result: { status: 'discovered', summary: 'Recent log table became visible' },
      }],
    });

    expect(markdown).toContain('# Agentic E2E Business Flow Discovery');
    expect(markdown).toContain('链路追踪');
    expect(markdown).toContain('查询最新日志');
  });

  it('escapes markdown table cells in action labels and reasons', () => {
    const markdown = renderDiscoveryMarkdown({
      summary: { totalFlows: 1, allowedActions: 1, blockedActions: 0 },
      flows: [{
        id: 'x',
        actions: [{
          type: 'click',
          label: '查询|最新\n日志',
          safety: 'allowed',
          reason: 'read|oriented\nreason',
        }],
      }],
    });

    expect(markdown).toContain('| allowed | click | 查询\\|最新 日志 | read\\|oriented reason |');
  });

  it('fails fast with context for malformed discovery flows', () => {
    expect(() => summarizeDiscovery({ flows: [null] })).toThrow(/invalid discovery flow/);
    expect(() => renderDiscoveryMarkdown({ flows: [null] })).toThrow(/invalid discovery flow/);
  });

  it('fails fast with context for malformed discovery actions', () => {
    expect(() => summarizeDiscovery({
      flows: [{ id: 'x', actions: [null] }],
    })).toThrow(/invalid discovery action/);
  });
});

describe('agentic e2e DOM facts', () => {
  it('normalizes discovery fields from DOM summary items', () => {
    expect(normalizeDOMSummaryItem({
      tag: 'button',
      role: '',
      testId: '',
      parentTestId: 'sidebar-secondary-nav',
      ariaLabel: '链路追踪',
      text: '',
      disabled: false,
    })).toEqual({
      tag: 'button',
      role: '',
      testId: '',
      parentTestId: 'sidebar-secondary-nav',
      sourceTestId: 'sidebar-secondary-nav',
      ariaLabel: '链路追踪',
      text: '',
      disabled: false,
    });
  });

  it('collects and normalizes DOM summary items from a browser page', async () => {
    const browser = await chromium.launch({ headless: true });
    try {
      const page = await browser.newPage();
      await page.setContent(`
        <main data-testid="frontend-app">
          <nav data-testid="sidebar-secondary-nav">
            <button aria-label="链路追踪"></button>
          </nav>
          <h1>观测面板</h1>
          <label>
            筛选
            <select aria-label="日志级别">
              <option>info</option>
            </select>
          </label>
        </main>
      `);

      const facts = await collectPageFacts(page, []);
      const button = facts.domSummary.find((item) => item.tag === 'button' && item.ariaLabel === '链路追踪');

      expect(button).toEqual(expect.objectContaining({
        parentTestId: 'sidebar-secondary-nav',
        sourceTestId: 'sidebar-secondary-nav',
      }));
      expect(facts.domSummary).toEqual(expect.arrayContaining([
        expect.objectContaining({ tag: 'h1', text: '观测面板' }),
        expect.objectContaining({ tag: 'select', ariaLabel: '日志级别' }),
      ]));
    }
    finally {
      await browser.close();
    }
  });
});

describe('agentic e2e config', () => {
  it('builds a bounded default config', () => {
    const config = agenticE2EConfig({
      SUPER_DOLPHIN_AGENTIC_E2E_RUN_ID: 'probe run',
    }, '/repo/app');

    expect(config.baseURL).toBe('http://127.0.0.1:5176/');
    expect(config.outputDir).toBe('/repo/app/.tmp/agentic-e2e/probe-run');
    expect(config.maxSteps).toBe(12);
    expect(config.headless).toBe(true);
  });

  it('normalizes explicit goal text', () => {
    expect(normalizeGoal({ id: ' custom ', composerText: ' hello ' })).toEqual({
      id: 'custom',
      composerText: 'hello',
    });
  });
});
