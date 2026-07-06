import React from 'react';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { act, fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { FilesPage } from './FilesPage.jsx';

const backend = vi.hoisted(() => ({
  deleteSharedFile: vi.fn(),
  listSharedFilesDashboard: vi.fn(),
  openSharedFile: vi.fn(),
  readSharedFile: vi.fn(),
  saveTextFile: vi.fn(),
}));

vi.mock('../../services/modules/fileService.js', () => backend);

function renderFilesPage(store = {}) {
  const queryClient = new QueryClient({
    defaultOptions: {
      queries: { retry: false },
      mutations: { retry: false },
    },
  });
  return render(
    <QueryClientProvider client={queryClient}>
      <FilesPage projectPath="/repo/app" store={store} />
    </QueryClientProvider>,
  );
}

function getOverviewMetric(overview, label) {
  const metric = within(overview).getByText(label).closest('div');
  if (!metric) throw new Error(`Missing overview metric: ${label}`);
  return metric;
}

function deferred() {
  let resolve;
  let reject;
  const promise = new Promise((promiseResolve, promiseReject) => {
    resolve = promiseResolve;
    reject = promiseReject;
  });
  return { promise, resolve, reject };
}

describe('FilesPage module', () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  it('exports the files page component', () => {
    expect(FilesPage).toBeTypeOf('function');
  });

  it('shows shared file overview metrics', async () => {
    backend.listSharedFilesDashboard.mockResolvedValue({
      files: [
        { id: 'reports/final.md:0', path: 'reports/final.md', content: 'final report', updatedAt: '2026-06-06T08:00:00Z' },
        { id: 'scratch/work.json:0', path: 'scratch/work.json', content: '{"ok":true}', updatedAt: '2026-06-06T09:00:00Z' },
      ],
      finalOutputRefs: [{ path: 'reports/final.md', runKey: 'run-1', dagKey: 'daily-brief' }],
      retention: {
        items: [
          { path: 'reports/final.md', protected: true, cleanupCandidate: false, reason: 'final_output' },
          { path: 'scratch/work.json', protected: false, cleanupCandidate: true, reason: 'unreferenced' },
        ],
        protectedCount: 1,
        cleanupCandidateCount: 1,
      },
    });

    renderFilesPage();

    expect(await screen.findByRole('heading', { name: '文件产物' })).toBeInTheDocument();
    expect(await screen.findByText('final.md')).toBeInTheDocument();
    expect(screen.getByText('work.json')).toBeInTheDocument();
    const overview = screen.getByRole('region', { name: '共享文件状态' });
    expect(within(overview).getByRole('heading', { name: '共享文件和最终产物' })).toBeInTheDocument();
    expect(within(getOverviewMetric(overview, '全部文件')).getByText('2')).toBeInTheDocument();
    expect(within(getOverviewMetric(overview, '最终产物')).getByText('1')).toBeInTheDocument();
    expect(within(getOverviewMetric(overview, '工作文件')).getByText('1')).toBeInTheDocument();
    expect(within(getOverviewMetric(overview, '可清理文件')).getByText('1')).toBeInTheDocument();
  });

  it('opens mp4 shared files through native open without reading binary content as text', async () => {
    const finalPath = 'dag/douyin/daily-video/run-1/final.mp4';
    backend.listSharedFilesDashboard.mockResolvedValue({
      files: [{ id: `${finalPath}:0`, path: finalPath, content: '', updatedAt: '2026-06-06T08:00:00Z' }],
      finalOutputRefs: [{ path: finalPath, runKey: 'run-1', dagKey: 'douyin-video' }],
      retention: {
        items: [{ path: finalPath, protected: true, cleanupCandidate: false, reason: 'final_output' }],
        protectedCount: 1,
        cleanupCandidateCount: 0,
      },
    });
    backend.openSharedFile.mockResolvedValue({ opened: true });
    backend.readSharedFile.mockResolvedValue({ path: finalPath, content: '' });

    renderFilesPage();

    expect(await screen.findByText('final.mp4')).toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: '打开' }));

    await waitFor(() => expect(backend.openSharedFile).toHaveBeenCalledWith({ path: finalPath }));
    expect(backend.readSharedFile).not.toHaveBeenCalled();
  });

  it('continues with a shared file through a button instead of a checkbox', async () => {
    const finalPath = 'reports/final.md';
    const store = { continueWithSharedFile: vi.fn() };
    backend.listSharedFilesDashboard.mockResolvedValue({
      files: [{ id: `${finalPath}:0`, path: finalPath, content: 'final report', updatedAt: '2026-06-06T08:00:00Z' }],
      finalOutputRefs: [{ path: finalPath, runKey: 'run-1', dagKey: 'daily-brief' }],
      retention: {
        items: [{ path: finalPath, protected: true, cleanupCandidate: false, reason: 'final_output' }],
        protectedCount: 1,
        cleanupCandidateCount: 0,
      },
    });

    renderFilesPage(store);

    expect(await screen.findByText('final.md')).toBeInTheDocument();
    expect(screen.queryByRole('checkbox', { name: '用此文件继续对话' })).not.toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: '用此文件继续对话' }));

    expect(store.continueWithSharedFile).toHaveBeenCalledWith(finalPath);
  });

  it('keeps the latest file preview when an older detail request resolves later', async () => {
    const firstPath = 'reports/first.md';
    const secondPath = 'reports/second.md';
    const firstDetail = deferred();
    const secondDetail = deferred();
    backend.listSharedFilesDashboard.mockResolvedValue({
      files: [
        { id: `${firstPath}:0`, path: firstPath, content: 'first summary', updatedAt: '2026-06-06T08:00:00Z' },
        { id: `${secondPath}:1`, path: secondPath, content: 'second summary', updatedAt: '2026-06-06T09:00:00Z' },
      ],
      finalOutputRefs: [],
      retention: { items: [], protectedCount: 0, cleanupCandidateCount: 0 },
    });
    backend.readSharedFile.mockImplementation(({ path }) => {
      if (path === firstPath) return firstDetail.promise;
      if (path === secondPath) return secondDetail.promise;
      return Promise.reject(new Error(`Unexpected shared file path: ${path}`));
    });

    const { container } = renderFilesPage();

    const firstCard = (await screen.findByText('first.md')).closest('article');
    const secondCard = screen.getByText('second.md').closest('article');
    fireEvent.click(within(firstCard).getByRole('button', { name: '打开' }));
    await waitFor(() => expect(backend.readSharedFile).toHaveBeenCalledWith({ path: firstPath }, expect.objectContaining({ path: firstPath })));
    fireEvent.click(within(secondCard).getByRole('button', { name: '打开' }));
    await waitFor(() => expect(backend.readSharedFile).toHaveBeenCalledWith({ path: secondPath }, expect.objectContaining({ path: secondPath })));

    await act(async () => {
      secondDetail.resolve({ path: secondPath, content: 'latest preview content', updatedAt: '2026-06-06T09:00:00Z' });
      await secondDetail.promise;
    });
    const latestDialog = await screen.findByRole('dialog', { name: '文件预览' });
    expect(within(latestDialog).getByText(secondPath)).toBeInTheDocument();
    expect(container.querySelector('.shared-file-content-preview')?.textContent).toContain('latest preview content');

    await act(async () => {
      firstDetail.resolve({ path: firstPath, content: 'stale preview content', updatedAt: '2026-06-06T08:00:00Z' });
      await firstDetail.promise;
    });

    const dialog = screen.getByRole('dialog', { name: '文件预览' });
    expect(within(dialog).getByText(secondPath)).toBeInTheDocument();
    expect(within(dialog).queryByText(firstPath)).not.toBeInTheDocument();
    expect(container.querySelector('.shared-file-content-preview')?.textContent).toContain('latest preview content');
    expect(container.querySelector('.shared-file-content-preview')?.textContent).not.toContain('stale preview content');
  });
});
