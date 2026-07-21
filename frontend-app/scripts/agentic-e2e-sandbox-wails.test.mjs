import { describe, expect, it } from 'vitest';
import { chromium } from 'playwright';
import { mkdtemp, readFile, rm } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import path from 'node:path';

import { agenticE2EConfig } from './agentic-e2e.mjs';
import { AGENTIC_GOAL_IDS, normalizeGoal } from './agentic-e2e-planner.mjs';
import { prepareAgenticE2ESandbox, snapshotAgenticE2ESandbox } from './agentic-e2e-sandbox.mjs';
import { assertAgenticE2EMockWailsClean, installAgenticE2EMockWails, readAgenticE2EMockWailsState } from './agentic-e2e-wails-mock.mjs';
import { validateRuntimeConfigResponse } from '../src/shared/api/response-validators/core/config.js';
import { validateSidebarStateResponse } from '../src/shared/api/response-validators/runtime/sidebar-state.js';
import { assertPreferenceResponseShape } from '../src/shared/api/preferenceResponseGuards.js';
import { validateShortcutOverrides } from '../src/features/shortcut-settings/model/shortcutSettingsModel.js';

describe('agentic e2e config', () => {
	it('builds a bounded default config', () => {
		const config = agenticE2EConfig({
			SUPER_DOLPHIN_AGENTIC_E2E_RUN_ID: 'probe run',
		}, '/repo/app');

		expect(config.baseURL).toBe('http://127.0.0.1:5176/');
		expect(config.outputDir).toBe('/repo/app/.tmp/agentic-e2e/probe-run/frontend_navigation_probe');
		expect(config.runID).toBe('probe-run');
		expect(config.sandbox).toEqual({
			rootDir: '/repo/app/.tmp/agentic-e2e/sandbox/probe-run',
			homeDir: '/repo/app/.tmp/agentic-e2e/sandbox/probe-run/home',
			projectDir: '/repo/app/.tmp/agentic-e2e/sandbox/probe-run/project',
			uploadFile: '/repo/app/.tmp/agentic-e2e/sandbox/probe-run/project/files/sample.txt',
		});
		expect(config.maxSteps).toBe(12);
		expect(config.headless).toBe(true);
		expect(config.mockWails).toBe(false);
	});

	it('normalizes explicit goal text', () => {
		expect(normalizeGoal({ id: ' chat-composer ', composerText: ' hello ' })).toEqual(expect.objectContaining({
			id: 'chat-composer',
			composerText: 'hello',
		}));
	});

	it('keeps provider goal selection defaults when overrides are blank', () => {
		expect(normalizeGoal({
			id: 'settings-provider-save-mocked',
			modelValue: ' ',
			codexHomeValue: ' /tmp/agentic-e2e/home/.codex ',
		})).toEqual(expect.objectContaining({
			id: 'settings-provider-save-mocked',
			modelValue: 'gpt-5',
			codexHomeValue: '/tmp/agentic-e2e/home/.codex',
			instanceKeyValue: 'agentic-e2e',
		}));
	});

	it('exposes the stable goal runner candidates', () => {
		expect(AGENTIC_GOAL_IDS).toEqual(expect.arrayContaining([
			'chat-composer',
			'observability-latest-logs',
			'plugins-skills-open',
			'automation-open',
			'prompts-open',
			'shared-files-open',
			'memory-open',
			'settings-open',
			'chat-send-mocked',
			'project-add-sandbox',
			'file-attach-sandbox',
			'settings-video-key-save-mocked',
			'settings-provider-save-mocked',
		]));
	});

	it('exposes stable provider settings anchors for dangerous-action e2e', async () => {
		const componentDir = path.join(process.cwd(), 'src/pages/settings/components');
		const [panelSource, formSource] = await Promise.all([
			readFile(path.join(componentDir, 'ProviderSettingsPanels.jsx'), 'utf8'),
			readFile(path.join(componentDir, 'ProviderSettingsForm.jsx'), 'utf8'),
		]);
		const source = `${panelSource}\n${formSource}`;
		expect(source).toContain('data-testid="settings-provider-runtime-card"');
		expect(source).toContain('data-testid="settings-provider-save-button"');
		expect(source).toContain('data-testid="settings-provider-model"');
		expect(source).toContain('data-testid="settings-provider-effort"');
		expect(source).toContain('data-testid="settings-provider-personality"');
		expect(source).toContain('data-testid="settings-provider-codex-home"');
		expect(source).toContain('data-testid="settings-provider-instance-key"');
		expect(source).toContain('data-testid="settings-provider-network-access"');
		expect(source).toContain('data-testid="settings-provider-writable-roots"');
		const writableRootsTextarea = source.match(/<textarea[^>]*data-testid="settings-provider-writable-roots"[^>]*>/)?.[0];
		expect(writableRootsTextarea).toContain('placeholder={copy.provider.rootPlaceholder}');
	});

	it('fails fast for unsupported goals', () => {
		expect(() => normalizeGoal({ id: 'missing-goal' })).toThrow(/unsupported agentic e2e goal/);
	});

	it('uses command line goal selection and goal-scoped output directories', () => {
		const config = agenticE2EConfig({
			SUPER_DOLPHIN_AGENTIC_E2E_RUN_ID: 'probe run',
			SUPER_DOLPHIN_AGENTIC_E2E_GOAL: 'plugins-skills-open',
		}, '/repo/app', ['--goal=memory-open']);

		expect(config.goal.id).toBe('memory-open');
		expect(config.outputDir).toBe('/repo/app/.tmp/agentic-e2e/probe-run/memory-open');
	});

	it('uses sandbox values for provider settings goal selection', () => {
		const config = agenticE2EConfig({
			SUPER_DOLPHIN_AGENTIC_E2E_RUN_ID: 'probe run',
			SUPER_DOLPHIN_AGENTIC_E2E_GOAL: 'settings-provider-save-mocked',
		}, '/repo/app');

		expect(config.goal).toEqual(expect.objectContaining({
			id: 'settings-provider-save-mocked',
			codexHomeValue: '/repo/app/.tmp/agentic-e2e/sandbox/probe-run/home/.codex',
			writableRootsValue: '/repo/app/.tmp/agentic-e2e/sandbox/probe-run/project',
		}));
		expect(config.outputDir).toBe('/repo/app/.tmp/agentic-e2e/probe-run/settings-provider-save-mocked');
	});

	it('enables strict mock Wails from env or command line', () => {
		expect(agenticE2EConfig({
			SUPER_DOLPHIN_AGENTIC_E2E_MOCK_WAILS: '1',
		}, '/repo/app').mockWails).toBe(true);
		expect(agenticE2EConfig({}, '/repo/app', ['--mock-wails']).mockWails).toBe(true);
	});

	it('exposes the opt-in desktop wide e2e script without changing desktop smoke', async () => {
		const pkg = JSON.parse(await readFile(path.join(process.cwd(), 'package.json'), 'utf8'));
		expect(pkg.scripts?.['test:e2e:desktop-wide']).toBe('playwright test --config playwright.desktop-wide.config.js');
		expect(pkg.scripts?.['smoke:desktop:ux']).toBe('node scripts/desktop-ux-smoke.mjs');
	});

	it('keeps desktop wide playwright isolated to its spec and tmp output', async () => {
		const config = await readFile(path.join(process.cwd(), 'playwright.desktop-wide.config.js'), 'utf8');
		expect(config).toContain("testMatch: 'desktop-wide.spec.js'");
		expect(config).toContain("outputDir: '../.tmp/playwright-desktop-wide'");
		expect(config).toContain("name: 'desktop-1440'");
		expect(config).toContain("name: 'desktop-1600'");
	});

	it('fails fast for unsupported command line options', () => {
		expect(() => agenticE2EConfig({}, '/repo/app', ['--unknown'])).toThrow(/unsupported agentic e2e option/);
	});
});

