// @ts-nocheck
import { test, expect } from '@playwright/test';

import { installMockBackend, readBackendState, readMethodCalls } from './support/mock-backend.js';

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
          status: 'idle',
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
          status: 'idle',
          trigger: { type: 'manual' },
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
    dagStart: {
      'daily-brief': {
        response: { runKey: 'run-started' },
        run: {
          run_key: 'run-started',
          status: 'succeeded',
          started_at: '2026-05-23 09:02:00',
          metadata: { final_output: { kind: 'text', text: 'started output' } },
        },
      },
    },
  });

  await page.goto('/');
  await expect(page.getByTestId('app-shell')).toBeVisible();

  await page.getByTestId('nav-dags').click();
  await expect(page.getByTestId('dag-console')).toBeVisible();
  await expect(page.getByTestId('dag-console-list')).toContainText('daily-brief');
  await expect(page.getByTestId('dag-console-final-marker')).toBeVisible();

  await expect(page.getByTestId('dag-node-list')).toContainText('Collect inputs');
  await expect(page.getByTestId('dag-node-list')).toContainText('succeeded');
  await expect(page.getByTestId('dag-node-list')).toContainText('agent');
  await expect(page.getByTestId('dag-node-list')).toContainText('openai / gpt-5 / analyst');
  await expect(page.getByTestId('dag-run-history')).toContainText('run-file');
  await expect(page.getByTestId('dag-final-output-panel')).toContainText('reports/final.md');

  await page.getByTestId('dag-final-output-read').click();
  await expect(page.getByTestId('dag-final-output-content')).toContainText('Smoke file content from sharedfile');

  await page.getByTestId('dag-run-history-row').filter({ hasText: 'run-json' }).click();
  await expect(page.getByTestId('dag-final-output-preview')).toContainText('"verdict": "pass"');

  await expect(page.getByTestId('dag-start-button')).toBeEnabled();
  await page.getByTestId('dag-start-button').click();
  await expect(page.getByTestId('dag-run-history')).toContainText('run-started');
  await expect(page.getByTestId('dag-final-output-preview')).toContainText('started output');

  await expect(page.getByTestId('dag-node-open-chat')).toContainText('thread-child');
  await page.getByTestId('dag-node-open-chat').click();
  await expect(page.getByTestId('chat-page')).toBeVisible();

  const state = await readBackendState(page);
  expect(state.activeThreadId).toBe('thread-child');

  await expect.poll(async () => (await readMethodCalls(page, 'dashboard/dagDetail')).length).toBeGreaterThanOrEqual(2);
  await expect.poll(async () => (await readMethodCalls(page, 'dashboard/dagRuns')).length).toBeGreaterThanOrEqual(2);
  expect(await readMethodCalls(page, 'dashboard/dagStart')).toHaveLength(1);
  expect(await readMethodCalls(page, 'ui/memory/shared-file/get')).toHaveLength(1);
});
