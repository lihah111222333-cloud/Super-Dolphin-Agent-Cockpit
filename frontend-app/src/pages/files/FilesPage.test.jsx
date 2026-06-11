import React from 'react';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { fireEvent, render, screen, waitFor } from '@testing-library/react';
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

function renderFilesPage() {
  const queryClient = new QueryClient({
    defaultOptions: {
      queries: { retry: false },
      mutations: { retry: false },
    },
  });
  return render(
    <QueryClientProvider client={queryClient}>
      <FilesPage projectPath="/repo/app" store={{}} />
    </QueryClientProvider>,
  );
}

describe('FilesPage module', () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  it('exports the files page component', () => {
    expect(FilesPage).toBeTypeOf('function');
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
});
