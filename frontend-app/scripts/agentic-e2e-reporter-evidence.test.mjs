import { describe, expect, it } from 'vitest';
import { mkdir, mkdtemp, readFile, rm } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import path from 'node:path';

import { mergeDiscoveredFlows, readinessForAction, writeFailureEvidence, writeFinalEvidence } from './agentic-e2e.mjs';
import { renderDiscoveryMarkdown, summarizeDiscovery } from './agentic-e2e-reporter.mjs';

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

	it('uses stable DOM readiness after provider settings save', () => {
		expect(readinessForAction({
			type: 'click',
			target: { type: 'testId', value: 'settings-provider-save-button' },
		})).toEqual({ type: 'stableDOM' });
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

	it('keeps target route separate from the latest observed page when merging repeated flows', () => {
		const merged = mergeDiscoveredFlows([
			{
				id: 'visible-sidebar-nav-插件与技能',
				entry: { route: '/', label: '插件与技能', source: 'sidebar-nav', targetRoute: '/skills' },
				page: { route: '/' },
				actions: [{ type: 'click', label: '插件与技能', safety: 'allowed', reason: 'navigation entry' }],
			},
		], [
			{
				id: 'visible-sidebar-nav-插件与技能',
				entry: { route: '/observability', label: '插件与技能', source: 'sidebar-nav', targetRoute: '/skills' },
				page: { route: '/observability' },
				actions: [{ type: 'click', label: '查询最新日志', safety: 'allowed', reason: 'read-oriented action keyword: 查询' }],
			},
		]);

		expect(merged).toHaveLength(1);
		expect(merged[0].entry).toEqual(expect.objectContaining({
			route: '/',
			targetRoute: '/skills',
		}));
		expect(merged[0].page.route).toBe('/observability');
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
		const page = { screenshot: async () => { } };
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
		const page = { screenshot: async () => { } };
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
		const page = { screenshot: async () => { } };
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
		const page = { screenshot: async () => { } };
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
				uniqueAllowedActions: 1,
				uniqueBlockedActions: 0,
				reviewReadyFlows: 1,
				stableGoalCandidates: 0,
				shellControlFlows: 0,
				contextualFlows: 0,
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
			uniqueAllowedActions: 0,
			uniqueBlockedActions: 0,
			reviewReadyFlows: 0,
			stableGoalCandidates: 0,
			shellControlFlows: 0,
			contextualFlows: 0,
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
		expect(summary.uniqueAllowedActions).toBe(1);
		expect(summary.uniqueBlockedActions).toBe(1);
		expect(summary.reviewReadyFlows).toBe(1);
		expect(summary.stableGoalCandidates).toBe(1);
	});

	it('separates review-ready entries from shell and contextual controls', () => {
		const summary = summarizeDiscovery({
			flows: [
				{
					id: 'visible-sidebar-secondary-nav-链路追踪',
					entry: { route: '/', label: '链路追踪', source: 'sidebar-secondary-nav' },
					actions: [],
				},
				{
					id: 'visible-app-sidebar-切换到-english',
					entry: { route: '/', label: '切换到 English', source: 'app-sidebar' },
					actions: [],
				},
				{
					id: 'visible-app-sidebar-新对话-agentic-e2e-harness',
					entry: { route: '/', label: '新对话 agentic-e2e-harness', source: 'app-sidebar' },
					actions: [],
				},
			],
		});

		expect(summary.reviewReadyFlows).toBe(1);
		expect(summary.stableGoalCandidates).toBe(1);
		expect(summary.shellControlFlows).toBe(1);
		expect(summary.contextualFlows).toBe(1);
	});

	it('deduplicates dynamic trace actions in discovery summaries', () => {
		const summary = summarizeDiscovery({
			flows: [{
				id: 'visible-sidebar-secondary-nav-链路追踪',
				entry: { route: '/', label: '链路追踪', source: 'sidebar-secondary-nav' },
				actions: [
					{ type: 'click', label: '打开 Trace 8a7a506fc528263963f9234b0f45d581', safety: 'allowed', reason: 'read-oriented action keyword: 打开' },
					{ type: 'click', label: '打开 Trace f4af9f8d6d21b49ed9cf0a62cf9d2c81', safety: 'allowed', reason: 'read-oriented action keyword: 打开' },
				],
			}],
		});

		expect(summary.allowedActions).toBe(2);
		expect(summary.uniqueAllowedActions).toBe(1);
	});

	it('renders a human-readable markdown report', () => {
		const markdown = renderDiscoveryMarkdown({
			flows: [{
				id: 'visible-sidebar-secondary-nav-链路追踪',
				entry: { route: '/', label: '链路追踪', source: 'sidebar-secondary-nav', targetRoute: '/observability' },
				page: { route: '/observability', heading: '链路追踪', testIds: ['observability-page'] },
				actions: [{ type: 'click', label: '查询最新日志', safety: 'allowed', reason: 'read-oriented action keyword: 查询' }],
				result: { status: 'discovered', summary: 'Recent log table became visible' },
			}, {
				id: 'visible-sidebar-nav-自动化',
				entry: { route: '/', label: '自动化', source: 'sidebar-nav', targetRoute: '/dags' },
				page: { route: '/observability', heading: '链路追踪', testIds: ['observability-page'] },
				actions: [{ type: 'click', label: '自动化', safety: 'allowed', reason: 'navigation entry', targetRoute: '/dags' }],
				result: { status: 'candidate', summary: 'Discovered from visible page structure' },
			}, {
				id: 'visible-sidebar-nav-插件与技能',
				entry: { route: '/', label: '插件与技能', source: 'sidebar-nav', targetRoute: '/skills' },
				page: { route: '/observability', heading: '链路追踪', testIds: ['observability-page'] },
				actions: [{ type: 'click', label: '插件与技能', safety: 'allowed', reason: 'navigation entry', targetRoute: '/skills' }],
				result: { status: 'candidate', summary: 'Discovered from visible page structure' },
			}, {
				id: 'visible-sidebar-nav-提示词',
				entry: { route: '/', label: '提示词', source: 'sidebar-nav', targetRoute: '/prompts' },
				page: { route: '/observability', heading: '链路追踪', testIds: ['observability-page'] },
				actions: [{ type: 'click', label: '提示词', safety: 'allowed', reason: 'navigation entry', targetRoute: '/prompts' }],
				result: { status: 'candidate', summary: 'Discovered from visible page structure' },
			}, {
				id: 'visible-sidebar-nav-共享文件',
				entry: { route: '/', label: '共享文件', source: 'sidebar-nav', targetRoute: '/files' },
				page: { route: '/observability', heading: '链路追踪', testIds: ['observability-page'] },
				actions: [{ type: 'click', label: '共享文件', safety: 'allowed', reason: 'navigation entry', targetRoute: '/files' }],
				result: { status: 'candidate', summary: 'Discovered from visible page structure' },
			}],
		});

		expect(markdown).toContain('# Agentic E2E Business Flow Discovery');
		expect(markdown).toContain('- Total flows: 5');
		expect(markdown).toContain('- Stable goal candidates: 5');
		expect(markdown).toContain('| Entry | Category | Source | Target Route | Observed Page | Result | Suggested Goal |');
		expect(markdown).toContain('链路追踪');
		expect(markdown).toContain('| 自动化 | business-candidate | sidebar-nav | /dags | /observability | candidate - Discovered from visible page structure | automation-open |');
		expect(markdown).toContain('| 插件与技能 | business-candidate | sidebar-nav | /skills | /observability | candidate - Discovered from visible page structure | plugins-skills-open |');
		expect(markdown).toContain('| 提示词 | business-candidate | sidebar-nav | /prompts | /observability | candidate - Discovered from visible page structure | prompts-open |');
		expect(markdown).toContain('| 共享文件 | business-candidate | sidebar-nav | /files | /observability | candidate - Discovered from visible page structure | shared-files-open |');
		expect(markdown).toContain('Stable Goal Candidates');
		expect(markdown).toContain('observability-latest-logs');
		expect(markdown).toContain('查询最新日志');
	});

	it('escapes markdown table cells in action labels and reasons', () => {
		const markdown = renderDiscoveryMarkdown({
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

		expect(markdown).toContain('| click | 查询\\|最新 日志 | 1 | read\\|oriented reason |');
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
