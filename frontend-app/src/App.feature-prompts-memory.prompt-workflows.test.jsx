import { installAppTestHooks, testEnv } from "./test-utils/appTestHarness.jsx";

installAppTestHooks();
const {
  fireEvent,
  render,
  screen,
  waitFor,
  within,
  expect,
  it,
  App,
  backend,
  waitForBackendThreadHeading,
  mockPromptWizardEntryPrompt,
  mockPromptAssetWorkflow,
  openPromptAssetsPage,
  openPromptWizardFromPendingCard,
  editAndDeleteReviewerPrompt,
  handlePendingPromptDraft,
  createGeneratedPromptIntent,
} = testEnv;

it('wires prompt edit, delete, pending draft, and intent wizard actions without card copy action', async () => {
  mockPromptAssetWorkflow();

  await openPromptAssetsPage();
  await editAndDeleteReviewerPrompt();
  await handlePendingPromptDraft();
  await createGeneratedPromptIntent();
});

it('uses the first generated prompt draft option when the backend infers multiple choices', async () => {
  backend.draftPromptIntent.mockResolvedValueOnce({
    requested_kind: 'expert',
    inferred_kind: 'recall',
    drafts: [{
      draft_key: 'intent/recall/generated',
      kind: 'recall',
      scope: 'project',
      status: 'review',
      card: {
        kind: 'recall',
        title: '酒后提醒',
        summary: '阻止酒后继续操作',
        recall_body: '在用户喝酒时提醒停止继续操作。',
        hit_examples: ['我喝酒了还想继续工作'],
        miss_examples: ['普通工作安排'],
      },
      issues: [],
    }],
  });
  backend.commitPromptIntent.mockResolvedValueOnce({ prompt: { id: 'recall/alcohol-guard' } });
  mockPromptWizardEntryPrompt();

  render(<App />);
  await waitForBackendThreadHeading();
  fireEvent.click(screen.getByLabelText('提示词'));
  const { wizard } = await openPromptWizardFromPendingCard('待确认入口');
  fireEvent.click(within(wizard).getByRole('tab', { name: '专家能力' }));
  fireEvent.change(await screen.findByLabelText('写下希望 AI 记住或使用的内容'), {
    target: { value: '在我喝酒的时候阻止我' },
  });
  fireEvent.click(screen.getByRole('button', { name: '帮我生成' }));

  expect(await screen.findByText('酒后提醒')).toBeInTheDocument();
  fireEvent.click(screen.getByRole('button', { name: '确认保存' }));
  await waitFor(() => {
    expect(backend.commitPromptIntent).toHaveBeenCalledWith({ cwd: '/repo/app', draftKey: 'intent/recall/generated', scope: 'project' });
  });
});

it('does not submit prompt drafts that still need revision', async () => {
  backend.draftPromptIntent.mockResolvedValueOnce({
    draft_key: 'intent/expert/alcohol-support',
    kind: 'expert',
    scope: 'project',
    status: 'draft',
    card: {
      kind: 'expert',
      title: '想喝酒时给予支持性鼓励',
      summary: '在用户想喝酒时给予支持。',
      output: '温和提醒用户先停下来。',
      hit_examples: ['我想喝酒'],
      miss_examples: ['帮我写代码'],
    },
    issues: [{ code: 'missing_when_not_to_use', severity: 'block', message: '需要补充不用它的场景' }],
  });
  mockPromptWizardEntryPrompt();

  render(<App />);
  await waitForBackendThreadHeading();
  fireEvent.click(screen.getByLabelText('提示词'));
  await openPromptWizardFromPendingCard('待确认入口');
  fireEvent.change(await screen.findByLabelText('写下希望 AI 记住或使用的内容'), {
    target: { value: '在我想喝酒的时候鼓励我' },
  });
  fireEvent.click(screen.getByRole('button', { name: '帮我生成' }));

  expect(await screen.findByText('想喝酒时给予支持性鼓励')).toBeInTheDocument();
  expect(screen.getByText('这条内容还需要完善后才能保存，请调整描述后重新生成。')).toBeInTheDocument();
  expect(screen.getByRole('button', { name: '确认保存' })).toBeDisabled();
  expect(backend.commitPromptIntent).not.toHaveBeenCalled();
});

