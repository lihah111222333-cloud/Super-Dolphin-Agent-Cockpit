// @ts-nocheck

import { expect } from '@playwright/test';

export async function callRealAPI(page, method, params = {}) {
    return page.evaluate(async ({ method, params }) => {
        if (!globalThis.go?.main?.App?.CallAPI) {
            throw new Error('real backend bridge is not ready');
        }
        return globalThis.go.main.App.CallAPI(method, params || {});
    }, { method, params });
}

export async function isRealBackendReady(page) {
    return page.evaluate(() => Boolean(globalThis.go?.main?.App?.CallAPI));
}

export function captureRPCRequests(page) {
    const calls = [];
    const handler = (request) => {
        if (request.method() !== 'POST' || !request.url().includes('/rpc')) return;
        let payload = {};
        try {
            payload = request.postDataJSON ? request.postDataJSON() : JSON.parse(request.postData() || '{}');
        } catch {
            payload = {};
        }
        calls.push({
            at: Date.now(),
            method: (payload?.method || '').toString(),
            params: payload?.params && typeof payload.params === 'object' ? payload.params : {},
        });
    };
    page.on('request', handler);
    return {
        calls,
        detach() {
            page.off('request', handler);
        },
    };
}

function normalizeMessageText(message) {
    const content = message?.content;
    if (typeof content === 'string') return content.trim();
    if (Array.isArray(content)) {
        return content
            .map((item) => {
                if (typeof item === 'string') return item.trim();
                if (item && typeof item === 'object' && typeof item.text === 'string') return item.text.trim();
                return '';
            })
            .filter(Boolean)
            .join(' ')
            .trim();
    }
    return '';
}

function candidateLabel(thread) {
    const name = (thread?.name || '').toString().trim();
    if (name) return name;
    const alias = (thread?.alias || '').toString().trim();
    if (alias) return alias;
    return (thread?.id || '').toString().trim();
}

export async function findThreadsWithAssistantHistory(page, desiredCount = 2) {
    const visibleThreadIds = await page.evaluate(() => {
        return Array.from(document.querySelectorAll('.thread-rail-item[data-thread-id]'))
            .map((element) => element.getAttribute('data-thread-id') || '')
            .map((value) => value.toString().trim())
            .filter(Boolean);
    });
    const visibleSet = new Set(visibleThreadIds);
    const snapshot = await callRealAPI(page, 'ui/state/get', {});
    const threads = Array.isArray(snapshot?.threads) ? snapshot.threads : [];
    const candidates = [];
    for (const thread of threads) {
        const threadId = (thread?.id || '').toString().trim();
        if (!threadId) continue;
        if (visibleSet.size > 0 && !visibleSet.has(threadId)) continue;
        const history = await callRealAPI(page, 'thread/messages', { threadId, limit: 30 });
        const messages = Array.isArray(history?.messages) ? history.messages : [];
        const assistant = messages.find((item) => {
            const role = (item?.role || '').toString().trim().toLowerCase();
            return role === 'assistant' && normalizeMessageText(item);
        });
        if (!assistant) continue;
        candidates.push({
            id: threadId,
            label: candidateLabel(thread),
            assistantText: normalizeMessageText(assistant),
        });
        if (candidates.length >= desiredCount) break;
    }
    return {
        activeThreadId: (snapshot?.activeThreadId || '').toString().trim(),
        candidates,
    };
}


export async function selectThreadInRail(page, thread) {
    const label = (thread?.label || '').toString().trim();
    const threadId = (thread?.id || '').toString().trim();
    let locator = threadId
        ? page.locator(`.thread-rail-item[data-thread-id="${threadId}"]`).first()
        : page.locator('.thread-rail-item').filter({ hasText: label }).first();
    if (await locator.count() === 0 && label) {
        locator = page.locator('.thread-rail-item').filter({ hasText: label }).first();
    }
    if (await locator.count() === 0 && threadId) {
        locator = page.locator('.thread-rail-item').filter({ hasText: threadId }).first();
    }
    await expect(locator).toBeVisible();
    await locator.click();
}

export async function expectAssistantItems(page) {
    await expect.poll(async () => page.locator('.chat-item.kind-assistant').count(), {
        timeout: 10_000,
    }).toBeGreaterThan(0);
}



export async function restoreActiveThread(page, threadId) {
    const id = (threadId || '').toString().trim();
    if (!id) return;
    await callRealAPI(page, 'ui/preferences/set', {
        key: 'activeThreadId',
        value: id,
    });
}

export function firstCallDelta(calls, fromTs, matcher) {
    const target = calls.find((item) => item.at >= fromTs && matcher(item));
    if (!target) return Number.POSITIVE_INFINITY;
    return target.at - fromTs;
}
