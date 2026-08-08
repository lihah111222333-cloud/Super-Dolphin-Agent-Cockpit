import { describe, expect, it } from 'vitest';
import { chromium } from 'playwright';

import { installAgenticE2EMockWails, readAgenticE2EMockWailsState } from './agentic-e2e-wails-mock.mjs';

describe('agentic e2e strict Wails mock readiness and ACK contracts', () => {
  it('implements the frontend readiness probe and commit epoch contract', async () => {
    const browser = await chromium.launch({ headless: true, args: ['--disable-dev-shm-usage'] });
    try {
      const page = await browser.newPage();
      await installAgenticE2EMockWails(page, { sandbox: sandboxFixture('/tmp/agentic-e2e-readiness') });
      await page.goto('data:text/html,<main>mock</main>');

      await expect(callMockWailsRPC(page, 'ui/frontend/readiness', { phase: 'probe' }))
        .resolves.toEqual(expect.objectContaining({ result: { epoch: 1 } }));
      for (const params of [{ phase: 'commit', epoch: 1 }, { phase: 'commit', epoch: 1 }]) {
        await expect(callMockWailsRPC(page, 'ui/frontend/readiness', params))
          .resolves.toEqual(expect.objectContaining({ result: { epoch: 1 } }));
      }

      const invalidRequests = [
        [{ phase: 'commit', epoch: 2 }, 'wails frontend readiness: epoch does not match current activation'],
        [{ phase: 'probe', epoch: 1 }, 'wails frontend readiness: probe must not include an epoch'],
        [{ phase: ' probe ' }, 'wails frontend readiness: phase must be probe or commit'],
        [{ phase: 'probe', unexpected: true }, 'wails frontend readiness: decode request: json: unknown field "unexpected"'],
        [{ phase: 'reset' }, 'wails frontend readiness: phase must be probe or commit'],
      ];
      for (const [params, message] of invalidRequests) {
        await expect(callMockWailsRPC(page, 'ui/frontend/readiness', params))
          .resolves.toEqual(expect.objectContaining({ error: expect.objectContaining({ message }) }));
      }

      const state = await readAgenticE2EMockWailsState(page);
      expect(state.frontendReadiness).toEqual({ epoch: 1, committedEpoch: 1 });
      expect(state.unhandledRPC).toEqual([]);
      expect(state.failures.map((failure) => failure.method)).toEqual(Array(5).fill('ui/frontend/readiness'));
    }
    finally {
      await browser.close();
    }
  });

  it('responds to exact trace-ingest and ui/log ACKs while recording unhandled RPCs', async () => {
    const browser = await chromium.launch({ headless: true, args: ['--disable-dev-shm-usage'] });
    try {
      const page = await browser.newPage();
      const sandbox = sandboxFixture('/tmp/agentic-e2e-known');
      await installAgenticE2EMockWails(page, { sandbox });
      await page.goto('data:text/html,<main>mock</main>');

      const known = await callMockWailsRPC(page, 'config/read', {});
      expect(known.result).toEqual({
        model: 'gpt-5.5',
        modelProvider: null,
        cwd: sandbox.projectDir,
        approvalPolicy: 'on-failure',
        sandbox: 'workspace-write',
        config: null,
        baseInstructions: null,
        developerInstructions: null,
        personality: null,
        toolRouting: {
          mode: 'legacy',
          routerModel: '',
          routerProvider: 'openai_compatible',
          routerBaseURL: '',
          routerHasAPIKey: false,
          confidenceThreshold: 0.65,
          timeoutSec: 8,
        },
      });
      const traceIngest = await callMockWailsRPC(page, 'observability/frontend/ingest', { events: [{ phase: 'test' }, { phase: 'test' }] });
      expect(traceIngest.result).toEqual({ enabled: true, recorded: 2, dropped: 0 });
      const log = await callMockWailsRPC(page, 'ui/log', { level: 'info' });
      expect(log.result).toEqual({ ok: true });

      const unknown = await callMockWailsRPC(page, 'missing/method', {});
      expect(unknown.error.message).toMatch(/unhandled agentic e2e mock RPC/);
      const state = await readAgenticE2EMockWailsState(page);
      expect(state.calls.map((call) => call.method)).toEqual([
        'config/read',
        'observability/frontend/ingest',
        'ui/log',
        'missing/method',
      ]);
      expect(state.unhandledRPC).toEqual(['missing/method']);
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
    socket.onopen = () => socket.send(JSON.stringify({ jsonrpc: '2.0', id: 1, method: rpcMethod, params: rpcParams }));
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
