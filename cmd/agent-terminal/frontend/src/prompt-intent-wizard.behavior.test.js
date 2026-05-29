// @ts-nocheck
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { nextTick, reactive } from '../lib/vue.esm-browser.prod.js';

const lifecycleState = vi.hoisted(() => ({
  mounted: [],
  beforeUnmount: [],
}));

const apiMock = vi.hoisted(() => ({
  callAPI: vi.fn(),
  onFilesDropped: vi.fn(),
  readDroppedTextFiles: vi.fn(),
}));

vi.mock('../lib/vue.esm-browser.prod.js', async () => {
  const actual = await vi.importActual('../lib/vue.esm-browser.prod.js');
  return {
    ...actual,
    onMounted: (fn) => {
      lifecycleState.mounted.push(fn);
      fn();
    },
    onBeforeUnmount: (fn) => {
      lifecycleState.beforeUnmount.push(fn);
    },
  };
});

vi.mock('./services/api.js', () => ({
  callAPI: apiMock.callAPI,
  onFilesDropped: apiMock.onFilesDropped,
  readDroppedTextFiles: apiMock.readDroppedTextFiles,
}));

vi.mock('./services/log.js', () => ({
  logWarn: vi.fn(),
}));

import { PromptIntentWizard } from './pages/PromptIntentWizard.js';

let registeredNativeDropHandler = null;
let registeredNativeDropDispose = vi.fn();

function createWizard(overrides = {}) {
  const props = reactive({
    cwd: overrides.cwd ?? '/test-repo',
    visible: overrides.visible ?? true,
    fallbackMode: overrides.fallbackMode ?? false,
    initialDraft: overrides.initialDraft ?? null,
  });
  const emit = vi.fn();
  return { props, emit, vm: PromptIntentWizard.setup(props, { emit }) };
}

function readyDraft(overrides = {}) {
  return {
    draft_key: overrides.draft_key ?? 'draft-1',
    kind: overrides.kind ?? 'expert',
    status: overrides.status ?? 'ready_to_save',
    card: {
      kind: overrides.kind ?? 'expert',
      title: 'SQLC helper',
      summary: 'Use source SQL first.',
      hit_examples: ['Fix sqlc drift'],
      miss_examples: ['Write marketing copy'],
      ...(overrides.card || {}),
    },
    issues: overrides.issues ?? [],
  };
}

beforeEach(() => {
  lifecycleState.mounted.length = 0;
  lifecycleState.beforeUnmount.length = 0;
  registeredNativeDropHandler = null;
  registeredNativeDropDispose = vi.fn();
  apiMock.callAPI.mockReset();
  apiMock.onFilesDropped.mockReset().mockImplementation((handler) => {
    registeredNativeDropHandler = handler;
    return registeredNativeDropDispose;
  });
  apiMock.readDroppedTextFiles.mockReset();
});

afterEach(() => {
  vi.unstubAllGlobals();
});

async function flushPromises() {
  await Promise.resolve();
  await Promise.resolve();
}

