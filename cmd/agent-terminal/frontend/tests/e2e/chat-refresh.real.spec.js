// @ts-nocheck
import { test, expect } from '@playwright/test';

import {
    callRealAPI,
    captureRPCRequests,
    expectAssistantItems,
    findThreadsWithAssistantHistory,
    firstCallDelta,
    isRealBackendReady,
    restoreActiveThread,
    selectThreadInRail,
} from './support/real-backend.js';;

test.describe('real environment chat refresh regressions', () => {
    test('page switch back to chat triggers immediate refresh for active thread', async ({ page }) => {
        await page.goto('/');
        test.skip(!(await isRealBackendReady(page)), '需要真实 backend bridge');
        await expect(page.getByTestId('app-shell')).toBeVisible();
        await expect(page.getByTestId('chat-page')).toBeVisible();

        const { activeThreadId, candidates } = await findThreadsWithAssistantHistory(page, 1);
        test.skip(candidates.length < 1, '需要至少 1 个有 assistant 历史的真实线程');

        const target = candidates.find((item) => item.id === activeThreadId) || candidates[0];
        try {
            await restoreActiveThread(page, target.id);
            await page.reload();
            await expect(page.getByTestId('chat-page')).toBeVisible();
            await expectAssistantItems(page);
            await expect.poll(async () => {
                const snapshot = await callRealAPI(page, 'ui/state/get', {});
                return (snapshot?.activeThreadId || '').toString().trim();
            }, { timeout: 5_000 }).toBe(target.id);

            const tracker = captureRPCRequests(page);
            try {
                await page.getByTestId('nav-dags').click();
                await expect(page.getByTestId('dag-console')).toBeVisible();

                const startedAt = Date.now();
                await page.getByTestId('nav-chat').click();
                await expect(page.getByTestId('chat-page')).toBeVisible();

                await expect.poll(() => tracker.calls.filter((item) => item.at >= startedAt && item.method === 'thread/list').length, {
                    timeout: 5_000,
                }).toBeGreaterThan(0);
                await expect.poll(() => tracker.calls.filter((item) => item.at >= startedAt && item.method === 'ui/state/get').length, {
                    timeout: 5_000,
                }).toBeGreaterThan(0);

                const delta = firstCallDelta(tracker.calls, startedAt, (item) => item.method === 'thread/list' || item.method === 'ui/state/get');
                expect(delta).toBeLessThan(3_000);
                await expectAssistantItems(page);
                await expect.poll(async () => {
                    const snapshot = await callRealAPI(page, 'ui/state/get', {});
                    return (snapshot?.activeThreadId || '').toString().trim();
                }, { timeout: 5_000 }).toBe(target.id);
            } finally {
                tracker.detach();
            }
        } finally {
            await restoreActiveThread(page, activeThreadId);
        }
    });

    test('switching back to a cached thread still triggers immediate refresh path', async ({ page }) => {
        test.skip(!(await isRealBackendReady(page)), '需要真实 backend bridge');
        await page.goto('/');
        await expect(page.getByTestId('app-shell')).toBeVisible();
        await expect(page.getByTestId('chat-page')).toBeVisible();

        const { activeThreadId, candidates } = await findThreadsWithAssistantHistory(page, 2);
        test.skip(candidates.length < 2, '需要至少 2 个有 assistant 历史的真实线程');

        const primary = candidates.find((item) => item.id === activeThreadId) || candidates[0];
        const secondary = candidates.find((item) => item.id !== primary.id);
        test.skip(!secondary, '未找到第二个可切换的真实线程');

        try {
            await restoreActiveThread(page, primary.id);
            await page.reload();
            await expect(page.getByTestId('chat-page')).toBeVisible();
            await expectAssistantItems(page);

            await selectThreadInRail(page, secondary);
            await expectAssistantItems(page);

            await selectThreadInRail(page, primary);
            await expectAssistantItems(page);

            const tracker = captureRPCRequests(page);
            try {
                const startedAt = Date.now();
                await selectThreadInRail(page, secondary);

                await expect.poll(() => tracker.calls.filter((item) => {
                    if (item.at < startedAt) return false;
                    if (item.method === 'thread/messages') {
                        return (item.params?.threadId || '').toString().trim() === secondary.id;
                    }
                    if (item.method === 'ui/state/get') {
                        return (item.params?.threadId || '').toString().trim() === secondary.id;
                    }
                    return false;
                }).length, {
                    timeout: 5_000,
                }).toBeGreaterThan(0);

                const delta = firstCallDelta(tracker.calls, startedAt, (item) => {
                    if (item.method === 'thread/messages') {
                        return (item.params?.threadId || '').toString().trim() === secondary.id;
                    }
                    if (item.method === 'ui/state/get') {
                        return (item.params?.threadId || '').toString().trim() === secondary.id;
                    }
                    return false;
                });
                expect(delta).toBeLessThan(3_000);
                await expectAssistantItems(page);
            } finally {
                tracker.detach();
            }
        } finally {
            await restoreActiveThread(page, activeThreadId);
            await callRealAPI(page, 'ui/state/get', { threadId: activeThreadId });
        }

    });
});
