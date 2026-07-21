import { describe, expect, it } from 'vitest';
import { decideNextAction } from './agentic-e2e-planner.mjs';

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
			type: 'role',
			role: 'button',
			name: '链路追踪',
		});
	});

	it('returns to the chat route without clicking the new conversation action', () => {
		const action = decideNextAction({
			url: 'http://127.0.0.1:5176/settings',
			hasFrontendApp: true,
			hasChatPage: false,
		});

		expect(action).toEqual(expect.objectContaining({
			type: 'goto',
			path: '/',
			expectRoute: '/',
			reason: expect.stringContaining('without invoking new conversation'),
		}));
	});

	it('finishes only after observability logs are visible', () => {
		expect(decideNextAction({
			url: 'http://127.0.0.1:5176/observability',
			hasFrontendApp: true,
			observabilityPageVisible: true,
			recentLogsVisible: true,
		}, { id: 'observability-latest-logs', composerText: 'probe text' }).type).toBe('done');
	});

	it('queries latest observability logs for the observability goal', () => {
		const action = decideNextAction({
			url: 'http://127.0.0.1:5176/observability',
			hasFrontendApp: true,
			observabilityPageVisible: true,
			recentLogsVisible: false,
		}, { id: 'observability-latest-logs' });

		expect(action).toEqual(expect.objectContaining({
			type: 'click',
			target: { type: 'role', role: 'button', name: '查询最新日志' },
		}));
	});

	it('opens stable business navigation goals without filling the composer', () => {
		const action = decideNextAction({
			url: 'http://127.0.0.1:5176/',
			hasFrontendApp: true,
			hasChatPage: true,
			composerVisible: true,
			composerValue: '',
		}, { id: 'plugins-skills-open' });

		expect(action).toEqual(expect.objectContaining({
			type: 'click',
			expectRoute: '/skills',
			target: {
				type: 'role',
				role: 'button',
				name: '插件与技能',
			},
		}));
	});

	it('finishes a stable open goal once its route is active', () => {
		expect(decideNextAction({
			url: 'http://127.0.0.1:5176/settings',
			hasFrontendApp: true,
		}, { id: 'settings-open' })).toEqual(expect.objectContaining({
			type: 'done',
			reason: expect.stringContaining('settings-open'),
		}));
	});

	it('finishes chat-composer after the composer text is filled', () => {
		expect(decideNextAction({
			url: 'http://127.0.0.1:5176/',
			hasFrontendApp: true,
			hasChatPage: true,
			composerVisible: true,
			composerValue: 'probe text',
		}, { id: 'chat-composer', composerText: 'probe text' })).toEqual(expect.objectContaining({
			type: 'done',
			reason: expect.stringContaining('chat-composer'),
		}));
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

	it('clicks the real send button for the mocked chat send danger goal', () => {
		const action = decideNextAction({
			url: 'http://127.0.0.1:5176/',
			hasFrontendApp: true,
			hasChatPage: true,
			composerVisible: true,
			composerValue: 'probe text',
			mockWailsCallMethods: [],
		}, { id: 'chat-send-mocked', composerText: 'probe text' });

		expect(action).toEqual(expect.objectContaining({
			type: 'click',
			target: { type: 'role', role: 'button', name: '发送消息', exact: true },
		}));
	});

	it('finishes the mocked chat send goal only after thread and turn RPCs are observed', () => {
		expect(decideNextAction({
			url: 'http://127.0.0.1:5176/',
			hasFrontendApp: true,
			hasChatPage: true,
			composerVisible: true,
			composerValue: '',
			mockWailsCallMethods: ['thread/start', 'turn/start'],
		}, { id: 'chat-send-mocked', composerText: 'probe text' })).toEqual(expect.objectContaining({
			type: 'done',
			reason: expect.stringContaining('chat-send-mocked'),
		}));
	});

	it('drives the project picker through the real sidebar add-project button', () => {
		const addProject = decideNextAction({
			url: 'http://127.0.0.1:5176/',
			hasFrontendApp: true,
			hasChatPage: true,
			mockWailsCallMethods: [],
		}, { id: 'project-add-sandbox' });
		expect(addProject).toEqual(expect.objectContaining({
			type: 'click',
			target: { type: 'nestedRole', parentTestId: 'app-sidebar', role: 'button', name: '添加项目目录' },
		}));
	});

	it('finishes the sandbox project add goal after selection and project RPCs', () => {
		expect(decideNextAction({
			url: 'http://127.0.0.1:5176/',
			hasFrontendApp: true,
			hasChatPage: true,
			mockWailsCallMethods: ['ui/selectProjectDir', 'ui/projects/add'],
		}, { id: 'project-add-sandbox' })).toEqual(expect.objectContaining({
			type: 'done',
			reason: expect.stringContaining('project-add-sandbox'),
		}));
	});

	it('clicks the real composer add-file action and waits for the file picker RPC', () => {
		expect(decideNextAction({
			url: 'http://127.0.0.1:5176/',
			hasFrontendApp: true,
			hasChatPage: true,
			mockWailsCallMethods: [],
		}, { id: 'file-attach-sandbox' })).toEqual(expect.objectContaining({
			type: 'click',
			target: { type: 'role', role: 'button', name: '添加文件', exact: true },
		}));

		expect(decideNextAction({
			url: 'http://127.0.0.1:5176/',
			hasFrontendApp: true,
			hasChatPage: true,
			attachmentCount: 1,
			mockWailsCallMethods: ['ui/selectFiles'],
		}, { id: 'file-attach-sandbox' })).toEqual(expect.objectContaining({
			type: 'done',
			reason: expect.stringContaining('file-attach-sandbox'),
		}));
	});

	it('saves the settings video key through the real settings form', () => {
		expect(decideNextAction({
			url: 'http://127.0.0.1:5176/settings',
			hasFrontendApp: true,
			settingsPageVisible: true,
			settingsApiKeyVisible: true,
			settingsApiKeyValue: '',
		}, { id: 'settings-video-key-save-mocked' })).toEqual(expect.objectContaining({
			type: 'fill',
			target: { type: 'css', value: '#settings-sf-key' },
			value: 'agentic-e2e-video-key',
		}));

		expect(decideNextAction({
			url: 'http://127.0.0.1:5176/settings',
			hasFrontendApp: true,
			settingsPageVisible: true,
			settingsApiKeyVisible: true,
			settingsApiKeyValue: 'agentic-e2e-video-key',
			mockWailsCallMethods: [],
		}, { id: 'settings-video-key-save-mocked' })).toEqual(expect.objectContaining({
			type: 'click',
			target: { type: 'nestedRole', parentTestId: 'settings-video-card', role: 'button', name: '保存' },
		}));

		expect(decideNextAction({
			url: 'http://127.0.0.1:5176/settings',
			hasFrontendApp: true,
			settingsPageVisible: true,
			mockWailsCallMethods: ['ui/video/setApiKey'],
		}, { id: 'settings-video-key-save-mocked' })).toEqual(expect.objectContaining({
			type: 'done',
			reason: expect.stringContaining('settings-video-key-save-mocked'),
		}));
	});

	it('saves provider settings through the real settings form', () => {
		const providerGoal = {
			id: 'settings-provider-save-mocked',
			modelValue: 'gpt-5',
			effortValue: 'high',
			personalityValue: 'friendly',
			codexHomeValue: '/tmp/agentic-e2e/home/.codex',
			writableRootsValue: '/tmp/agentic-e2e/project',
		};
		const matchingFacts = {
			url: 'http://127.0.0.1:5176/settings',
			hasFrontendApp: true,
			settingsPageVisible: true,
			settingsProviderSaveVisible: true,
			settingsProviderModelValue: 'gpt-5',
			settingsProviderEffortValue: 'high',
			settingsProviderPersonalityValue: 'friendly',
			settingsProviderCodexHomeValue: '/tmp/agentic-e2e/home/.codex',
			settingsProviderInstanceKeyValue: 'agentic-e2e',
			settingsProviderWritableRootsValue: '/tmp/agentic-e2e/project',
			mockWailsCallMethods: [],
		};

		expect(decideNextAction({
			...matchingFacts,
			settingsProviderSaveVisible: false,
		}, providerGoal)).toEqual(expect.objectContaining({
			type: 'fail',
			reason: expect.stringContaining('Provider settings save button is not visible'),
		}));

		expect(decideNextAction({
			...matchingFacts,
			settingsProviderModelValue: 'gpt-5.5',
		}, providerGoal)).toEqual(expect.objectContaining({
			type: 'select',
			target: { type: 'testId', value: 'settings-provider-model' },
			value: 'gpt-5',
		}));

		expect(decideNextAction({
			...matchingFacts,
			settingsProviderEffortValue: 'xhigh',
		}, providerGoal)).toEqual(expect.objectContaining({
			type: 'select',
			target: { type: 'testId', value: 'settings-provider-effort' },
			value: 'high',
		}));

		expect(decideNextAction({
			...matchingFacts,
			settingsProviderPersonalityValue: 'pragmatic',
		}, providerGoal)).toEqual(expect.objectContaining({
			type: 'select',
			target: { type: 'testId', value: 'settings-provider-personality' },
			value: 'friendly',
		}));

		expect(decideNextAction({
			...matchingFacts,
			settingsProviderCodexHomeValue: '',
		}, providerGoal)).toEqual(expect.objectContaining({
			type: 'fill',
			target: { type: 'testId', value: 'settings-provider-codex-home' },
			value: '/tmp/agentic-e2e/home/.codex',
		}));

		expect(decideNextAction({
			...matchingFacts,
			settingsProviderInstanceKeyValue: '',
		}, providerGoal)).toEqual(expect.objectContaining({
			type: 'fill',
			target: { type: 'testId', value: 'settings-provider-instance-key' },
			value: 'agentic-e2e',
		}));

		expect(decideNextAction({
			...matchingFacts,
			settingsProviderWritableRootsValue: '',
		}, providerGoal)).toEqual(expect.objectContaining({
			type: 'fill',
			target: { type: 'testId', value: 'settings-provider-writable-roots' },
			value: '/tmp/agentic-e2e/project',
		}));

		expect(decideNextAction(matchingFacts, providerGoal)).toEqual(expect.objectContaining({
			type: 'click',
			target: { type: 'testId', value: 'settings-provider-save-button' },
		}));

		expect(decideNextAction({
			url: 'http://127.0.0.1:5176/settings',
			hasFrontendApp: true,
			mockWailsCallMethods: ['ui/preferences/set'],
		}, providerGoal)).toEqual(expect.objectContaining({
			type: 'done',
			reason: expect.stringContaining('settings-provider-save-mocked'),
		}));
	});
});