describe('PromptIntentWizard behavior', () => {
  it('renders one raw input for ordinary creation', () => {
    expect(PromptIntentWizard.template.match(/data-testid="sp-intent-raw-input"/g)).toHaveLength(1);
    expect(PromptIntentWizard.template).toContain('sp-intent-type-tabs');
    expect(PromptIntentWizard.template).toContain('sp-intent-draft-btn');
    expect(PromptIntentWizard.template).toContain('sp-intent-save-btn');
  });

  it('defaults new assets to this project scope', () => {
    const { vm } = createWizard();

    expect(vm.selectedScope.value).toBe('project');
    expect(vm.globalDefaultRuleConfirmation.value).toBe(false);
    expect(PromptIntentWizard.template).toContain('data-testid="sp-intent-scope-project"');
    expect(PromptIntentWizard.template).toContain('data-testid="sp-intent-scope-global"');
    expect(PromptIntentWizard.template).toContain('这个项目');
    expect(PromptIntentWizard.template).toContain('所有项目');
  });

  it('shows generated save boundary in the confirmation card', () => {
    const { vm } = createWizard();

    expect(vm.cardSaveBoundary({ save_boundary: 'Only output suggested knowledge items unless saving is confirmed.' }))
      .toBe('Only output suggested knowledge items unless saving is confirmed.');
    expect(PromptIntentWizard.template).toContain('data-testid="sp-intent-save-boundary"');
  });

  it('maps quality gate issue codes to ordinary user copy', () => {
    const { vm } = createWizard({
      initialDraft: readyDraft({
        status: 'draft',
        issues: [
          { code: 'missing_save_boundary', severity: 'block' },
          { code: 'vague_output', severity: 'block' },
          { code: 'kind_mismatch', severity: 'block', message: 'Dream returned a different prompt intent kind' },
          { code: 'missing_source_facts', severity: 'review' },
          { code: 'missing_source_fact_coverage', severity: 'review', message: 'API 文档的关键要点覆盖不完整：接口地址、鉴权方式' },
          { code: 'source_fact_not_applied', severity: 'block', message: '原文关键要点尚未进入保存内容：请求参数、错误码' },
          { code: 'default_rule_conflict', severity: 'review', message: 'default rule may conflict with existing project rules' },
        ],
      }),
    });

    expect(vm.reviewIssues.value.map(issue => issue.message)).toEqual([
      '需要说明保存边界：没有明确保存工具或用户确认时，只能输出建议保存的条目，不能声称已经保存。',
      '需要写清楚输出会包含哪些栏目或结构。',
      'AI 判断这份内容更适合作为其他类型，请按推荐方向重新整理。',
      '需要先从原文提取关键要点，再整理成可用内容。',
      'API 文档的关键要点覆盖不完整：接口地址、鉴权方式',
      '原文关键要点尚未进入保存内容：请求参数、错误码',
      '和已有默认规则可能重复或冲突，保存前需要确认。',
    ]);
    expect(vm.canSave.value).toBe(false);
  });

  it('keeps scope selection enabled until a draft exists', async () => {
    apiMock.callAPI.mockResolvedValueOnce(readyDraft());
    const { vm } = createWizard();

    expect(vm.currentDraftKey.value).toBe('');
    expect(vm.isScopeSelectionDisabled()).toBe(false);

    vm.rawInput.value = 'Create shared sqlc workflow knowledge';
    await vm.draftIntent();

    expect(vm.currentDraftKey.value).toBe('draft-1');
    expect(vm.isScopeSelectionDisabled()).toBe(true);
  });

  it('does not render advanced internal fields', () => {
    const forbidden = [
      'when_to_use',
      'match_when',
      'priority',
      'section_key',
      'region',
      'ordinal',
      'trigger_type',
      'recall_topic',
      'enable_when',
    ];
    for (const token of forbidden) {
      expect(PromptIntentWizard.template).not.toContain(token);
    }
  });

  it('drafts expert and displays confirmation card', async () => {
    apiMock.callAPI.mockResolvedValueOnce(readyDraft());
    const { vm, emit } = createWizard();
    vm.rawInput.value = 'Create a sqlc workflow helper';

    await vm.draftIntent();

    expect(apiMock.callAPI).toHaveBeenCalledWith('prompt-intents/draft', {
      kind: 'expert',
      raw_input: 'Create a sqlc workflow helper',
      source_type: 'user_input',
      cwd: '/test-repo',
    });
    expect(vm.state.value).toBe('review');
    expect(vm.currentCard.value.title).toBe('SQLC helper');
    expect(vm.canShowDraft.value).toBe(true);
    expect(emit).toHaveBeenCalledWith('drafted', expect.objectContaining({
      draft_key: 'draft-1',
    }));
  });

  it('normalizes multiple inferred drafts and lets the user select one', async () => {
    apiMock.callAPI.mockResolvedValueOnce({
      requested_kind: 'recall',
      inferred_kind: 'recall',
      drafts: [
        readyDraft({
          draft_key: 'draft-recall',
          kind: 'recall',
          card: {
            kind: 'recall',
            title: 'Claude 原文资料',
            summary: '外部提示词原文资料',
            recall_body: 'You are Claude Code.',
            hit_examples: ['查阅 Claude 原文'],
            miss_examples: ['启用默认规则'],
          },
          issues: [{ code: 'external_system_prompt_source', severity: 'review', message: '需要确认来源' }],
        }),
        readyDraft({
          draft_key: 'draft-rule',
          kind: 'default_rule',
          card: {
            kind: 'default_rule',
            title: '项目执行规则',
            summary: '去身份后的项目规则',
            default_rule_body: '先说明计划和风险，再按确认执行。',
            hit_examples: ['执行项目任务'],
            miss_examples: ['查阅原文'],
          },
          issues: [{ code: 'external_system_prompt_source', severity: 'review', message: '需要确认提炼结果' }],
        }),
      ],
    });
    const { vm } = createWizard();
    vm.selectedKind.value = 'recall';
    vm.rawInput.value = 'You are Claude Code.';

    await vm.draftIntent();

    expect(vm.draftOptions.value).toHaveLength(2);
    expect(vm.currentDraftKey.value).toBe('draft-recall');
    expect(vm.currentCard.value.title).toBe('Claude 原文资料');
    vm.selectDraftOption(vm.draftOptions.value[1]);
    expect(vm.currentDraftKey.value).toBe('draft-rule');
    expect(vm.currentCard.value.title).toBe('项目执行规则');
    expect(vm.selectedKind.value).toBe('default_rule');
    expect(vm.canSave.value).toBe(false);
    vm.markReviewConfirmed(true);
    expect(vm.canSave.value).toBe(true);
  });

  it('selecting global sends explicit global draft target', async () => {
    apiMock.callAPI.mockResolvedValueOnce(readyDraft());
    const { vm } = createWizard();
    vm.selectedScope.value = 'global';
    vm.rawInput.value = 'Create shared sqlc workflow knowledge';

    await vm.draftIntent();

    expect(apiMock.callAPI).toHaveBeenCalledWith('prompt-intents/draft', {
      kind: 'expert',
      raw_input: 'Create shared sqlc workflow knowledge',
      source_type: 'user_input',
      cwd: '/test-repo',
      enable_global: true,
    });
  });

  it('loads an existing pending draft for confirmation', async () => {
    const { vm } = createWizard({
      initialDraft: readyDraft({
        draft_key: 'intent/recall/ready',
        kind: 'recall',
        card: {
          kind: 'recall',
          title: '价格表资料',
          summary: '价格表说明',
          recall_body: 'A 套餐 100 元',
          hit_examples: ['查询价格'],
          miss_examples: ['写代码'],
        },
      }),
    });

    expect(vm.state.value).toBe('review');
    expect(vm.selectedKind.value).toBe('recall');
    expect(vm.currentDraftKey.value).toBe('intent/recall/ready');
    expect(vm.currentCard.value.title).toBe('价格表资料');
    expect(vm.canSave.value).toBe(true);
    expect(apiMock.callAPI).not.toHaveBeenCalled();
  });

  it('shows blocked draft issues and disables save', async () => {
    apiMock.callAPI.mockResolvedValueOnce(readyDraft({
      status: 'draft',
      issues: [{ code: 'bad', severity: 'block', message: '不能保存' }],
    }));
    const { vm } = createWizard();
    vm.rawInput.value = 'bad draft';

    await vm.draftIntent();

    expect(vm.state.value).toBe('draft_blocked');
    expect(vm.hasBlocks.value).toBe(true);
    expect(vm.canSave.value).toBe(false);
  });

  it('requires explicit confirmation before saving review-severity issues', async () => {
    apiMock.callAPI.mockResolvedValueOnce(readyDraft({
      issues: [{ code: 'review', severity: 'review', message: '需要确认' }],
    }));
    const { vm } = createWizard();
    vm.rawInput.value = 'review draft';
    await vm.draftIntent();

    expect(vm.hasReviews.value).toBe(true);
    expect(vm.canSave.value).toBe(false);
    vm.markReviewConfirmed(true);
    expect(vm.canSave.value).toBe(true);
  });

  it('renders default-rule conflicts and suggested alternative', async () => {
    apiMock.callAPI
      .mockResolvedValueOnce(readyDraft({
        kind: 'default_rule',
        card: {
          conflicting_rules: [{ title: '旧规则', summary: '已有数据库规则' }],
          suggested_alternative: {
            kind: 'expert',
            reason: '建议改成：专家能力，requested_kind 为 default_rule 是合适的。',
          },
        },
      }))
      .mockResolvedValueOnce(readyDraft({
        draft_key: 'draft-expert',
        kind: 'expert',
        card: {
          kind: 'expert',
          title: '执行说明专家',
          summary: '按步骤执行。',
          hit_examples: ['需要执行说明'],
          miss_examples: ['保存资料'],
        },
      }));
    const { vm } = createWizard();
    vm.selectedKind.value = 'default_rule';
    vm.rawInput.value = 'default rule';

    await vm.draftIntent();

    expect(vm.currentCard.value.conflicting_rules[0].title).toBe('旧规则');
    expect(vm.suggestedAlternative.value.title).toBe('推荐优化为专家能力');
    expect(vm.suggestedAlternative.value.body).toContain('按步骤执行');
    expect(vm.suggestedAlternative.value.reason).toBe('');
    expect(vm.selectedKind.value).toBe('default_rule');
    expect(vm.hasReviews.value).toBe(true);
    expect(PromptIntentWizard.template).toContain('data-testid="sp-intent-optimization"');
    expect(PromptIntentWizard.template).toContain('data-testid="sp-intent-apply-suggested-alternative"');
    expect(PromptIntentWizard.template).not.toContain('requested_kind');

    await vm.applySuggestedAlternative();

    expect(apiMock.callAPI).toHaveBeenLastCalledWith('prompt-intents/draft', {
      kind: 'expert',
      raw_input: 'default rule',
      source_type: 'user_input',
      cwd: '/test-repo',
    });
    expect(vm.selectedKind.value).toBe('expert');
    expect(vm.currentDraftKey.value).toBe('draft-expert');
  });

  it('derives a one-click alternative from old kind-mismatch drafts', async () => {
    apiMock.callAPI.mockResolvedValueOnce(readyDraft({
      draft_key: 'draft-recall-fixed',
      kind: 'recall',
      card: {
        kind: 'recall',
        title: 'Claude Opus 4.7 参考资料',
        summary: '外部提示词参考资料。',
        recall_body: 'Claude Opus 4.7 prompt notes',
        hit_examples: ['查阅 Claude Opus 4.7 原文要求'],
        miss_examples: ['把它作为项目默认规则启用'],
      },
      issues: [{ code: 'external_system_prompt_source', severity: 'review', message: '需要确认来源' }],
    }));
    const { vm } = createWizard({
      initialDraft: {
        draft_key: 'old-draft',
        requested_kind: 'default_rule',
        status: 'draft',
        card: {
          kind: 'recall',
          title: 'Claude Opus 4.7 文件',
          summary: '外部提示词资料。',
          recall_body: 'Claude prompt text',
          hit_examples: ['查阅 Claude Opus 4.7 原文要求'],
          miss_examples: ['作为默认规则启用'],
        },
        issues: [
          { code: 'kind_mismatch', severity: 'block', message: 'Dream returned a different prompt intent kind' },
          { code: 'default_rule_conflict', severity: 'review', message: 'default rule may conflict with existing project rules' },
        ],
      },
    });

    expect(vm.suggestedAlternative.value.title).toBe('推荐优化为给 AI 查阅的资料');
    expect(vm.reviewIssues.value.map(issue => issue.message)).not.toContain('Dream returned a different prompt intent kind');
    vm.rawInput.value = 'Claude Opus 4.7 prompt text';
    await vm.applySuggestedAlternative();

    expect(apiMock.callAPI).toHaveBeenLastCalledWith('prompt-intents/draft', {
      kind: 'recall',
      raw_input: 'Claude Opus 4.7 prompt text',
      source_type: 'user_input',
      cwd: '/test-repo',
    });
    expect(vm.currentDraftKey.value).toBe('draft-recall-fixed');
  });

  it('requires confirmation before saving default-rule conflict drafts', async () => {
    apiMock.callAPI.mockResolvedValueOnce(readyDraft({
      kind: 'default_rule',
      card: { conflicting_rules: [{ title: '旧规则', summary: '冲突' }] },
    }));
    const { vm } = createWizard();
    vm.selectedKind.value = 'default_rule';
    vm.rawInput.value = 'default rule';
    await vm.draftIntent();

    expect(vm.canSave.value).toBe(false);
    vm.markReviewConfirmed(true);
    expect(vm.canSave.value).toBe(true);
  });

  it('sends confirm_risk only after review issue confirmation', async () => {
    apiMock.callAPI
      .mockResolvedValueOnce(readyDraft({
        issues: [{ code: 'review', severity: 'review', message: '需要确认' }],
      }))
      .mockResolvedValueOnce({ prompt: { id: 'saved' } });
    const { vm } = createWizard();
    vm.rawInput.value = 'review draft';
    await vm.draftIntent();
    vm.markReviewConfirmed(true);
    await vm.commitIntent();

    expect(apiMock.callAPI).toHaveBeenLastCalledWith('prompt-intents/commit', {
      draft_key: 'draft-1',
      cwd: '/test-repo',
      confirm_risk: true,
    });

    apiMock.callAPI.mockReset();
    apiMock.callAPI
      .mockResolvedValueOnce(readyDraft({ draft_key: 'draft-2' }))
      .mockResolvedValueOnce({ prompt: { id: 'saved-2' } });
    const next = createWizard();
    next.vm.rawInput.value = 'clean draft';
    await next.vm.draftIntent();
    await next.vm.commitIntent();
    expect(apiMock.callAPI).toHaveBeenLastCalledWith('prompt-intents/commit', {
      draft_key: 'draft-2',
      cwd: '/test-repo',
    });
  });

  it('selecting global sends explicit global commit confirmation', async () => {
    apiMock.callAPI
      .mockResolvedValueOnce(readyDraft({ draft_key: 'global-expert' }))
      .mockResolvedValueOnce({ prompt: { id: 'saved-global' } });
    const { vm } = createWizard();
    vm.rawInput.value = 'global expert';
    await vm.draftIntent();

    vm.selectedScope.value = 'global';
    await vm.commitIntent();

    expect(apiMock.callAPI).toHaveBeenLastCalledWith('prompt-intents/commit', {
      draft_key: 'global-expert',
      cwd: '/test-repo',
      enable_global: true,
      confirm_global: true,
    });
  });

  it('requires extra confirmation for global default rules', async () => {
    apiMock.callAPI.mockResolvedValueOnce(readyDraft({
      draft_key: 'global-rule',
      kind: 'default_rule',
      card: {
        kind: 'default_rule',
        title: '数据库默认规则',
        summary: '全局数据库规则',
        hit_examples: ['改 migration'],
        miss_examples: ['写周报'],
      },
    }));
    const { vm } = createWizard();
    vm.selectedKind.value = 'default_rule';
    vm.selectedScope.value = 'global';
    vm.rawInput.value = 'database rule';
    await vm.draftIntent();

    expect(vm.requiresGlobalDefaultRuleConfirmation.value).toBe(true);
    expect(vm.canSave.value).toBe(false);

    vm.markGlobalDefaultRuleConfirmed(true);
    expect(vm.canSave.value).toBe(true);
    expect(PromptIntentWizard.template).toContain('data-testid="sp-intent-global-rule-confirm"');
  });

  it('disables draft, dry-run, and commit when fallback mode removes cwd', async () => {
    apiMock.callAPI.mockResolvedValueOnce(readyDraft());
    const { props, vm } = createWizard({ cwd: '/repo-root' });
    vm.rawInput.value = 'ready draft';
    await vm.draftIntent();
    expect(vm.canSave.value).toBe(true);

    props.cwd = '';
    props.fallbackMode = true;
    await nextTick();

    expect(vm.hasResolvedCwd.value).toBe(false);
    expect(vm.canDraft.value).toBe(false);
    expect(vm.canSave.value).toBe(false);

    vm.dryRunQuestion.value = '如何验证？';
    await vm.runDryRun();
    await vm.commitIntent();

    expect(apiMock.callAPI).toHaveBeenCalledTimes(1);
    expect(apiMock.callAPI).toHaveBeenCalledWith('prompt-intents/draft', expect.objectContaining({
      cwd: '/repo-root',
    }));
  });

  it('requires hit and miss examples in the confirmation card', async () => {
    apiMock.callAPI.mockResolvedValueOnce(readyDraft({ card: { hit_examples: [], miss_examples: ['miss'] } }));
    const { vm } = createWizard();
    vm.rawInput.value = 'missing examples';
    await vm.draftIntent();

    expect(vm.examplesReady.value).toBe(false);
    expect(vm.canSave.value).toBe(false);
  });

  it('resets review confirmation when selected type changes', async () => {
    apiMock.callAPI.mockResolvedValueOnce(readyDraft({
      issues: [{ code: 'review', severity: 'review', message: '需要确认' }],
    }));
    const { vm } = createWizard();
    vm.rawInput.value = 'review draft';
    await vm.draftIntent();
    vm.markReviewConfirmed(true);

    vm.selectedKind.value = 'recall';
    await nextTick();

    expect(vm.reviewConfirmation.value).toBe(false);
    expect(vm.draft.value).toBe(null);
  });

  it('resets review confirmation when drafting again', async () => {
    apiMock.callAPI
      .mockResolvedValueOnce(readyDraft({
        issues: [{ code: 'review', severity: 'review', message: '需要确认' }],
      }))
      .mockResolvedValueOnce(readyDraft({ draft_key: 'draft-2' }));
    const { vm } = createWizard();
    vm.rawInput.value = 'review draft';
    await vm.draftIntent();
    vm.markReviewConfirmed(true);

    vm.rawInput.value = 'new draft';
    await nextTick();
    await vm.draftIntent();

    expect(vm.currentDraftKey.value).toBe('draft-2');
    expect(vm.reviewConfirmation.value).toBe(false);
  });

  it('resets review confirmation when the wizard closes and reopens', async () => {
    apiMock.callAPI.mockResolvedValueOnce(readyDraft({
      issues: [{ code: 'review', severity: 'review', message: '需要确认' }],
    }));
    const { props, vm } = createWizard();
    vm.rawInput.value = 'review draft';
    await vm.draftIntent();
    vm.markReviewConfirmed(true);

    props.visible = false;
    await nextTick();
    props.visible = true;
    await nextTick();

    expect(vm.reviewConfirmation.value).toBe(false);
    expect(vm.draft.value).toBe(null);
  });

  it('keeps dry-run question hidden by default', () => {
    const { vm } = createWizard();
    expect(vm.canShowDraft.value).toBe(false);
    expect(PromptIntentWizard.template).toContain('data-testid="sp-intent-dry-run-panel"');
    expect(PromptIntentWizard.template).not.toContain('<details class="sp-intent-dry-run" open');
  });

  it('marks the raw input area as a native file drop target', () => {
    expect(PromptIntentWizard.template).toContain('id="prompt-intent-drop-zone"');
    expect(PromptIntentWizard.template).toContain('data-file-drop-target=""');
    expect(PromptIntentWizard.template).toContain('@drop="onDrop"');
    expect(PromptIntentWizard.template).toContain('sp-intent-drop-hint');
    expect(PromptIntentWizard.template).toContain('松开读取文档、表格或文本资料');
    expect(PromptIntentWizard.template).toContain(`:class="'is-' + noticeLevel"`);
  });

  it('reads native dropped text files into raw input and ignores unrelated targets', async () => {
    apiMock.readDroppedTextFiles.mockResolvedValueOnce([
      { path: '/tmp/sqlc.md', name: 'sqlc.md', text: 'SQLC workflow notes' },
    ]);
    const { vm } = createWizard();

    expect(apiMock.onFilesDropped).toHaveBeenCalledTimes(1);
    expect(typeof registeredNativeDropHandler).toBe('function');

    registeredNativeDropHandler({ files: ['/tmp/ignored.md'], details: { id: 'chat-input-bar' } });
    await flushPromises();
    expect(apiMock.readDroppedTextFiles).not.toHaveBeenCalled();

    registeredNativeDropHandler({ files: ['/tmp/sqlc.md'], details: { id: 'prompt-intent-drop-zone' } });
    await flushPromises();

    expect(apiMock.readDroppedTextFiles).toHaveBeenCalledWith(['/tmp/sqlc.md'], 'prompt-intent-drop-zone');
    expect(vm.rawInput.value).toContain('文件：sqlc.md');
    expect(vm.rawInput.value).toContain('SQLC workflow notes');
    expect(vm.notice.value).toBe('已读取 1 个文件');
    expect(vm.noticeLevel.value).toBe('info');
  });

  it('reads browser dropped text files and reports unsupported files', async () => {
    const { vm } = createWizard();
    const preventDefault = vi.fn();
    const transfer = {
      dropEffect: 'none',
      files: [
        { name: 'notes.md', size: 12, type: 'text/markdown', text: vi.fn(async () => 'Markdown notes') },
      ],
      types: ['Files'],
    };

    vm.onDragEnter({ dataTransfer: transfer, preventDefault });
    expect(vm.dropActive.value).toBe(true);
    await vm.onDrop({ dataTransfer: transfer, preventDefault });

    expect(transfer.files[0].text).toHaveBeenCalled();
    expect(vm.rawInput.value).toContain('文件：notes.md');
    expect(vm.rawInput.value).toContain('Markdown notes');
    expect(vm.dropActive.value).toBe(false);
    expect(vm.dropDepth.value).toBe(0);

    await vm.onDrop({
      dataTransfer: {
        files: [{ name: 'archive.zip', size: 4, type: 'application/zip', text: vi.fn(async () => 'zip') }],
        types: ['Files'],
      },
      preventDefault: vi.fn(),
    });
    expect(vm.notice.value).toContain('不支持的文件类型');
    expect(vm.noticeLevel.value).toBe('error');
  });

  it('shows dry-run question only after expanding the secondary panel', async () => {
    apiMock.callAPI.mockResolvedValueOnce(readyDraft());
    const { vm } = createWizard();
    vm.rawInput.value = 'draft';
    await vm.draftIntent();

    expect(vm.canShowDraft.value).toBe(true);
    expect(PromptIntentWizard.template).toContain('data-testid="sp-intent-dry-run-question"');
  });

  it('dry-run displays a user-facing preview without routing internals', async () => {
    apiMock.callAPI
      .mockResolvedValueOnce(readyDraft())
      .mockResolvedValueOnce({
        would_use: true,
        action: 'prompt_recall',
        target: 'sqlc-workflow',
        reasons: ['question provided: 如何验证？'],
        candidates: ['sqlc-workflow', 'SQLC 工作流'],
        disclaimer: '仅用于保存前验证，不代表真实模型一定做出相同选择。',
      });
    const { vm } = createWizard();
    vm.rawInput.value = 'draft';
    await vm.draftIntent();
    vm.dryRunQuestion.value = '如何验证？';
    await vm.runDryRun();

    expect(apiMock.callAPI).toHaveBeenLastCalledWith('prompt-intents/dry-run', {
      draft_key: 'draft-1',
      kind: 'expert',
      card: vm.currentCard.value,
      question: '如何验证？',
      cwd: '/test-repo',
    });
    const summary = vm.dryRunSummary(vm.dryRunResult.value);
    expect(summary).toContain('这个问题会触发这份资料「sqlc-workflow」');
    expect(summary).not.toContain('prompt_recall');
    expect(summary).not.toContain('question provided');
    expect(summary).not.toContain('候选');
    expect(PromptIntentWizard.template).toContain('仅用于保存前验证');
  });

  it('dry-run describes default rules without exposing action names', () => {
    const { vm } = createWizard();
    const summary = vm.dryRunSummary({
      would_use: true,
      action: 'default_rule',
      target: '代码修改后必须完成 P0 审核',
      reasons: ['question provided: 你改完代码做什么'],
      candidates: ['代码修改后必须完成 P0 审核'],
      disclaimer: '这是创建前试问解释，不会写入路由索引，也不承诺真实模型一定做出相同选择。',
    });

    expect(summary).toContain('这个问题会触发这条默认规则「代码修改后必须完成 P0 审核」');
    expect(summary).toContain('保存后，AI 会在类似问题中看到这份内容');
    expect(summary).not.toContain('default_rule');
    expect(summary).not.toContain('question provided');
    expect(summary).not.toContain('候选');
    expect(summary).not.toContain('路由索引');
  });

  it('ignores stale dry-run results after switching draft options', async () => {
    let resolveDryRun;
    apiMock.callAPI.mockResolvedValueOnce({
      requested_kind: 'recall',
      inferred_kind: 'recall',
      drafts: [
        readyDraft({
          draft_key: 'draft-recall',
          kind: 'recall',
          card: {
            kind: 'recall',
            title: '资料草稿',
            summary: '资料',
            recall_topic: 'reference',
            recall_body: 'body',
            hit_examples: ['查资料'],
            miss_examples: ['执行规则'],
          },
        }),
        readyDraft({
          draft_key: 'draft-rule',
          kind: 'default_rule',
          card: {
            kind: 'default_rule',
            title: '规则草稿',
            summary: '规则',
            default_rule_body: '先说明计划。',
            hit_examples: ['执行任务'],
            miss_examples: ['查资料'],
          },
        }),
      ],
    }).mockImplementationOnce(() => new Promise(resolve => { resolveDryRun = resolve; }));
    const { vm } = createWizard();
    vm.selectedKind.value = 'recall';
    vm.rawInput.value = 'multi draft';
    await vm.draftIntent();

    vm.dryRunQuestion.value = '什么时候查资料？';
    const pending = vm.runDryRun();
    vm.selectDraftOption(vm.draftOptions.value[1]);
    resolveDryRun({
      would_use: true,
      action: 'prompt_recall',
      target: 'reference',
      reasons: ['old draft'],
      disclaimer: 'old disclaimer',
    });
    await pending;

    expect(vm.currentDraftKey.value).toBe('draft-rule');
    expect(vm.dryRunResult.value).toBe(null);
  });

  it('keeps the confirmation card visible while dry-run is loading', async () => {
    let resolveDryRun;
    apiMock.callAPI
      .mockResolvedValueOnce(readyDraft())
      .mockImplementationOnce(() => new Promise(resolve => { resolveDryRun = resolve; }));
    const { vm } = createWizard();
    vm.rawInput.value = 'draft';
    await vm.draftIntent();
    vm.dryRunQuestion.value = '如何验证？';

    const pending = vm.runDryRun();
    await flushPromises();

    expect(vm.state.value).toBe('dry_running');
    expect(vm.canShowDraft.value).toBe(true);

    resolveDryRun({ would_use: true, action: 'launch_agent', target: 'SQLC helper', reasons: [] });
    await pending;
  });

  it('commit emits saved event', async () => {
    apiMock.callAPI
      .mockResolvedValueOnce(readyDraft())
      .mockResolvedValueOnce({ prompt: { id: 'saved' } });
    const { emit, vm } = createWizard();
    vm.rawInput.value = 'draft';
    await vm.draftIntent();
    await vm.commitIntent();

    expect(emit).toHaveBeenCalledWith('saved', { prompt: { id: 'saved' } });
    expect(vm.state.value).toBe('editing');
  });
});
