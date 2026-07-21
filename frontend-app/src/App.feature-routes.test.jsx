import { installAppTestHooks, testEnv } from "./test-utils/appTestHarness.jsx";

installAppTestHooks();
const {
  act,
  fireEvent,
  render,
  screen,
  waitFor,
  within,
  expect,
  it,
  vi,
  frontendHealthSnapshot,
  App,
  support,
  backend,
  deferred,
  waitForBackendThreadHeading,
} = testEnv;

it('formats markdown-fenced JSON shared files for the row summary and preview modal', async () => {
  const content = [
    '```json',
    JSON.stringify({
      videos: [{
        title: '月薪5000我是怎么在上海活下去的',
        hook: '很多人问我，5000块在上海怎么活？',
        script: '开场：我来上海三年了，最低的时候月薪5000。',
      }],
      thumbnail_idea: '本人手写账单特写',
    }),
    '```',
  ].join('\n');
  backend.listSharedFiles.mockResolvedValue({
    files: [{
      path: 'reports/douyin_viral_scripts.md',
      content,
      updated_by: 'node-router',
      updated_at: '2026-06-03T12:59:59Z',
    }],
    finalOutputRefs: [{
      path: 'reports/douyin_viral_scripts.md',
      runKey: 'run-ui-1',
      dagKey: 'douyin-viral-script-daily-5pm',
      sourceNodeKey: 'generate_douyin_scripts',
    }],
    sharedFileRetention: {
      items: [{ path: 'reports/douyin_viral_scripts.md', protected: true, cleanupCandidate: false, reason: 'final_output' }],
      protectedCount: 1,
      cleanupCandidateCount: 0,
    },
});
  backend.readSharedFile.mockResolvedValue({
    path: 'reports/douyin_viral_scripts.md',
    content,
    updatedBy: 'node-router',
    updatedAt: '2026-06-03T12:59:59Z',
  });

  render(<App />);
  await waitForBackendThreadHeading();
  fireEvent.click(screen.getByLabelText('共享文件'));

  const finalCard = (await screen.findByText('douyin_viral_scripts.md')).closest('article');
  expect(within(finalCard).getByText(/JSON 对象 · videos: 1 项/)).toBeInTheDocument();
  expect(within(finalCard).queryByText(/```json/)).not.toBeInTheDocument();

  fireEvent.click(within(finalCard).getByRole('button', { name: '打开' }));
  const dialog = await screen.findByRole('dialog', { name: '文件预览' });
  expect(within(dialog).getByText('JSON（Markdown 代码块）')).toBeInTheDocument();

  const preview = support.appOverlayHost.querySelector('.shared-file-content-preview');
  expect(preview?.textContent).toContain('"videos": [');
  expect(preview?.textContent).toContain('"title": "月薪5000我是怎么在上海活下去的"');
  expect(preview?.textContent).not.toContain('```json');
  });

it('renders invalid markdown-fenced JSON-like shared files without showing parse errors', async () => {
  const content = [
    '```json\n',
    '{"videos":[',
    '{"title":"月薪5000我是怎么在上海活下去的",',
    '"hook":"很多人问我，5000块在上海怎么活？",',
    '"thumbnail_idea":"本人手写账单特写，标注"月薪5000存款5万"红色大字",',
    '"cta":"评论区报一下"}',
    ']}\n```',
  ].join("");
  backend.listSharedFiles.mockResolvedValue({
    files: [{
      path: 'reports/douyin_viral_scripts.md',
      content,
      updated_by: 'node-router',
      updated_at: '2026-06-03T12:59:59Z',
    }],
    finalOutputRefs: [{
      path: 'reports/douyin_viral_scripts.md',
      runKey: 'run-ui-1',
      dagKey: 'douyin-viral-script-daily-5pm',
      sourceNodeKey: 'generate_douyin_scripts',
    }],
    sharedFileRetention: {
      items: [{ path: 'reports/douyin_viral_scripts.md', protected: true, cleanupCandidate: false, reason: 'final_output' }],
      protectedCount: 1,
      cleanupCandidateCount: 0,
    },
  });
  backend.readSharedFile.mockResolvedValue({
    path: 'reports/douyin_viral_scripts.md',
    content,
    updatedBy: 'node-router',
    updatedAt: '2026-06-03T12:59:59Z',
  });

  render(<App />);
  await waitForBackendThreadHeading();
  fireEvent.click(screen.getByLabelText('共享文件'));

  const finalCard = (await screen.findByText('douyin_viral_scripts.md')).closest('article');
  expect(within(finalCard).getByText(/类 JSON · videos: 1 项/)).toBeInTheDocument();
  expect(within(finalCard).queryByText(/JSON 格式化失败|JSON Parse error|Unrecognized token/)).not.toBeInTheDocument();

  fireEvent.click(within(finalCard).getByRole('button', { name: '打开' }));
  const dialog = await screen.findByRole('dialog', { name: '文件预览' });
  expect(within(dialog).getByText('类 JSON（Markdown 代码块）')).toBeInTheDocument();

  const preview = support.appOverlayHost.querySelector('.shared-file-content-preview');
  expect(preview?.textContent).toContain('\n    "hook":');
  expect(preview?.textContent).toContain('标注"月薪5000存款5万"红色大字');
  expect(preview?.textContent).not.toMatch(/JSON 格式化失败|JSON Parse error|Unrecognized token|```json/);
  });

it('keeps the shared-file delete dialog open while deletion is pending', async () => {
  const deletePending = deferred();
  backend.listSharedFiles.mockResolvedValue({
    files: [{
      path: 'scratch/work.json',
      content: '{"step":1}',
      updated_by: 'agent',
      updated_at: '2026-05-30T07:00:00Z',
    }],
    memory: [{
      path: 'scratch/work.json',
      content: '{"step":1}',
      updated_by: 'agent',
      updated_at: '2026-05-30T07:00:00Z',
    }],
    finalOutputRefs: [],
    sharedFileRetention: {
      items: [{ path: 'scratch/work.json', protected: false, cleanupCandidate: true, reason: 'unreferenced' }],
      protectedCount: 0,
      cleanupCandidateCount: 1,
    },
  });
  backend.deleteSharedFile.mockReturnValue(deletePending.promise);

  render(<App />);
  await waitForBackendThreadHeading();
  fireEvent.click(screen.getByLabelText('共享文件'));

  const workCard = (await screen.findByText('work.json')).closest('article');
  fireEvent.click(within(workCard).getByRole('button', { name: '删除' }));
  let dialog = await screen.findByRole('dialog', { name: '删除文件' });
  fireEvent.click(within(dialog).getByRole('button', { name: '确认删除' }));
  await waitFor(() => {
    expect(within(screen.getByRole('dialog', { name: '删除文件' })).getByRole('button', { name: '删除中...' })).toBeDisabled();
  });

  dialog = screen.getByRole('dialog', { name: '删除文件' });
  fireEvent.keyDown(dialog, { key: 'Escape', code: 'Escape' });
  expect(screen.getByRole('dialog', { name: '删除文件' })).toBeInTheDocument();

  await act(async () => {
    deletePending.resolve({ deleted: true });
  });
  await waitFor(() => {
    expect(screen.queryByRole('dialog', { name: '删除文件' })).not.toBeInTheDocument();
  });
  });

it('accepts the legacy shared-files response without final-output metadata', async () => {
  backend.listSharedFiles.mockResolvedValue({
    memory: [{
      path: 'scratch/legacy.md',
      content: 'legacy shared file',
      updated_by: 'agent',
      updated_at: '2026-05-30T09:00:00Z',
    }],
  });

  render(<App />);
  await waitForBackendThreadHeading();
  fireEvent.click(screen.getByLabelText('共享文件'));

  expect(await screen.findByText('legacy.md')).toBeInTheDocument();
  expect(screen.queryByRole('alert')).not.toBeInTheDocument();
  expect(screen.getByRole('tab', { name: '全部 1' })).toBeInTheDocument();
  expect(screen.getByRole('tab', { name: '工作文件 1' })).toBeInTheDocument();
  });

it('keeps cached shared files visible when navigating back and refreshes silently', async () => {
  let memoryFiles = [{
    path: 'reports/final.md',
    content: 'final summary',
    updated_by: 'dag-runner',
    updated_at: '2026-05-30T08:00:00Z',
  }];
  const memoryPayload = () => ({
    files: memoryFiles,
    memory: memoryFiles,
    finalOutputRefs: [{ path: 'reports/final.md', runKey: 'run-1', dagKey: 'daily-brief', sourceNodeKey: 'report' }],
    sharedFileRetention: {
      items: [{ path: 'reports/final.md', protected: true, cleanupCandidate: false, reason: 'final_output' }],
      protectedCount: 1,
      cleanupCandidateCount: 0,
    },
  });
  backend.listSharedFiles.mockImplementation(() => Promise.resolve(memoryPayload()));

  render(<App />);
  await waitForBackendThreadHeading();
  fireEvent.click(screen.getByLabelText('共享文件'));
  expect(await screen.findByText('final.md')).toBeInTheDocument();

  fireEvent.click(screen.getByLabelText('新对话'));
  memoryFiles = [{
    path: 'scratch/notes.md',
    content: 'fresh notes',
    updated_by: 'agent',
    updated_at: '2026-05-30T09:00:00Z',
  }];
  fireEvent.click(screen.getByLabelText('共享文件'));

  expect(screen.queryByText('正在加载共享文件...')).not.toBeInTheDocument();
  expect(screen.getByText('final.md')).toBeInTheDocument();
  expect(await screen.findByText('notes.md')).toBeInTheDocument();
  expect(screen.queryByText('final.md')).not.toBeInTheDocument();
  });

it('does not poll shared files with a page interval', async () => {
  const intervalSpy = vi.spyOn(window, 'setInterval');
  try {
    backend.listSharedFiles.mockResolvedValue({
      files: [{
        path: 'reports/final.md',
        content: 'final summary',
        updated_by: 'dag-runner',
        updated_at: '2026-05-30T08:00:00Z',
      }],
      finalOutputRefs: [{ path: 'reports/final.md', runKey: 'run-1', dagKey: 'daily-brief', sourceNodeKey: 'report' }],
      sharedFileRetention: {
        items: [{ path: 'reports/final.md', protected: true, cleanupCandidate: false, reason: 'final_output' }],
        protectedCount: 1,
        cleanupCandidateCount: 0,
      },
    });

    render(<App />);
    await waitForBackendThreadHeading();
    fireEvent.click(screen.getByLabelText('共享文件'));

    expect(await screen.findByText('final.md')).toBeInTheDocument();
    expect(intervalSpy.mock.calls.filter((call) => call[1] === 4000)).toHaveLength(0);
  }
  finally {
    intervalSpy.mockRestore();
  }
  });

it('keeps cached shared files visible and exposes retry when a background sync fails', async () => {
  let memoryFiles = [{
    path: 'reports/final.md',
    content: 'final summary',
    updated_by: 'dag-runner',
    updated_at: '2026-05-30T08:00:00Z',
  }];
  const memoryPayload = () => ({
    files: memoryFiles,
    memory: memoryFiles,
    finalOutputRefs: [{ path: 'reports/final.md', runKey: 'run-1', dagKey: 'daily-brief', sourceNodeKey: 'report' }],
    sharedFileRetention: {
      items: [{ path: 'reports/final.md', protected: true, cleanupCandidate: false, reason: 'final_output' }],
      protectedCount: 1,
      cleanupCandidateCount: 0,
    },
  });
  backend.listSharedFiles.mockImplementation(() => Promise.resolve(memoryPayload()));

  render(<App />);
  await waitForBackendThreadHeading();
  fireEvent.click(screen.getByLabelText('共享文件'));
  expect(await screen.findByText('final.md')).toBeInTheDocument();

  backend.listSharedFiles.mockRejectedValueOnce(new Error('shared files backend offline'));
  await act(async () => {
    backend.__bridgeCallback?.({ type: 'ui/shared-files/changed', payload: { path: 'reports/final.md', action: 'write' } });
    await Promise.resolve();
  });

  expect(screen.getByText('final.md')).toBeInTheDocument();
  const alert = await screen.findByRole('alert');
  expect(alert).toHaveTextContent('同步共享文件失败，当前显示上次成功数据。');
  expect(alert).not.toHaveTextContent('shared files backend offline');
  expect(frontendHealthSnapshot()).toEqual(expect.arrayContaining([
    expect.objectContaining({ actionId: 'file.dashboard.load', diagnosticId: expect.any(String) }),
  ]));

  memoryFiles = [{
    path: 'scratch/notes.md',
    content: 'fresh notes',
    updated_by: 'agent',
    updated_at: '2026-05-30T09:00:00Z',
  }];
  fireEvent.click(screen.getByRole('button', { name: '重试同步' }));

  expect(await screen.findByText('notes.md')).toBeInTheDocument();
  expect(screen.queryByRole('alert')).not.toBeInTheDocument();
  });

it('shows a retryable blocking error instead of an empty shared-files state on initial load failure', async () => {
  backend.listSharedFiles.mockRejectedValueOnce(new Error('shared files backend offline'));

  render(<App />);
  await waitForBackendThreadHeading();
  fireEvent.click(screen.getByLabelText('共享文件'));

  const alert = await screen.findByRole('alert');
  expect(alert).toHaveTextContent('加载共享文件失败，请重试。');
  expect(alert).not.toHaveTextContent('shared files backend offline');
  expect(frontendHealthSnapshot()).toEqual(expect.arrayContaining([
    expect.objectContaining({ actionId: 'file.dashboard.load', diagnosticId: expect.any(String) }),
  ]));
  expect(screen.queryByText('还没有文件产物')).not.toBeInTheDocument();

  backend.listSharedFiles.mockResolvedValueOnce({
    files: [{
      path: 'scratch/notes.md',
      content: 'fresh notes',
      updated_by: 'agent',
      updated_at: '2026-05-30T09:00:00Z',
    }],
    finalOutputRefs: [],
    sharedFileRetention: { items: [], protectedCount: 0, cleanupCandidateCount: 0 },
  });
  fireEvent.click(within(alert).getByRole('button', { name: '重试同步' }));

  expect(await screen.findByText('notes.md')).toBeInTheDocument();
  expect(screen.queryByRole('alert')).not.toBeInTheDocument();
  });
