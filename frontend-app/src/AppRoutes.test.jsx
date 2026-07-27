import React, { Suspense } from 'react';
import { fireEvent, render, screen } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import { ActivePageContent } from './AppRoutes.jsx';
import { APP_COPY } from './shared/i18n/appI18n.js';

vi.mock('./pages/prompts/PromptPage.jsx', () => ({
  PromptPage: ({ store }) => (
    <button
      type="button"
      onClick={() => store.notifyAction('个人资料已保存', 'success', { category: 'profile' })}
    >
      模拟保存个人资料
    </button>
  ),
}));

describe('ActivePageContent prompt route', () => {
  it('projects required prompt actions through the production route facade', async () => {
    const notifyAction = vi.fn();
    render(
      <Suspense fallback={<p>loading</p>}>
        <ActivePageContent
          activePage="prompts"
          copy={APP_COPY.zh}
          projectPath="/repo/app"
          store={{
            notifyAction,
            promptRevision: 0,
            resolveLaunchPreferences: vi.fn(),
          }}
        />
      </Suspense>,
    );

    fireEvent.click(await screen.findByRole('button', { name: '模拟保存个人资料' }));

    expect(notifyAction).toHaveBeenCalledWith(
      '个人资料已保存',
      'success',
      { category: 'profile' },
    );
  });
});
