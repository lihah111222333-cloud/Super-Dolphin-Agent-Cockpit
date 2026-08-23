import { Profiler } from 'react';
import { act, cleanup, render } from '@testing-library/react';
import { afterEach, expect, it, vi } from 'vitest';
import App from './App.jsx';
import {
  resetClientStoreForTests,
  useClientStore,
} from './entities/client/model/useClientStore.js';

vi.mock('/wails/runtime.js', () => ({
  Call: {
    ByID: vi.fn(async (_methodId, _method, payload) => ({
      enabled: false,
      recorded: 0,
      dropped: Array.isArray(payload?.events) ? payload.events.length : 0,
      disabled_reason: 'render-isolation-test',
    })),
  },
  Events: {},
}));

window.matchMedia = vi.fn(() => ({
  matches: false,
  addEventListener: vi.fn(),
  removeEventListener: vi.fn(),
}));

afterEach(() => {
  cleanup();
  resetClientStoreForTests();
});

function BroadStoreSubscriptionControl() {
  useClientStore();
  return <div data-testid="broad-store-subscription-control" />;
}

async function applyLogLevelUpdates(count) {
  for (let index = 0; index < count; index += 1) {
    const level = index % 2 === 0 ? 'debug' : 'info';
    await act(async () => {
      useClientStore.getState().setLogLevel(level);
    });
    expect(useClientStore.getState().logLevel).toBe(level);
  }
}

it('keeps App isolated from unrelated store updates', async () => {
  resetClientStoreForTests({
    activePage: 'chat',
    activeProject: '/render-isolation',
    cwd: '/render-isolation',
  });
  let mainPageUpdateCommits = 0;
  let broadControlUpdateCommits = 0;
  const warnings = [];
  const warningSpy = vi.spyOn(console, 'warn').mockImplementation((...args) => warnings.push(args));
  try {
    render(
      <>
        <Profiler
          id="mainPage"
          onRender={(_id, phase) => {
            if (phase === 'update') mainPageUpdateCommits += 1;
          }}
        >
          <App skipBootstrap />
        </Profiler>
        <Profiler
          id="broadControl"
          onRender={(_id, phase) => {
            if (phase === 'update') broadControlUpdateCommits += 1;
          }}
        >
          <BroadStoreSubscriptionControl />
        </Profiler>
      </>,
    );
    await applyLogLevelUpdates(2);
    mainPageUpdateCommits = 0;
    broadControlUpdateCommits = 0;
    await applyLogLevelUpdates(20);
    await act(async () => Promise.resolve());
  } finally {
    warningSpy.mockRestore();
  }

  expect(warnings).toEqual([]);
  expect(broadControlUpdateCommits).toBeGreaterThan(1);
  expect(mainPageUpdateCommits).toBeLessThanOrEqual(1);
});
