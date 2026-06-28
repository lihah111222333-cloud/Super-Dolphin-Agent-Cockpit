// @ts-nocheck
import { test, expect } from '@playwright/test';

import { installMockBackend, readMethodCalls } from './support/mock-backend.js';
import { prepareVisualSnapshot, expectVisualSnapshot } from './support/visual.js';

test('skills page supports search, edit subfiles, create, import, and delete', async ({ page }) => {
  await installMockBackend(page, {
    projects: ['/workspace/project-alpha'],
    activeProject: '/workspace/project-alpha',
    dashboard: {
      skills: [
        {
          name: 'backend',
          dir: '/mock-skills/backend',
          description: '后端接口开发',
          summary: '处理后端 API 与数据库',
          triggerWords: ['api', 'golang'],
          forceWords: ['必须'],
        },
      ],
    },
    skillFilesByDir: {
      '/mock-skills/backend': [
        { name: 'SKILL.md', path: '/mock-skills/backend/SKILL.md', isMain: true },
        { name: 'notes.md', path: '/mock-skills/backend/notes.md', isMain: false },
      ],
    },
    skillFileContents: {
      '/mock-skills/backend/SKILL.md': {
        content: '---\nname: "backend"\ndescription: "后端接口开发"\nsummary: "处理后端 API 与数据库"\ntrigger_words: ["api", "golang"]\nforce_words: ["必须"]\n---\n\n## 说明\n\n初始后端技能正文。',
        summary: '处理后端 API 与数据库',
        summary_source: 'frontmatter',
      },
      '/mock-skills/backend/notes.md': {
        content: '# 子文件\n\n原始子文件内容。',
      },
    },
    selectProjectDirsResult: ['/imports/ops', '/imports/broken'],
    importResult: {
      skills: [
        { name: 'ops', skill_file: '/imports/ops/SKILL.md' },
      ],
      failures: [
        { source: '/imports/broken', error: 'missing SKILL.md' },
      ],
    },
    confirmResult: true,
  });

  await prepareVisualSnapshot(page);

  await page.goto('/');
  await expect(page.getByTestId('app-shell')).toBeVisible();

  await page.getByTestId('nav-skills').click();
  await expect(page.getByTestId('skills-page')).toBeVisible();
  await expect(page.getByTestId('skills-list')).toContainText('backend');
  await expectVisualSnapshot(page.getByTestId('skills-page'), 'skills-page-overview.png');

  await page.getByTestId('skills-search-input').fill('不存在');
  await expect(page.getByTestId('skills-search-empty-state')).toBeVisible();
  await page.getByTestId('skills-search-input').fill('backend');
  await expect(page.getByTestId('skills-list')).toContainText('后端接口开发');

  await page.getByTestId('skills-edit-button-0').click();
  await expect(page.getByTestId('skills-editor-panel')).toBeVisible();
  await expect(page.getByTestId('skills-editor-name-input')).toHaveValue('backend');

  await page.getByTestId('skills-subfile-item-1').click();
  await page.getByTestId('skills-editor-body-edit-button').click();
  await page.getByTestId('skills-editor-body-input').fill('# 子文件\n\n更新后的子文件正文。');
  await page.getByTestId('skills-save-button').click();

  const subfileWriteCalls = await readMethodCalls(page, 'skills/local/write');
  expect(subfileWriteCalls[0]?.params?.path).toBe('/mock-skills/backend/notes.md');
  expect(subfileWriteCalls[0]?.params?.content).toContain('更新后的子文件正文');

  await page.getByTestId('skills-subfile-item-0').click();
  await page.getByTestId('skills-editor-name-input').fill('backend-pro');
  await page.getByTestId('skills-editor-summary-input').fill('新的后端摘要');
  await page.getByTestId('skills-editor-trigger-input').fill('api, rpc');
  await page.getByTestId('skills-save-button').click();

  const skillSaveCalls = await readMethodCalls(page, 'skills/config/write');
  expect(skillSaveCalls[0]?.params?.name).toBe('backend-pro');
  expect(skillSaveCalls[0]?.params?.content).toContain('新的后端摘要');

  await page.getByTestId('skills-editor-close-button').click();
  await page.getByTestId('skills-create-button').click();
  await page.getByTestId('skills-editor-name-input').fill('new-skill');
  await page.getByTestId('skills-editor-description-input').fill('全新技能描述');
  await page.getByTestId('skills-editor-summary-input').fill('全新技能摘要');
  await page.getByTestId('skills-editor-body-input').fill('## 新建技能\n\n这是一个全新的技能正文。');
  await page.getByTestId('skills-save-button').click();
  await page.getByTestId('skills-search-input').fill('');
  await expect(page.getByTestId('skills-list')).toContainText('new-skill');

  await page.getByTestId('skills-editor-close-button').click();
  await page.getByTestId('skills-import-button').click();
  await expect(page.getByTestId('skills-notice')).toContainText('导入完成：成功 1，失败 1');
  await expect(page.getByTestId('skills-failure-list')).toContainText('/imports/broken');
  await expect(page.getByTestId('skills-list')).toContainText('ops');

  const newSkillCard = page.locator('.skill-card').filter({ hasText: 'new-skill' }).first();
  await newSkillCard.getByRole('button', { name: '删除' }).click();
  await expect(page.locator('.skill-card').filter({ hasText: 'new-skill' })).toHaveCount(0);

  const deleteCalls = await readMethodCalls(page, 'skills/local/delete');
  expect(deleteCalls[0]?.params?.name).toBe('new-skill');
});
