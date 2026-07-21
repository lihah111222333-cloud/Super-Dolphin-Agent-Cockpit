import { describe, expect, it } from 'vitest';
import { chromium } from 'playwright';

import { collectPageFacts, normalizeDOMSummaryItem } from './agentic-e2e.mjs';

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
          <section data-testid="settings-page">
            <select data-testid="settings-provider-model">
              <option value="gpt-5" selected>gpt-5</option>
            </select>
            <select data-testid="settings-provider-effort">
              <option value="high" selected>high</option>
            </select>
            <select data-testid="settings-provider-personality">
              <option value="friendly" selected>friendly</option>
            </select>
            <input data-testid="settings-provider-codex-home" value="/tmp/agentic-e2e/home/.codex" />
            <input data-testid="settings-provider-instance-key" value="agentic-e2e" />
            <textarea data-testid="settings-provider-writable-roots">/tmp/agentic-e2e/project</textarea>
            <button data-testid="settings-provider-save-button">保存</button>
          </section>
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
			expect(facts).toEqual(expect.objectContaining({
				settingsProviderSaveVisible: true,
				settingsProviderModelValue: 'gpt-5',
				settingsProviderEffortValue: 'high',
				settingsProviderPersonalityValue: 'friendly',
				settingsProviderCodexHomeValue: '/tmp/agentic-e2e/home/.codex',
				settingsProviderInstanceKeyValue: 'agentic-e2e',
				settingsProviderWritableRootsValue: '/tmp/agentic-e2e/project',
			}));
		}
		finally {
			await browser.close();
		}
	}, 15_000);
});
