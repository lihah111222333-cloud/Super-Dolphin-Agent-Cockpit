import React, { Profiler } from 'react';
import { existsSync, readFileSync, writeFileSync } from 'node:fs';
import { parse } from '@babel/parser';
import { resolve } from 'node:path';
import process from 'node:process';
import { act, cleanup, render } from '@testing-library/react';
import { afterEach, expect, it, vi } from 'vitest';
import App from '../src/App.jsx';
import {
  resetClientStoreForTests,
  useClientStore,
} from '../src/entities/client/model/useClientStore.js';

const UPDATE_COUNT = 20;
const FRONTEND_PACKAGE_NAME = 'super-dolphin-frontend-app';

vi.mock('/wails/runtime.js', () => ({
  Call: {
    ByID: vi.fn(async (_methodId, _method, payload) => ({
      enabled: false,
      recorded: 0,
      dropped: Array.isArray(payload?.events) ? payload.events.length : 0,
      disabled_reason: 'render-isolation-probe',
    })),
  },
  Events: {},
}));

function resolveAppSourcePath(frontendRoot = process.cwd()) {
  const packagePath = resolve(frontendRoot, 'package.json');
  const appPath = resolve(frontendRoot, 'src/App.jsx');
  if (!existsSync(packagePath) || !existsSync(appPath)) {
    throw new Error(`render isolation frontend root is invalid: ${frontendRoot}`);
  }
  const packageJSON = JSON.parse(readFileSync(packagePath, 'utf8'));
  if (packageJSON.name !== FRONTEND_PACKAGE_NAME) {
    throw new Error(`render isolation frontend root package mismatch: ${packageJSON.name || '<missing>'}`);
  }
  return appPath;
}

function appStoreSubscriptionSources() {
  const source = readFileSync(resolveAppSourcePath(), 'utf8');
  const ast = parse(source, { plugins: ['jsx'], sourceType: 'module' });
  const calls = [];
  const visited = new WeakSet();
  const visit = (value) => {
    if (!value || typeof value !== 'object' || visited.has(value)) return;
    visited.add(value);
    if (value.type === 'CallExpression' && value.callee?.type === 'Identifier'
      && value.callee.name === 'useClientStore') {
      calls.push(Object.freeze({
        source: source.slice(value.start, value.end),
        line: value.loc.start.line,
        column: value.loc.start.column + 1,
      }));
    }
    Object.values(value).forEach(visit);
  };
  visit(ast);
  return calls.sort((left, right) => left.line - right.line || left.column - right.column);
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
  const warnings = [];
  const warningSpy = vi.spyOn(console, 'warn').mockImplementation((...args) => warnings.push(args));
  try {
    render(
      <>
        <Profiler id="mainPage" onRender={onRender}>
          <App skipBootstrap />
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
    await act(async () => Promise.resolve());
  } finally {
    warningSpy.mockRestore();
  }
  if (warnings.length > 0) throw new Error(`render isolation emitted ${warnings.length} console warning(s)`);

  return Object.freeze({
    metricId: 'P01-render-isolation',
    warmupUpdates: 2,
    updateCount: UPDATE_COUNT,
    updateAction: 'useClientStore.getState().setLogLevel',
    mainPageUpdateCommits: updateCommits.mainPage,
    unrelatedSubtreeUpdateCommits: updateCommits.unrelatedSubtree,
    mutationUpdateCommits: updateCommits.mutation,
    mutationDetected: updateCommits.mutation > 1,
    productionBoundary: 'src/App.jsx#App',
    productionStoreSubscriptions: Object.freeze(appStoreSubscriptionSources()),
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
    unrelatedSubtreeUpdateCommits: 0,
    mutationDetected: true,
    productionBoundary: 'src/App.jsx#App',
  }));
  expect(Number.isSafeInteger(evidence.mainPageUpdateCommits)).toBe(true);
  expect(evidence.mutationUpdateCommits).toBeGreaterThan(1);
  const evidencePath = process.env.FRONTEND_PERFORMANCE_EVIDENCE_PATH;
  if (evidencePath) writeFileSync(evidencePath, `${JSON.stringify(evidence)}\n`, 'utf8');
});

it('enumerates every production App store subscription without hiding duplicates', () => {
  const subscriptions = appStoreSubscriptionSources();
  expect(subscriptions.length).toBeGreaterThan(0);
  expect(new Set(subscriptions.map(({ line, column }) => `${line}:${column}`)).size).toBe(subscriptions.length);
  subscriptions.forEach(({ source, line, column }) => {
    expect(source).toMatch(/^useClientStore\(/);
    expect(line).toBeGreaterThan(0);
    expect(column).toBeGreaterThan(0);
  });
});

it('fails fast when the controlled frontend root drifts', () => {
  expect(() => resolveAppSourcePath(resolve(process.cwd(), 'src'))).toThrow(/frontend root is invalid/);
  expect(resolveAppSourcePath()).toBe(resolve(process.cwd(), 'src/App.jsx'));
});

export {
  measureRenderIsolation,
};
