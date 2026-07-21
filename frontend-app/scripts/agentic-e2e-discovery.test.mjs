import { describe, expect, it } from 'vitest';

import {
	BLOCKED_ACTION_KEYWORDS,
	businessActionsFromDOMSummary,
	discoverBusinessFlows,
	safetyForAction,
} from './agentic-e2e-discovery.mjs';

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
		expect(flows).toEqual(expect.arrayContaining([
			expect.objectContaining({ entry: expect.objectContaining({ label: '插件与技能', targetRoute: '/skills' }) }),
			expect.objectContaining({ entry: expect.objectContaining({ label: '设置', targetRoute: '/settings' }) }),
			expect.objectContaining({ entry: expect.objectContaining({ label: '链路追踪', targetRoute: '/observability' }) }),
		]));
	});

	it('marks known navigation buttons as safe route changes without allowing shell controls', () => {
		const actions = businessActionsFromDOMSummary([
			{ tag: 'button', role: '', testId: '', ariaLabel: '插件与技能', text: '插件', disabled: false, sourceTestId: 'sidebar-nav' },
			{ tag: 'button', role: '', testId: '', ariaLabel: '自动化', text: '自动化', disabled: false, sourceTestId: 'sidebar-nav' },
			{ tag: 'button', role: '', testId: '', ariaLabel: '设置', text: '设置', disabled: false, sourceTestId: 'app-sidebar' },
			{ tag: 'button', role: '', testId: '', ariaLabel: '新对话', text: '', disabled: false, sourceTestId: 'app-sidebar' },
			{ tag: 'button', role: '', testId: '', ariaLabel: '切换到 English', text: '', disabled: false, sourceTestId: 'app-sidebar' },
			{ tag: 'button', role: '', testId: '', ariaLabel: '发送消息', text: '', disabled: false, sourceTestId: 'sidebar-nav' },
		]);

		expect(actions).toEqual(expect.arrayContaining([
			expect.objectContaining({ label: '插件与技能', safety: 'allowed', reason: 'navigation entry', targetRoute: '/skills' }),
			expect.objectContaining({ label: '自动化', safety: 'allowed', reason: 'navigation entry', targetRoute: '/dags' }),
			expect.objectContaining({ label: '设置', safety: 'allowed', reason: 'navigation entry', targetRoute: '/settings' }),
			expect.objectContaining({ label: '新对话', safety: 'blocked', reason: 'action is not recognized as read-only', targetRoute: '/' }),
			expect.objectContaining({ label: '切换到 English', safety: 'blocked', reason: 'action is not recognized as read-only' }),
			expect.objectContaining({ label: '发送消息', safety: 'blocked', reason: 'mutating or provider action keyword: 发送' }),
		]));
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
