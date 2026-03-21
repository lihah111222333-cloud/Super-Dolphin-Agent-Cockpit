// @ts-nocheck
import { test, expect } from '@playwright/test';

import { installMockBackend } from './support/mock-backend.js';

test('sidebar pages load dashboard data and tasks tabs switch correctly', async ({ page }) => {
  await installMockBackend(page, {
    threads: [
      {
        id: 'thread-nav-1',
        name: '导航测试线程',
        alias: '导航测试线程',
        cwd: '/workspace/project-alpha',
      },
    ],
    activeThreadId: 'thread-nav-1',

    dashboard: {
      agents: [
        { agent_id: 'agent-alpha', status: 'running', updated_at: '2026-03-06 10:00:00' },
      ],
      dags: [
        { dag_key: 'build-release', status: 'idle', updated_at: '2026-03-06 10:05:00' },
      ],
      taskAcks: [
        { ack_key: 'ACK-101', title: '修复前端回归', status: 'open', assigned_to: 'alice' },
      ],
      taskTraces: [
        { trace_id: 'trace-9001', span_name: 'e2e-runner', status: 'ok', started_at: '2026-03-06 10:08:00' },
      ],
      memory: [
        { path: '/workspace/project-alpha/memory.md', updated_by: 'bob', updated_at: '2026-03-06 10:09:00' },
      ],
    },
  });

  await page.goto('/');
  await expect(page.getByTestId('app-shell')).toBeVisible();

  await page.getByTestId('nav-dags').click();
  await expect(page.getByTestId('data-page-dags')).toBeVisible();
  await expect(page.getByTestId('data-page-list-dags')).toContainText('build-release');

  await page.getByTestId('nav-tasks').click();
  await expect(page.getByTestId('tasks-page')).toBeVisible();
  await expect(page.getByTestId('tasks-list')).toContainText('ACK-101');
  await expect(page.getByTestId('tasks-list')).toContainText('修复前端回归');

  await page.getByTestId('tasks-subtab-traces').click();
  await expect(page.getByTestId('tasks-list')).toContainText('trace-9001');
  await expect(page.getByTestId('tasks-list')).toContainText('e2e-runner');

  await page.getByTestId('nav-memory').click();
  await expect(page.getByTestId('data-page-memory')).toBeVisible();
  await expect(page.getByTestId('data-page-list-memory')).toContainText('/workspace/project-alpha/memory.md');
});
