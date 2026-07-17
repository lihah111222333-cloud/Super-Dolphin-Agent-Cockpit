import React, { Profiler } from 'react';
import { writeFileSync } from 'node:fs';
import process from 'node:process';
import { act, cleanup, render } from '@testing-library/react';
import { afterEach, expect, it } from 'vitest';
import { useShallow } from 'zustand/react/shallow';
import { selectAppShellStore } from '../src/app/appShellModel.js';
import {
  resetClientStoreForTests,
  useClientStore,
} from '../src/entities/client/model/useClientStore.js';

const UPDATE_COUNT = 20;

function MainPageSubscriptionBoundary() {
  useClientStore(useShallow(selectAppShellStore));
  return <div data-testid="main-page-subscription-boundary" />;
}

function UnrelatedSubtree() {
  useClientStore((state) => state.activeProject);
  return <div data-testid="unrelated-subtree" />;
}

function BroadSubscriptionMutation() {
  useClientStore();
  return <div data-testid="broad-subscription-mutation" />;
}

async function applyUnrelatedLogLevelUpdates(count) {
  for (let index = 0; index < count; index += 1) {
    await act(async () => {
      useClientStore.getState().setLogLevel(index % 2 === 0 ? 'debug' : 'info');
    });
  }
}

async function measureRenderIsolation() {
  resetClientStoreForTests({
    activePage: 'chat',
    activeProject: '/performance/probe',
    cwd: '/performance/probe',
  });
  const updateCommits = {
    mainPage: 0,
    mutation: 0,
    unrelatedSubtree: 0,
  };
  const onRender = (id, phase) => {
    if (phase === 'update') updateCommits[id] += 1;
  };
  render(
    <>
      <Profiler id="mainPage" onRender={onRender}>
        <MainPageSubscriptionBoundary />
      </Profiler>
      <Profiler id="unrelatedSubtree" onRender={onRender}>
        <UnrelatedSubtree />
      </Profiler>
      <Profiler id="mutation" onRender={onRender}>
        <BroadSubscriptionMutation />
      </Profiler>
    </>,
  );

  await applyUnrelatedLogLevelUpdates(2);
  updateCommits.mainPage = 0;
  updateCommits.unrelatedSubtree = 0;
  updateCommits.mutation = 0;
  await applyUnrelatedLogLevelUpdates(UPDATE_COUNT);

  return Object.freeze({
    metricId: 'P01-render-isolation',
    warmupUpdates: 2,
    updateCount: UPDATE_COUNT,
    updateAction: 'useClientStore.getState().setLogLevel',
    mainPageUpdateCommits: updateCommits.mainPage,
    unrelatedSubtreeUpdateCommits: updateCommits.unrelatedSubtree,
    mutationUpdateCommits: updateCommits.mutation,
    mutationDetected: updateCommits.mutation > 1,
  });
}

afterEach(() => {
  cleanup();
  resetClientStoreForTests();
});

it('measures main-page render isolation with the real store and catches a broad subscription mutation', async () => {
  const evidence = await measureRenderIsolation();

  expect(evidence).toEqual(expect.objectContaining({
    updateCount: 20,
    mainPageUpdateCommits: 0,
    unrelatedSubtreeUpdateCommits: 0,
    mutationDetected: true,
  }));
  expect(evidence.mutationUpdateCommits).toBeGreaterThan(1);
  const evidencePath = process.env.FRONTEND_PERFORMANCE_EVIDENCE_PATH;
  if (evidencePath) writeFileSync(evidencePath, `${JSON.stringify(evidence)}\n`, 'utf8');
});

export {
  measureRenderIsolation,
};
