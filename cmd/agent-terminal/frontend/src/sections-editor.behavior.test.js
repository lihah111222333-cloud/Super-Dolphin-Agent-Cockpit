// @ts-nocheck
import { beforeEach, describe, expect, it, vi } from 'vitest';

const apiMock = vi.hoisted(() => ({
  callAPI: vi.fn(),
}));

vi.mock('./services/api.js', () => ({
  callAPI: apiMock.callAPI,
}));

vi.mock('./services/log.js', () => ({
  logWarn: vi.fn(),
}));

import { SectionsEditor } from './pages/SectionsEditor.js';
import {
  sectionDisplayName,
  validRecallTopicName,
} from './pages/SystemPromptPage.helpers.js';

function createEditor(overrides = {}) {
  const props = {
    promptId: overrides.promptId ?? 'prompt-1',
    cwd: overrides.cwd ?? '/test-repo',
    fallbackMode: overrides.fallbackMode ?? false,
    visible: overrides.visible ?? false,
  };
  return { props, vm: SectionsEditor.setup(props) };
}

beforeEach(() => {
  apiMock.callAPI.mockReset();
});

describe('SectionsEditor behavior', () => {
  it('friendly names use known labels, recall badges, and fallback copy', () => {
    expect(sectionDisplayName({ section_key: 'identity', trigger_type: 'always' })).toBe('身份设定');
    expect(sectionDisplayName({ section_key: 'sqlc-workflow', trigger_type: 'recall' })).toBe('其他段落 · Recall');
  });

  it('validates recall topics as lowercase dash-separated names shorter than 64 chars', () => {
    expect(validRecallTopicName('sqlc-workflow')).toBe(true);
    expect(validRecallTopicName('SQLC Workflow')).toBe(false);
    expect(validRecallTopicName('bad--topic')).toBe(false);
    expect(validRecallTopicName('a'.repeat(64))).toBe(false);
  });

  it('renders collapsible cards with friendly section names and advanced fields tucked away', () => {
    expect(SectionsEditor.template).toContain('sp-section-toggle');
    expect(SectionsEditor.template).toContain('sp-section-friendly-name');
    expect(SectionsEditor.template).toContain('sp-section-advanced');
    expect(SectionsEditor.template).toContain('sp-section-trigger-select');
    expect(SectionsEditor.template).toContain('sp-section-recall-topic-input');
    expect(SectionsEditor.template).toContain('🔗 Recall');
    expect(SectionsEditor.template).toContain(':disabled="fallbackMode"');
    expect(SectionsEditor.template).toContain(':disabled="fallbackMode || sectionDeletingKey === item.section_key"');
  });

  it('openEdit hydrates recall trigger fields from section rows', () => {
    const { vm } = createEditor();

    vm.openEdit({
      section_key: 'sqlc-workflow',
      region: 'dynamic',
      ordinal: 7,
      body: 'Recall body',
      trigger_type: 'recall',
      recall_topic: 'sqlc-workflow',
    });

    expect(vm.sectionForm.triggerType).toBe('recall');
    expect(vm.sectionForm.recallTopic).toBe('sqlc-workflow');
  });

  it('saveSection sends trigger_type and recall_topic for recall sections', async () => {
    apiMock.callAPI
      .mockResolvedValueOnce({ ok: true })
      .mockResolvedValueOnce({ sections: [] });
    const { vm } = createEditor();

    vm.openCreate();
    vm.sectionForm.sectionKey = 'sqlc-workflow';
    vm.sectionForm.body = 'SQLC workflow recall';
    vm.sectionForm.triggerType = 'recall';
    vm.sectionForm.recallTopic = 'sqlc-workflow';
    await vm.saveSection();

    expect(apiMock.callAPI).toHaveBeenCalledWith('prompt-sections/write', {
      prompt_id: 'prompt-1',
      section_key: 'sqlc-workflow',
      region: 'dynamic',
      ordinal: 0,
      body: 'SQLC workflow recall',
      enabled: true,
      trigger_type: 'recall',
      recall_topic: 'sqlc-workflow',
      scope: 'project',
      cwd: '/test-repo',
    });
  });

  it('saveSection rejects invalid recall topic before writing', async () => {
    apiMock.callAPI.mockResolvedValue({ ok: true });
    const { vm } = createEditor();

    vm.openCreate();
    vm.sectionForm.sectionKey = 'bad-recall';
    vm.sectionForm.body = 'Bad recall';
    vm.sectionForm.triggerType = 'recall';
    vm.sectionForm.recallTopic = 'SQLC Workflow';
    await vm.saveSection();

    expect(apiMock.callAPI).not.toHaveBeenCalledWith('prompt-sections/write', expect.anything());
    expect(vm.notice.message).toContain('recall_topic');
  });

  it('readonly fallback blocks section edit, save, and delete mutations', async () => {
    const { vm } = createEditor({ fallbackMode: true });

    vm.openEdit({ section_key: 'identity', body: 'old body' });
    expect(vm.sectionEditorOpen.value).toBe(false);
    expect(vm.notice.message).toContain('只读');

    vm.sectionForm.sectionKey = 'identity';
    vm.sectionForm.body = 'new body';
    await vm.saveSection();
    await vm.deleteSection({ section_key: 'identity' });

    expect(apiMock.callAPI).not.toHaveBeenCalled();
    expect(vm.notice.message).toContain('只读');
  });
});
