import React from 'react';
import { fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import { ProjectSelector } from './ProjectSelector.jsx';

function createStore(overrides = {}) {
  return {
    activeProject: '/repo/app',
    projects: ['/repo/app', '/repo/side-project'],
    addProjectFromPicker: vi.fn(),
    removeProjectPath: vi.fn(),
    setActiveProjectPath: vi.fn(),
    ...overrides,
  };
}

function renderProjectSelector(store) {
  return render(
    <div className="sa-window" data-theme="light">
      <ProjectSelector store={store} projectPath="/repo/app" />
    </div>,
  );
}

describe('ProjectSelector', () => {
  it('selects, removes, and adds projects through the React Aria menu', async () => {
    const store = createStore();
    renderProjectSelector(store);

    fireEvent.click(screen.getByRole('button', { name: '选择项目' }));
    const menu = await screen.findByRole('menu');

    fireEvent.click(within(menu).getByRole('menuitem', { name: 'repo/side-project' }));
    expect(store.setActiveProjectPath).toHaveBeenCalledWith('/repo/side-project');

    fireEvent.click(screen.getByRole('button', { name: '选择项目' }));
    const reopenedMenu = await screen.findByRole('menu');
    fireEvent.click(within(reopenedMenu).getByRole('button', { name: '移除此项目 repo/side-project' }));
    expect(store.removeProjectPath).toHaveBeenCalledWith('/repo/side-project');

    fireEvent.click(within(reopenedMenu).getByRole('menuitem', { name: '添加项目' }));
    expect(store.addProjectFromPicker).toHaveBeenCalledTimes(1);
  });

  it('closes on Escape and restores focus to the project trigger', async () => {
    const store = createStore();
    renderProjectSelector(store);

    const trigger = screen.getByRole('button', { name: '选择项目' });
    trigger.focus();
    fireEvent.keyDown(trigger, { key: 'ArrowDown' });

    expect(await screen.findByRole('menu')).toBeInTheDocument();

    fireEvent.keyDown(document.activeElement || trigger, { key: 'Escape' });
    await waitFor(() => expect(screen.queryByRole('menu')).not.toBeInTheDocument());
    expect(trigger).toHaveFocus();
  });

  it('keeps the project menu inside the themed application shell', async () => {
    const store = createStore();
    const { container } = renderProjectSelector(store);

    fireEvent.click(screen.getByRole('button', { name: '选择项目' }));
    const popover = (await screen.findByRole('menu')).closest('.project-selector-popover');

    expect(popover).not.toBeNull();
    expect(container.querySelector('.sa-window')).toContainElement(popover);
  });
});
