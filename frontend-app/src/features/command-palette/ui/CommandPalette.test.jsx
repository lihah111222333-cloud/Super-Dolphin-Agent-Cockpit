import React, { useState } from 'react';
import { act, fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';

import { CommandPalette } from './CommandPalette.jsx';

const localeCopies = Object.freeze({
  en: Object.freeze({
    title: 'Command palette',
    searchPlaceholder: 'Search commands',
    empty: 'No matching commands',
    labels: Object.freeze({
      'commands.chat.new': 'New chat',
      'commands.settings.open': 'Open settings',
      'commands.sidebar.toggle': 'Toggle sidebar',
    }),
    help: Object.freeze({
      'commands.chat.newHelp': 'Start an empty conversation',
      'commands.settings.openHelp': 'Open application preferences',
    }),
    sections: Object.freeze({ chat: 'Chat', navigation: 'Navigation' }),
  }),
  zh: Object.freeze({
    title: '命令面板',
    searchPlaceholder: '搜索命令',
    empty: '没有匹配的命令',
    labels: Object.freeze({
      'commands.chat.new': '新建对话',
      'commands.settings.open': '打开设置',
      'commands.sidebar.toggle': '切换侧边栏',
    }),
    help: Object.freeze({
      'commands.chat.newHelp': '开始一个空对话',
      'commands.settings.openHelp': '打开应用设置',
    }),
    sections: Object.freeze({ chat: '对话', navigation: '导航' }),
  }),
});

function command(overrides) {
  return {
    id: 'chat.new',
    labelKey: 'commands.chat.new',
    helpKey: 'commands.chat.newHelp',
    section: 'chat',
    shortcut: { key: 'n', meta: false, ctrl: true, alt: false, shift: false },
    ...overrides,
  };
}

const commands = [
  command({}),
  command({
    id: 'settings.open',
    labelKey: 'commands.settings.open',
    helpKey: 'commands.settings.openHelp',
    section: 'navigation',
    shortcut: { key: ',', meta: false, ctrl: true, alt: false, shift: false },
  }),
  command({
    id: 'sidebar.toggle',
    labelKey: 'commands.sidebar.toggle',
    helpKey: undefined,
    section: 'navigation',
    shortcut: { key: 'b', meta: false, ctrl: true, alt: false, shift: false },
  }),
];

describe('CommandPalette', () => {
  it.each([
    ['en', 'Command palette', 'Search commands', 'opng', 'Open settings'],
    ['zh', '命令面板', '搜索命令', '开设', '打开设置'],
  ])('renders and filters localized commands for %s', (locale, title, placeholder, query, settingsLabel) => {
    render(<CommandPalette open commands={commands} execute={vi.fn()} onClose={vi.fn()} copy={localeCopies[locale]} />);

    expect(screen.getByRole('dialog', { name: title })).toBeInTheDocument();
    const search = screen.getByRole('searchbox', { name: placeholder });
    fireEvent.change(search, { target: { value: query } });
    expect(screen.getByRole('option', { name: new RegExp(settingsLabel, 'i') })).toBeInTheDocument();
    expect(screen.queryByRole('option', { name: /new chat|新建对话/i })).not.toBeInTheDocument();
  });

  it('moves the active option with End and Home and executes the active command', () => {
    const execute = vi.fn(() => ({ executed: true, reason: '' }));
    const onClose = vi.fn();
    render(<CommandPalette open commands={commands} execute={execute} onClose={onClose} copy={localeCopies.en} />);

    const search = screen.getByRole('searchbox', { name: 'Search commands' });
    fireEvent.keyDown(search, { key: 'End' });
    expect(screen.getByRole('option', { name: /Toggle sidebar/i })).toHaveAttribute('aria-selected', 'true');
    fireEvent.keyDown(search, { key: 'Home' });
    expect(screen.getByRole('option', { name: /New chat/i })).toHaveAttribute('aria-selected', 'true');
    fireEvent.keyDown(search, { key: 'End' });
    fireEvent.keyDown(search, { key: 'Enter' });

    expect(execute).toHaveBeenCalledWith('sidebar.toggle');
    expect(onClose).toHaveBeenCalledOnce();
  });

  it('shows an empty state for a query with no matches', () => {
    render(<CommandPalette open commands={commands} execute={vi.fn()} onClose={vi.fn()} copy={localeCopies.en} />);

    fireEvent.change(screen.getByRole('searchbox'), { target: { value: 'zzzz' } });

    expect(screen.getByText('No matching commands')).toBeInTheDocument();
    expect(screen.queryByRole('option')).not.toBeInTheDocument();
  });

  it('shows a disabled reason and does not execute the disabled command', () => {
    const execute = vi.fn();
    const disabledCommands = [command({
      canExecute: () => false,
      disabledReason: 'No active project',
    })];
    render(<CommandPalette open commands={disabledCommands} execute={execute} onClose={vi.fn()} copy={localeCopies.en} />);

    const option = screen.getByRole('option', { name: /New chat/i });
    expect(option).toHaveAttribute('aria-disabled', 'true');
    expect(within(option).getByText('No active project')).toBeInTheDocument();
    fireEvent.keyDown(screen.getByRole('searchbox'), { key: 'Enter' });
    expect(execute).not.toHaveBeenCalled();
  });

  it('focuses search on entry, closes on Escape, and restores trigger focus', async () => {
    function Harness() {
      const [open, setOpen] = useState(false);
      return (
        <>
          <button type="button" onClick={() => setOpen(true)}>Open palette</button>
          <CommandPalette open={open} commands={commands} execute={vi.fn()} onClose={() => setOpen(false)} copy={localeCopies.en} />
        </>
      );
    }
    render(<Harness />);
    const trigger = screen.getByRole('button', { name: 'Open palette' });
    trigger.focus();
    fireEvent.click(trigger);

    await waitFor(() => expect(screen.getByRole('searchbox')).toHaveFocus());
    fireEvent.keyDown(screen.getByRole('searchbox'), { key: 'Escape' });
    await act(async () => Promise.resolve());

    expect(screen.queryByRole('dialog', { name: 'Command palette' })).not.toBeInTheDocument();
    await waitFor(() => expect(trigger).toHaveFocus());
  });
});
