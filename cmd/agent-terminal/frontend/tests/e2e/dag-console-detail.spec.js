// @ts-nocheck
import { test, expect } from '@playwright/test';

import { installMockBackend, readBackendState, readMethodCalls } from './support/mock-backend.js';

async function openStepsSection(page) {
  const stepsSection = page.locator('details.dag-steps-section');
  if ((await stepsSection.getAttribute('open')) === null) {
    await stepsSection.locator('summary').click();
  }
}

test('DAG console opens detail, starts runs, reads final output, and opens child threads', async ({ page }) => {
  await installMockBackend(page, {
    projects: ['/workspace/project-alpha'],
    threads: [
      {
        id: 'thread-main',
        name: '主线程',
        alias: '主线程',
        cwd: '/workspace/project-alpha',
      },
      {
        id: 'thread-child',
        name: 'DAG 子线程',
        alias: 'DAG 子线程',
        cwd: '/workspace/project-alpha',
      },
    ],
    activeThreadId: 'thread-main',
    activeProject: '/workspace/project-alpha',
    runtimeConfig: { cwd: '/workspace/project-alpha' },
    dashboard: {
      dags: [
        {
          dag_key: 'daily-brief',
          title: 'Daily Brief',
          status: 'ready',
          trigger: { type: 'manual' },
          latest_run: {
            run_key: 'run-file',
            status: 'succeeded',
            metadata: { final_output: { kind: 'file', path: 'reports/final.md' } },
          },
          has_final_output: true,
        },
      ],
      memory: [
        {
          path: 'reports/final.md',
          content: '# Daily Brief\n\nSmoke file content from sharedfile.',
          updated_by: 'agent',
          updated_at: '2026-05-23 09:01:00',
        },
      ],
    },
    dagDetails: {
      'daily-brief': {
        dag: {
          dag_key: 'daily-brief',
          title: 'Daily Brief',
          status: 'ready',
          trigger: { type: 'manual' },
        },
        nodes: [
          {
            node_key: 'collect',
            title: 'Collect template',
            status: 'pending',
            node_type: 'agent',
            config: {
              exec: {
                provider: 'openai',
                model: 'gpt-5',
                agent_key: 'analyst',
              },
            },
          },
        ],
      },
    },
    dagRuns: {
      'daily-brief': [
        {
          run_key: 'run-file',
          status: 'succeeded',
          started_at: '2026-05-23 09:00:00',
          metadata: { final_output: { kind: 'file', path: 'reports/final.md' } },
        },
        {
          run_key: 'run-json',
          status: 'succeeded',
          started_at: '2026-05-23 08:00:00',
          metadata: { final_output: { kind: 'json', result: { verdict: 'pass' } } },
        },
      ],
    },
    dagRunDetails: {
      'run-file': {
        run: {
          run_key: 'run-file',
          status: 'succeeded',
          started_at: '2026-05-23 09:00:00',
          metadata: { final_output: { kind: 'file', path: 'reports/final.md' } },
        },
        nodes: [
          {
            node_key: 'collect',
            title: 'Collect inputs',
            status: 'succeeded',
            node_type: 'agent',
            config: {
              exec: {
                provider: 'openai',
                model: 'gpt-5',
                agent_key: 'analyst',
              },
            },
            spawning_thread_id: 'thread-child',
          },
        ],
      },
      'run-json': {
        run: {
          run_key: 'run-json',
          status: 'succeeded',
          started_at: '2026-05-23 08:00:00',
          metadata: { final_output: { kind: 'json', result: { verdict: 'pass' } } },
        },
        nodes: [
          {
            node_key: 'collect',
            title: 'Collect json',
            status: 'done',
            node_type: 'agent',
          },
        ],
      },
    },
    dagStart: {
      'daily-brief': {
        response: { runKey: 'run-started' },
        run: {
          run_key: 'run-started',
          status: 'succeeded',
          started_at: '2026-05-23 09:02:00',
          metadata: { final_output: { kind: 'text', text: 'started output' } },
        },
        nodes: [
          {
            node_key: 'collect',
            title: 'Collect started',
            status: 'done',
            node_type: 'agent',
            spawning_thread_id: 'thread-child',
          },
        ],
      },
    },
  });

  await page.goto('/');
  await expect(page.getByTestId('app-shell')).toBeVisible();

  await page.getByTestId('nav-dags').click();
  await expect(page.getByTestId('dag-console')).toBeVisible();
  await expect(page.getByTestId('dag-console-list')).toContainText('Daily Brief');
  await expect(page.getByTestId('dag-console-final-marker')).toBeVisible();

  await openStepsSection(page);
  await expect(page.getByTestId('dag-node-list')).toContainText('Collect inputs');
  await expect(page.getByTestId('dag-node-list')).toContainText('成功');
  await expect(page.getByTestId('dag-node-list')).toContainText('查看对话');
  await expect(page.getByTestId('dag-run-history')).toContainText('2026-05-23 09:00:00');
  await expect(page.getByTestId('dag-final-output-panel')).toContainText('reports/final.md');

  await page.getByTestId('dag-final-output-read').click();
  await expect(page.getByTestId('dag-final-output-content')).toContainText('Smoke file content from sharedfile');

  await page.getByTestId('dag-run-history-row').filter({ hasText: '2026-05-23 08:00:00' }).click();
  await expect(page.getByTestId('dag-final-output-preview')).toContainText('"verdict": "pass"');
  await expect(page.getByTestId('dag-node-list')).toContainText('Collect json');

  await expect(page.getByTestId('dag-start-button')).toBeEnabled();
  await page.getByTestId('dag-start-button').click();
  await expect(page.getByTestId('dag-run-history')).toContainText('2026-05-23 09:02:00');
  await expect(page.getByTestId('dag-final-output-preview')).toContainText('started output');
  await expect(page.getByTestId('dag-node-list')).toContainText('Collect started');

  await openStepsSection(page);
  await expect(page.getByTestId('dag-node-open-chat')).toContainText('查看对话');
  await expect(page.getByTestId('dag-node-open-chat')).toBeVisible();
  await page.getByTestId('dag-node-open-chat').click();
  await expect(page.getByTestId('chat-page')).toBeVisible();

  const state = await readBackendState(page);
  expect(state.activeThreadId).toBe('thread-child');

  await expect.poll(async () => (await readMethodCalls(page, 'dashboard/dagDetail')).length).toBeGreaterThanOrEqual(2);
  await expect.poll(async () => (await readMethodCalls(page, 'dashboard/dagRuns')).length).toBeGreaterThanOrEqual(2);
  await expect.poll(async () => (await readMethodCalls(page, 'dashboard/dagRun')).length).toBeGreaterThanOrEqual(3);
  expect(await readMethodCalls(page, 'dashboard/dagStart')).toHaveLength(1);
  expect(await readMethodCalls(page, 'ui/memory/shared-file/get')).toHaveLength(1);
});

