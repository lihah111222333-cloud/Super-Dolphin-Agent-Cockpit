// @ts-nocheck
import { test, expect } from '@playwright/test';

import { installMockBackend } from './support/mock-backend.js';

test('memory center supports durable CRUD and shared-file promote', async ({ page }) => {
  await installMockBackend(page, {
    projects: ['/workspace/project-alpha'],
    activeProject: '/workspace/project-alpha',
    threads: [
      {
        id: 'thread-memory-1',
        name: '记忆线程',
        alias: '记忆线程',
        cwd: '/workspace/project-alpha',
      },
    ],
    activeThreadId: 'thread-memory-1',
    dashboard: {
      memory: [
        {
          path: 'handoff/core-grafana-dashboard.txt',
          updated_by: 'bob',
          updated_at: '2026-04-20T16:00:00Z',
          content: 'Grafana dashboard lives at https://grafana.example.com/team/core.',
        },
      ],
    },
    memoryCenter: {
      overview: {
        enabled: true,
        toolsEnabled: true,
        projectRoot: '/workspace/project-alpha',
        privateRoot: '/tmp/memory/private',
        teamFeatureEnabled: true,
      },
      private: {
        rootPath: '/tmp/memory/private',
        indexPath: '/tmp/memory/private/MEMORY.md',
        entries: [],
      },
      team: {
        rootPath: '/tmp/memory/private/team',
        indexPath: '/tmp/memory/private/team/MEMORY.md',
        entries: [],
      },
    },
  });

  await page.goto('/');

  await page.getByTestId('nav-memory-center').click();
  await expect(page.getByTestId('memory-center-page')).toBeVisible();

  await page.getByTestId('memory-center-project-create').click();
  await page.getByTestId('memory-center-editor-name').fill('Release owner');
  await page.getByTestId('memory-center-editor-description').fill('Who owns production release decisions');
  await page.getByTestId('memory-center-editor-content').fill('Primary source is the production release runbook.');
  await page.getByTestId('memory-center-editor-save').click();
  await expect(page.getByTestId('memory-center-notice')).toContainText('已保存');
  await expect(page.getByTestId('memory-center-project-list')).toContainText('Release owner');

  await page.getByTestId('memory-center-project-edit-0').click();
  await expect(page.getByTestId('memory-center-editor')).toBeVisible();
  await page.getByTestId('memory-center-editor-delete').click();
  await expect(page.getByTestId('memory-center-inline-delete-modal')).toBeVisible();
  await page.getByTestId('memory-center-inline-delete-confirm').click();
  await expect(page.getByTestId('memory-center-notice')).toContainText('已删除');

  await expect(page.getByTestId('memory-center-project-empty')).toBeVisible();

  await page.getByTestId('nav-memory').click();
  await expect(page.getByTestId('shared-files-page')).toBeVisible();
  await page.getByTestId('shared-files-promote-0').click();
  await expect(page.getByTestId('shared-files-promote-modal')).toBeVisible();
  await page.getByTestId('shared-files-promote-type').selectOption('reference');
  await page.getByTestId('shared-files-promote-save').click();
  await expect(page.getByTestId('shared-files-notice')).toContainText('已从共享文件创建记忆');

  await page.getByTestId('nav-memory-center').click();
  await expect(page.getByTestId('memory-center-project-list')).toContainText('Core Grafana Dashboard');
});