describe('agentic e2e sandbox fixture', () => {
	it('creates a bounded test project and returns a deterministic file snapshot', async () => {
		const repoRoot = await mkdtemp(path.join(tmpdir(), 'agentic-e2e-repo-'));
		try {
			const config = agenticE2EConfig({
				SUPER_DOLPHIN_AGENTIC_E2E_RUN_ID: 'sandbox run',
			}, repoRoot);

			await prepareAgenticE2ESandbox(config);
			const readme = await readFile(path.join(config.sandbox.projectDir, 'README.md'), 'utf8');
			const skill = await readFile(path.join(config.sandbox.projectDir, '.agents/skills/e2e-fixture/SKILL.md'), 'utf8');
			const snapshot = await snapshotAgenticE2ESandbox(config);

			expect(readme).toContain('Agentic E2E Sandbox Project');
			expect(skill).toContain('agentic-e2e-fixture');
			expect(snapshot.files).toEqual(expect.arrayContaining([
				'.agents/skills/e2e-fixture/SKILL.md',
				'README.md',
				'docs/note.md',
				'files/sample.txt',
				'src/app.js',
			]));
		}
		finally {
			await rm(repoRoot, { recursive: true, force: true });
		}
	});
});

describe('agentic e2e strict Wails mock', () => {
	it('responds to known RPCs and records unhandled RPCs', async () => {
		const browser = await chromium.launch({ headless: true });
		try {
			const page = await browser.newPage();
			const sandbox = sandboxFixture('/tmp/agentic-e2e-known');
			await installAgenticE2EMockWails(page, { sandbox });
			await page.goto('data:text/html,<main>mock</main>');

			const known = await callMockWailsRPC(page, 'config/read', {});
			expect(known.result).toEqual(expect.objectContaining({
				cwd: sandbox.projectDir,
				model: 'gpt-5.5',
				approvalPolicy: 'on-failure',
				sandbox: 'workspace-write',
				toolRouting: {
					mode: 'legacy',
					routerModel: '',
					routerProvider: 'openai_compatible',
					routerBaseURL: '',
					routerHasAPIKey: false,
					confidenceThreshold: 0.65,
					timeoutSec: 8,
				},
			}));
			expect(() => validateRuntimeConfigResponse('config/read', known.result)).not.toThrow();

			const sidebar = await callMockWailsRPC(page, 'ui/sidebar/get', { cwd: sandbox.projectDir });
			expect(sidebar.result).toEqual(expect.objectContaining({
				threads: [],
				agents: [],
				recent_turns: [],
				workspace: { runs: [] },
				token_usage: {
					inputTokens: 0,
					outputTokens: 0,
					totalTokens: 0,
					usedTokens: 0,
					contextWindowTokens: 128000,
					usedPercent: 0,
				},
			}));
			expect(() => validateSidebarStateResponse('ui/sidebar/get', sidebar.result)).not.toThrow();

			const shortcuts = await callMockWailsRPC(page, 'ui/preferences/get', {
				cwd: sandbox.projectDir,
				key: 'settings.shortcuts.bindings',
			});
			expect(shortcuts.result).toEqual({});
			expect(() => validateShortcutOverrides({ registry: [], overrides: shortcuts.result, platform: 'darwin' })).not.toThrow();

			const preferenceValues = Object.fromEntries(await Promise.all([
				'settings.provider.active',
				'settings.provider.codex.effort',
				'settings.provider.codex.sandbox',
				'settings.provider.codex.approvalPolicy',
				'settings.provider.codex.personality',
				'settings.provider.codex.summary',
				'settings.provider.codex.model',
			].map(async (key) => {
				const response = await callMockWailsRPC(page, 'ui/preferences/get', { cwd: sandbox.projectDir, key });
				return [key, response.result];
			})));
			expect(preferenceValues).toEqual({
				'settings.provider.active': 'codex',
				'settings.provider.codex.effort': 'high',
				'settings.provider.codex.sandbox': 'workspace-write',
				'settings.provider.codex.approvalPolicy': 'on-failure',
				'settings.provider.codex.personality': 'none',
				'settings.provider.codex.summary': 'auto',
				'settings.provider.codex.model': 'gpt-5.5',
			});
			for (const [key, value] of Object.entries(preferenceValues)) {
				expect(() => assertPreferenceResponseShape(key, value)).not.toThrow();
			}

			const unknown = await callMockWailsRPC(page, 'missing/method', {});
			expect(unknown.error.message).toMatch(/unhandled agentic e2e mock RPC/);

			const state = await readAgenticE2EMockWailsState(page);
			expect(state.calls.map((call) => call.method)).toEqual([
				'config/read',
				'ui/sidebar/get',
				'ui/preferences/get',
				...Array(7).fill('ui/preferences/get'),
				'missing/method',
			]);
			expect(state.unhandledRPC).toEqual(['missing/method']);
		}
		finally {
			await browser.close();
		}
	});

	it('returns sandbox paths for project and file pickers and fails on out-of-sandbox project paths', async () => {
		const browser = await chromium.launch({ headless: true });
		try {
			const page = await browser.newPage();
			const sandbox = sandboxFixture('/tmp/agentic-e2e-boundary');
			await installAgenticE2EMockWails(page, { sandbox });
			await page.goto('data:text/html,<main>mock</main>');

			await expect(callMockWailsRPC(page, 'ui/selectProjectDir', { defaultPath: sandbox.projectDir }))
				.resolves.toEqual(expect.objectContaining({ result: { path: sandbox.projectDir } }));
			await expect(callMockWailsRPC(page, 'ui/selectFiles', {}))
				.resolves.toEqual(expect.objectContaining({ result: { paths: [sandbox.uploadFile] } }));
			const outside = await callMockWailsRPC(page, 'ui/projects/add', { cwd: sandbox.projectDir, path: '/tmp/outside-project' });
			expect(outside.error.message).toMatch(/outside sandbox/);

			const state = await readAgenticE2EMockWailsState(page);
			expect(state.sandboxViolations).toEqual([expect.objectContaining({
				method: 'ui/projects/add',
				path: 'outside',
			})]);
			expect(() => assertAgenticE2EMockWailsClean(state)).toThrow(/outside sandbox/);
		}
		finally {
			await browser.close();
		}
	});

	it('records sandbox-scoped provider preference writes without leaking raw paths', async () => {
		const browser = await chromium.launch({ headless: true });
		try {
			const page = await browser.newPage();
			const sandbox = sandboxFixture('/tmp/agentic-e2e-preferences');
			await installAgenticE2EMockWails(page, { sandbox });
			await page.goto('data:text/html,<main>mock</main>');

			await expect(callMockWailsRPC(page, 'ui/preferences/set', {
				cwd: sandbox.projectDir,
				key: 'settings.provider.codex.codexHome',
				value: `${sandbox.homeDir}/.codex`,
			})).resolves.toEqual(expect.objectContaining({ result: { ok: true } }));
			await expect(callMockWailsRPC(page, 'ui/preferences/set', {
				cwd: sandbox.projectDir,
				key: 'settings.provider.codex.sandbox',
				value: { type: 'workspaceWrite', writableRoots: [sandbox.projectDir], networkAccess: false },
			})).resolves.toEqual(expect.objectContaining({ result: { ok: true } }));
			await expect(callMockWailsRPC(page, 'ui/preferences/set', {
				cwd: sandbox.projectDir,
				key: 'settings.provider.codex.sandbox',
				value: { type: 'readOnly', access: { readableRoots: [sandbox.projectDir] }, readOnlyMode: 'restricted' },
			})).resolves.toEqual(expect.objectContaining({ result: { ok: true } }));

			const state = await readAgenticE2EMockWailsState(page);
			const preferenceCalls = state.calls.filter((call) => call.method === 'ui/preferences/set');
			expect(preferenceCalls).toEqual([
				expect.objectContaining({
					params: expect.objectContaining({
						cwd: 'sandbox',
						key: 'settings.provider.codex.codexHome',
						valueType: 'path',
						path: 'sandbox',
					}),
				}),
				expect.objectContaining({
					params: expect.objectContaining({
						cwd: 'sandbox',
						key: 'settings.provider.codex.sandbox',
						valueType: 'object',
						sandboxPolicy: 'workspaceWrite',
						writableRoots: ['sandbox'],
						networkAccess: false,
					}),
				}),
				expect.objectContaining({
					params: expect.objectContaining({
						cwd: 'sandbox',
						key: 'settings.provider.codex.sandbox',
						valueType: 'object',
						sandboxPolicy: 'readOnly',
						readableRoots: ['sandbox'],
						readOnlyMode: 'restricted',
					}),
				}),
			]);
			expect(state.settingsWrites).toEqual([
				expect.objectContaining({
					method: 'ui/preferences/set',
					key: 'settings.provider.codex.codexHome',
					cwd: 'sandbox',
					valueType: 'path',
					path: 'sandbox',
				}),
				expect.objectContaining({
					method: 'ui/preferences/set',
					key: 'settings.provider.codex.sandbox',
					cwd: 'sandbox',
					valueType: 'object',
					sandboxPolicy: 'workspaceWrite',
					writableRoots: ['sandbox'],
					networkAccess: false,
				}),
				expect.objectContaining({
					method: 'ui/preferences/set',
					key: 'settings.provider.codex.sandbox',
					cwd: 'sandbox',
					valueType: 'object',
					sandboxPolicy: 'readOnly',
					readableRoots: ['sandbox'],
					readOnlyMode: 'restricted',
				}),
			]);
			const serializedState = JSON.stringify(state);
			expect(serializedState).not.toContain(sandbox.rootDir);
			expect(() => assertAgenticE2EMockWailsClean(state)).not.toThrow();
		}
		finally {
			await browser.close();
		}
	});

	it('accepts runtime trace metadata on provider preference writes without recording trace values', async () => {
		const browser = await chromium.launch({ headless: true });
		try {
			const page = await browser.newPage();
			const sandbox = sandboxFixture('/tmp/agentic-e2e-preference-trace');
			const traceID = '4bf92f3577b34da6a3ce929d0e0e4736';
			const spanID = '00f067aa0ba902b7';
			await installAgenticE2EMockWails(page, { sandbox });
			await page.goto('data:text/html,<main>mock</main>');

			await expect(callMockWailsRPC(page, 'ui/preferences/set', {
				cwd: sandbox.projectDir,
				key: 'settings.provider.codex.model',
				value: 'gpt-5',
				_aoTraceparent: `00-${traceID}-${spanID}-01`,
				_aoTraceId: traceID,
				_aoSpanId: spanID,
			})).resolves.toEqual(expect.objectContaining({ result: { ok: true } }));

			const state = await readAgenticE2EMockWailsState(page);
			expect(state.failures).toEqual([]);
			expect(state.settingsWrites).toEqual([
				expect.objectContaining({
					method: 'ui/preferences/set',
					key: 'settings.provider.codex.model',
					cwd: 'sandbox',
					valueType: 'string',
					value: 'gpt-5',
				}),
			]);
			expect(state.calls.at(-1)?.params).toEqual({
				cwd: 'sandbox',
				key: 'settings.provider.codex.model',
				valueType: 'string',
			});
			expect(JSON.stringify(state)).not.toContain(traceID);
			expect(JSON.stringify(state)).not.toContain(spanID);
			expect(() => assertAgenticE2EMockWailsClean(state)).not.toThrow();
		}
		finally {
			await browser.close();
		}
	});

	it('fails provider preference writes for non-whitelisted keys and out-of-sandbox paths', async () => {
		const browser = await chromium.launch({ headless: true });
		try {
			const page = await browser.newPage();
			const sandbox = sandboxFixture('/tmp/agentic-e2e-preference-guard');
			await installAgenticE2EMockWails(page, { sandbox });
			await page.goto('data:text/html,<main>mock</main>');

			const unsupported = await callMockWailsRPC(page, 'ui/preferences/set', {
				cwd: sandbox.projectDir,
				key: 'settings.provider.codex.secretKey',
				value: 'sk-live-secret',
			});
			expect(unsupported.error.message).toMatch(/unsupported settings preference key/);

			const escaped = await callMockWailsRPC(page, 'ui/preferences/set', {
				cwd: sandbox.projectDir,
				key: 'settings.provider.codex.codexHome',
				value: '/home/l4place/.codex',
			});
			expect(escaped.error.message).toMatch(/outside sandbox/);

			const unexpected = await callMockWailsRPC(page, 'ui/preferences/set', {
				cwd: sandbox.projectDir,
				key: 'settings.provider.codex.model',
				value: 'gpt-5',
				secret: 'sk-live-secret',
			});
			expect(unexpected.error.message).toMatch(/unsupported preference payload field/);

			const nestedEscaped = await callMockWailsRPC(page, 'ui/preferences/set', {
				cwd: sandbox.projectDir,
				key: 'settings.provider.codex.sandbox',
				value: { type: 'readOnly', access: { readableRoots: ['/home/l4place/shared'] } },
			});
			expect(nestedEscaped.error.message).toMatch(/outside sandbox/);

			const traversal = await callMockWailsRPC(page, 'ui/preferences/set', {
				cwd: sandbox.projectDir,
				key: 'settings.provider.codex.codexHome',
				value: `${sandbox.rootDir}/../outside/.codex`,
			});
			expect(traversal.error.message).toMatch(/outside sandbox/);

			const unknownNested = await callMockWailsRPC(page, 'ui/preferences/set', {
				cwd: sandbox.projectDir,
				key: 'settings.provider.codex.sandbox',
				value: { type: 'readOnly', access: { readableRoots: [sandbox.projectDir], secret: 'sk-live-secret' } },
			});
			expect(unknownNested.error.message).toMatch(/unsupported sandbox access field/);

			const pathKey = await callMockWailsRPC(page, 'ui/preferences/set', {
				cwd: sandbox.projectDir,
				key: '/home/l4place',
				value: 'gpt-5',
			});
			expect(pathKey.error.message).toBe('ui/preferences/set unsupported settings preference key');

			const secretKey = await callMockWailsRPC(page, 'ui/preferences/set', {
				cwd: sandbox.projectDir,
				key: 'sk-live-secret',
				value: 'gpt-5',
			});
			expect(secretKey.error.message).toBe('ui/preferences/set unsupported settings preference key');

			const scalarPath = await callMockWailsRPC(page, 'ui/preferences/set', {
				cwd: sandbox.projectDir,
				key: 'settings.provider.codex.model',
				value: '/home/l4place',
			});
			expect(scalarPath.error.message).toBe('ui/preferences/set sensitive preference value must not be recorded');

			const secretReadOnlyMode = await callMockWailsRPC(page, 'ui/preferences/set', {
				cwd: sandbox.projectDir,
				key: 'settings.provider.codex.sandbox',
				value: { type: 'readOnly', access: { readableRoots: [sandbox.projectDir] }, readOnlyMode: 'sk-live-secret' },
			});
			expect(secretReadOnlyMode.error.message).toBe('ui/preferences/set unsupported read-only mode');

			const invalidPolicy = await callMockWailsRPC(page, 'ui/preferences/set', {
				cwd: sandbox.projectDir,
				key: 'settings.provider.codex.sandbox',
				value: { type: 'sk-live-secret', access: { readableRoots: [sandbox.projectDir] } },
			});
			expect(invalidPolicy.error.message).toBe('ui/preferences/set unsupported sandbox policy');

			const state = await readAgenticE2EMockWailsState(page);
			expect(state.failures.map((failure) => failure.method)).toEqual([
				'ui/preferences/set',
				'ui/preferences/set',
				'ui/preferences/set',
				'ui/preferences/set',
				'ui/preferences/set',
				'ui/preferences/set',
				'ui/preferences/set',
				'ui/preferences/set',
				'ui/preferences/set',
				'ui/preferences/set',
				'ui/preferences/set',
			]);
			expect(state.calls.filter((call) => call.method === 'ui/preferences/set').slice(-5)).toEqual([
				expect.objectContaining({
					params: expect.objectContaining({ keyType: 'unsupported', valueType: 'string' }),
				}),
				expect.objectContaining({
					params: expect.objectContaining({ keyType: 'unsupported', valueType: 'string' }),
				}),
				expect.objectContaining({
					params: expect.objectContaining({ key: 'settings.provider.codex.model', valueType: 'string' }),
				}),
				expect.objectContaining({
					params: expect.objectContaining({
						key: 'settings.provider.codex.sandbox',
						sandboxPolicy: 'readOnly',
						readOnlyMode: 'unsupported',
					}),
				}),
				expect.objectContaining({
					params: expect.objectContaining({
						key: 'settings.provider.codex.sandbox',
						sandboxPolicy: 'unsupported',
						readOnlyMode: '',
					}),
				}),
			]);
			const serializedState = JSON.stringify(state);
			expect(serializedState).not.toContain(sandbox.rootDir);
			expect(serializedState).not.toContain('/home/l4place');
			expect(serializedState).not.toContain('sk-live-secret');
			expect(() => assertAgenticE2EMockWailsClean(state)).toThrow(/ui\/preferences\/set/);
		}
		finally {
			await browser.close();
		}
	});
});

async function callMockWailsRPC(page, method, params) {
	return page.evaluate(({ method: rpcMethod, params: rpcParams }) => new Promise((resolve, reject) => {
		const socket = new WebSocket('ws://127.0.0.1/wails/ws');
		socket.onerror = () => reject(new Error('mock socket failed'));
		socket.onopen = () => {
			socket.send(JSON.stringify({ jsonrpc: '2.0', id: 1, method: rpcMethod, params: rpcParams }));
		};
		socket.onmessage = (event) => {
			socket.close();
			resolve(JSON.parse(event.data));
		};
	}), { method, params });
}

function sandboxFixture(rootDir) {
	return {
		rootDir,
		homeDir: `${rootDir}/home`,
		projectDir: `${rootDir}/project`,
		uploadFile: `${rootDir}/project/files/sample.txt`,
	};
}