test('DAG console blocks start when an active run is outside recent history', async ({ page }) => {
  await installMockBackend(page, {
    projects: ['/workspace/project-alpha'],
    activeProject: '/workspace/project-alpha',
    runtimeConfig: { cwd: '/workspace/project-alpha' },
    dashboard: {
      dags: [{
        dag_key: 'hidden-active',
        title: 'Hidden Active',
        status: 'ready',
        trigger: { type: 'manual' },
        latest_run: { run_key: 'run-1', status: 'succeeded' },
      }],
    },
    dagDetails: {
      'hidden-active': {
        dag: {
          dag_key: 'hidden-active',
          title: 'Hidden Active',
          status: 'ready',
          trigger: { type: 'manual' },
        },
        nodes: [{ node_key: 'collect', title: 'Collect template', status: 'pending', node_type: 'agent' }],
      },
    },
    dagRuns: {
      'hidden-active': [
        { run_key: 'run-1', status: 'succeeded' },
        { run_key: 'run-2', status: 'succeeded' },
        { run_key: 'run-3', status: 'succeeded' },
        { run_key: 'run-4', status: 'failed' },
        { run_key: 'run-5', status: 'cancelled' },
        { run_key: 'run-hidden', status: 'running' },
      ],
    },
    dagRunDetails: {
      'run-1': {
        run: { run_key: 'run-1', status: 'succeeded' },
        nodes: [{ node_key: 'collect', title: 'Collect finished', status: 'done', node_type: 'agent' }],
      },
    },
  });

  await page.goto('/');
  await expect(page.getByTestId('app-shell')).toBeVisible();

  await page.getByTestId('nav-dags').click();
  await expect(page.getByTestId('dag-console')).toBeVisible();
  await expect(page.getByTestId('dag-run-history')).toContainText('第 1 次运行');
  await expect(page.getByTestId('dag-run-history-row')).toHaveCount(5);
  await expect(page.getByTestId('dag-run-history')).not.toContainText('运行中');
  await expect(page.getByTestId('dag-start-button')).toBeDisabled();
  await expect(page.getByTestId('dag-start-disabled-reason')).toContainText('已有运行正在进行');

  const runsCalls = await readMethodCalls(page, 'dashboard/dagRuns');
  expect(runsCalls).toEqual(expect.arrayContaining([
    expect.objectContaining({ params: expect.objectContaining({ dagKey: 'hidden-active', limit: 5 }) }),
    expect.objectContaining({ params: expect.objectContaining({ dagKey: 'hidden-active', status: 'running', limit: 1 }) }),
  ]));
  expect(await readMethodCalls(page, 'dashboard/dagStart')).toHaveLength(0);
});
