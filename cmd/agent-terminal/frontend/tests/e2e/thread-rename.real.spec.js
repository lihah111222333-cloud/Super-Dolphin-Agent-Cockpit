// @ts-nocheck
import { test, expect } from '@playwright/test';

import { callRealAPI, captureRPCRequests, isRealBackendReady } from './support/real-backend.js';


const TARGET_THREAD_ID = process.env.TEST_E2E_RENAME_THREAD_ID || 'thread-1772932861493-3';
const API_ALIAS = process.env.TEST_E2E_RENAME_API_ALIAS || 'E2E-接口改名探针';
const UI_ALIAS = process.env.TEST_E2E_RENAME_UI_ALIAS || 'E2E-界面改名探针';

function normalizeText(value) {
    return (value || '').toString().trim();
}

async function openChatPage(page) {
    await page.goto('/');
    await expect(page.getByTestId('app-shell')).toBeVisible();
    await expect(page.getByTestId('chat-page')).toBeVisible();
}

async function readThreadState(page, threadId) {
    const snapshot = await callRealAPI(page, 'ui/state/get', {});
    const threads = Array.isArray(snapshot?.threads) ? snapshot.threads : [];
    const agentMetaById = snapshot?.agentMetaById && typeof snapshot.agentMetaById === 'object'
        ? snapshot.agentMetaById
        : {};
    const thread = threads.find((item) => normalizeText(item?.id) === threadId) || null;
    const alias = normalizeText(agentMetaById?.[threadId]?.alias);
    const displayName = normalizeText(alias || thread?.name || thread?.id);
    return {
        snapshot,
        thread,
        alias,
        displayName,
    };
}

async function expectTargetThread(page, threadId) {
    await expect.poll(async () => {
        const state = await readThreadState(page, threadId);
        return Boolean(state.thread);
    }, {
        timeout: 10_000,
    }).toBe(true);
    return readThreadState(page, threadId);
}

async function setThreadNameViaAPI(page, threadId, name) {
    await callRealAPI(page, 'thread/name/set', {
        threadId,
        name,
    });
}

async function waitForDisplayName(page, threadId, expectedName) {
    await expect.poll(async () => {
        const state = await readThreadState(page, threadId);
        return state.displayName;
    }, {
        timeout: 10_000,
    }).toBe(expectedName);
}

async function restoreOriginalName(page, threadId, originalName) {
    await setThreadNameViaAPI(page, threadId, originalName);
    await waitForDisplayName(page, threadId, originalName);
}

test.describe('real environment thread rename regressions', () => {
    test('direct thread/name/set works for the specific target thread', async ({ page }) => {
        await openChatPage(page);
        test.skip(!(await isRealBackendReady(page)), '需要真实 backend bridge');
        const before = await expectTargetThread(page, TARGET_THREAD_ID);
        const originalName = normalizeText(before.alias || before.thread?.name || TARGET_THREAD_ID) || TARGET_THREAD_ID;

        try {
            await setThreadNameViaAPI(page, TARGET_THREAD_ID, API_ALIAS);
            await waitForDisplayName(page, TARGET_THREAD_ID, API_ALIAS);
        } finally {
            await restoreOriginalName(page, TARGET_THREAD_ID, originalName);
        }
    });

    test('inline rename emits thread/name/set and updates the target card', async ({ page }) => {
        test.skip(!(await isRealBackendReady(page)), '需要真实 backend bridge');
        await openChatPage(page);
        const before = await expectTargetThread(page, TARGET_THREAD_ID);
        const originalName = normalizeText(before.alias || before.thread?.name || TARGET_THREAD_ID) || TARGET_THREAD_ID;
        const card = page.locator(`.thread-rail-item[data-thread-id="${TARGET_THREAD_ID}"]`).first();

        await expect(card).toBeVisible();
        await card.click();
        await card.locator('.thread-rail-name').first().click();

        const input = card.locator('.thread-rail-alias-input').first();
        const saveButton = page.locator(`[data-rename-save-button-for="${TARGET_THREAD_ID}"]`).first();
        await expect(input).toBeVisible();
        await input.fill(UI_ALIAS);
        await expect(saveButton).toBeVisible();

        const tracker = captureRPCRequests(page);
        const startedAt = Date.now();
        try {
            await saveButton.click();

            await expect.poll(() => tracker.calls.filter((item) => {
                return item.at >= startedAt
                    && item.method === 'thread/name/set'
                    && normalizeText(item.params?.threadId) === TARGET_THREAD_ID;
            }).length, {
                timeout: 5_000,
            }).toBeGreaterThan(0);

            const renameCalls = tracker.calls.filter((item) => {
                return item.at >= startedAt
                    && item.method === 'thread/name/set'
                    && normalizeText(item.params?.threadId) === TARGET_THREAD_ID;
            });
            expect(renameCalls[0]?.params?.name).toBe(UI_ALIAS);

            await waitForDisplayName(page, TARGET_THREAD_ID, UI_ALIAS);
            await expect(card).toContainText(UI_ALIAS);
        } finally {
            tracker.detach();
            await restoreOriginalName(page, TARGET_THREAD_ID, originalName);
        }
    });
});
