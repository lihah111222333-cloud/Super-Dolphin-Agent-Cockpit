import React from 'react';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import { afterEach, beforeEach, expect, it, vi } from 'vitest';
import { PromptPageView } from './PromptPageView.jsx';

const backend = vi.hoisted(() => ({
  commitPromptIntent: vi.fn(),
  copyTextToClipboard: vi.fn(),
  deletePrompt: vi.fn(),
  deletePromptSection: vi.fn(),
  discardPromptIntent: vi.fn(),
  draftPromptIntent: vi.fn(),
  dryRunPromptIntent: vi.fn(),
  getDashboardPrompts: vi.fn(),
  getPersonalizationProfile: vi.fn(),
  getPreference: vi.fn(),
  getPrompt: vi.fn(),
  listPromptAssets: vi.fn(),
  listPromptSections: vi.fn(),
  savePersonalizationProfile: vi.fn(),
  setPreference: vi.fn(),
  writePrompt: vi.fn(),
  writePromptSection: vi.fn(),
}));

vi.mock('../../pages/prompts/services/promptPageService.js', () => backend);
vi.mock('../../shared/api/backendApi.js', () => ({ getPreference: backend.getPreference }));

beforeEach(() => {
  vi.clearAllMocks();
  backend.listPromptAssets.mockResolvedValue({ prompts: [] });
  backend.getPreference.mockResolvedValue('');
  backend.getPersonalizationProfile.mockResolvedValue({ profile: {} });
  backend.savePersonalizationProfile.mockResolvedValue({ profile: {} });
});

afterEach(cleanup);

it('fails before the profile RPC when required save feedback is unavailable', async () => {
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  });
  render(
    <QueryClientProvider client={queryClient}>
      <PromptPageView projectPath="/repo/app" notifyAction={undefined} />
    </QueryClientProvider>,
  );

  const overview = await screen.findByLabelText('个性化概览');
  fireEvent.change(within(overview).getByLabelText('职业'), { target: { value: '架构师' } });
  fireEvent.click(within(overview).getByRole('button', { name: '保存个人资料' }));

  await waitFor(() => {
    expect(
      screen.queryByText('个人资料保存失败，请重试。')
        || backend.savePersonalizationProfile.mock.calls.length > 0,
    ).toBeTruthy();
  });
  expect(screen.getByText('个人资料保存失败，请重试。')).toBeInTheDocument();
  expect(backend.savePersonalizationProfile).not.toHaveBeenCalled();
}, 10_000);