it('shows user-facing prompt save guidance when the backend rejects an unready draft', async () => {
  backend.draftPromptIntent.mockResolvedValueOnce({
    draft_key: 'intent/expert/alcohol-support',
    kind: 'expert',
    scope: 'project',
    status: 'ready_to_save',
    card: {
      kind: 'expert',
      title: '想喝酒时给予支持性鼓励',
      summary: '在用户想喝酒时给予支持。',
      output: '温和提醒用户先停下来。',
      hit_examples: ['我想喝酒'],
      miss_examples: ['帮我写代码'],
    },
    issues: [],
  });
  backend.commitPromptIntent.mockRejectedValueOnce(new Error('with_tx prompt_template: [-31007] prompt intent draft is not ready to save'));
  mockPromptWizardEntryPrompt();

  render(<App />);
  await waitForBackendThreadHeading();
  fireEvent.click(screen.getByLabelText('提示词'));
  await openPromptWizardFromPendingCard('待确认入口');
  fireEvent.change(await screen.findByLabelText('写下希望 AI 记住或使用的内容'), {
    target: { value: '在我想喝酒的时候鼓励我' },
  });
  fireEvent.click(screen.getByRole('button', { name: '帮我生成' }));
  expect(await screen.findByText('想喝酒时给予支持性鼓励')).toBeInTheDocument();
  fireEvent.click(screen.getByRole('button', { name: '确认保存' }));

  await waitFor(() => {
    expect(screen.getByText('这条内容还需要完善后才能保存，请调整描述后重新生成。')).toBeInTheDocument();
  });
  expect(screen.getByText('这条内容还需要完善后才能保存，请调整描述后重新生成。')).not.toHaveClass('error');
  expect(screen.queryByText(/with_tx|31007|not ready to save/i)).not.toBeInTheDocument();
});

it('shows generated prompt draft details like the legacy confirmation card', async () => {
  backend.draftPromptIntent.mockResolvedValueOnce({
    draft_key: 'intent/expert/alcohol-support',
    kind: 'expert',
    scope: 'project',
    status: 'draft',
    card: {
      kind: 'expert',
      title: '想喝酒时暂停提醒',
      summary: '在用户表达想喝酒时给予支持。',
      when_to_use: '当用户表达想喝酒、想买酒或可能冲动饮酒时使用。',
      when_not_to_use: '不要用于普通饮食建议或医疗诊断。',
      workflow: ['先接住情绪', '提醒用户暂停饮酒', '建议做一个安全替代行动'],
      save_boundary: '只给出建议，不声称已经保存到记忆。',
      output: '输出一段温和、坚定的提醒，并给出一个可马上执行的替代行动。',
      hit_examples: ['我现在想喝酒'],
      miss_examples: ['推荐一杯咖啡'],
    },
    issues: [{ code: 'missing_when_not_to_use', severity: 'block', message: 'internal field copy' }],
  });
  mockPromptWizardEntryPrompt();

  render(<App />);
  await waitForBackendThreadHeading();
  fireEvent.click(screen.getByLabelText('提示词'));
  await openPromptWizardFromPendingCard('待确认入口');
  fireEvent.change(await screen.findByLabelText('写下希望 AI 记住或使用的内容'), {
    target: { value: '在我想喝酒的时候阻止我' },
  });
  fireEvent.click(screen.getByRole('button', { name: '帮我生成' }));

  expect(await screen.findByText('想喝酒时暂停提醒')).toBeInTheDocument();
  expect(screen.getByText('当用户表达想喝酒、想买酒或可能冲动饮酒时使用。')).toBeInTheDocument();
  expect(screen.getByText('不要用于普通饮食建议或医疗诊断。')).toBeInTheDocument();
  expect(screen.getByText('先接住情绪')).toBeInTheDocument();
  expect(screen.getByText('只给出建议，不声称已经保存到记忆。')).toBeInTheDocument();
  expect(screen.getByText('我现在想喝酒')).toBeInTheDocument();
  expect(screen.getByText('推荐一杯咖啡')).toBeInTheDocument();
  expect(screen.getByText('需要说明哪些问题不适合使用它。')).toBeInTheDocument();
  expect(screen.queryByText('internal field copy')).not.toBeInTheDocument();
});
