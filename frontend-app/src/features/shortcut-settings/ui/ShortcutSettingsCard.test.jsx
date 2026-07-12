import React from 'react';
import { fireEvent, render, screen, within } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';

import { ShortcutSettingsCard } from './ShortcutSettingsCard.jsx';

const localeCopies = Object.freeze({
  en: Object.freeze({
    title: 'Keyboard shortcuts',
    description: 'Customize application commands',
    loading: 'Loading shortcuts...',
    save: 'Save shortcuts',
    reset: 'Reset defaults',
    edit: 'Change shortcut for',
  }),
  zh: Object.freeze({
    title: '键盘快捷键',
    description: '自定义应用命令',
    loading: '正在加载快捷键...',
    save: '保存快捷键',
    reset: '恢复默认值',
    edit: '修改快捷键',
  }),
});

function controller(overrides = {}) {
  return {
    status: 'ready',
    error: '',
    commands: [{
      id: 'chat.new',
      label: 'New chat',
      help: 'Start an empty conversation',
      defaultDisplay: 'Ctrl+N',
      currentDisplay: 'Ctrl+M',
      isCustomized: true,
    }],
    setDraftBinding: vi.fn(),
    save: vi.fn(),
    reset: vi.fn(),
    ...overrides,
  };
}

describe('ShortcutSettingsCard', () => {
  it.each([
    ['en', 'Keyboard shortcuts', 'Save shortcuts', 'Reset defaults'],
    ['zh', '键盘快捷键', '保存快捷键', '恢复默认值'],
  ])('renders localized card controls for %s', (locale, title, saveLabel, resetLabel) => {
    render(<ShortcutSettingsCard controller={controller()} copy={localeCopies[locale]} />);

    expect(screen.getByRole('heading', { name: title })).toBeInTheDocument();
    expect(screen.getByRole('button', { name: saveLabel })).toBeInTheDocument();
    expect(screen.getByRole('button', { name: resetLabel })).toBeInTheDocument();
  });

  it('shows loading, visible errors, and the current draft display', () => {
    const view = render(<ShortcutSettingsCard controller={controller({ status: 'loading', commands: [] })} copy={localeCopies.en} />);
    expect(screen.getByText('Loading shortcuts...')).toBeInTheDocument();

    view.rerender(<ShortcutSettingsCard controller={controller({ error: 'Shortcut conflict' })} copy={localeCopies.en} />);
    expect(screen.getByRole('alert')).toHaveTextContent('Shortcut conflict');
    expect(screen.getByText('Ctrl+M')).toBeInTheDocument();
    expect(screen.getByText('Ctrl+N')).toBeInTheDocument();
  });

  it('disables save and reset while shortcut settings are unavailable', () => {
    render(<ShortcutSettingsCard controller={controller({ status: 'unavailable' })} copy={localeCopies.en} />);

    expect(screen.getByRole('button', { name: 'Save shortcuts' })).toBeDisabled();
    expect(screen.getByRole('button', { name: 'Reset defaults' })).toBeDisabled();
  });

  it('captures an edited binding and wires save and reset actions to the controller', () => {
    const state = controller();
    render(<ShortcutSettingsCard controller={state} copy={localeCopies.en} />);
    const row = screen.getByTestId('shortcut-setting-chat.new');

    fireEvent.keyDown(within(row).getByRole('button', { name: /Change shortcut for New chat/i }), {
      key: 'x',
      ctrlKey: true,
    });
    fireEvent.click(screen.getByRole('button', { name: 'Save shortcuts' }));
    fireEvent.click(screen.getByRole('button', { name: 'Reset defaults' }));

    expect(state.setDraftBinding).toHaveBeenCalledWith('chat.new', {
      key: 'x', meta: false, ctrl: true, alt: false, shift: false,
    });
    expect(state.save).toHaveBeenCalledOnce();
    expect(state.reset).toHaveBeenCalledOnce();
  });
});
