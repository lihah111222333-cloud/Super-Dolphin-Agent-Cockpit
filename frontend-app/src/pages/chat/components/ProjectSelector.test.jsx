import React from 'react';
import { readFileSync } from 'node:fs';
import path from 'node:path';
import { cwd } from 'node:process';
import { cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import { UNSAFE_PortalProvider } from 'react-aria';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { ProjectSelector } from './ProjectSelector.jsx';

const projectSelectorSource = readFileSync(path.join(cwd(), 'src/pages/chat/components/ProjectSelector.jsx'), 'utf8');
let overlayHost;

beforeEach(() => {
  const hosts = document.querySelectorAll('#overlay-root');
  overlayHost = hosts[0];
  if (hosts.length !== 1 || !(overlayHost instanceof HTMLElement)) {
    throw new Error('ProjectSelector tests require one overlay-root fixture.');
  }
});

afterEach(() => {
  cleanup();
  overlayHost.remove();
});

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

function projectSelectorTree(store, props = {}) {
  return (
    <UNSAFE_PortalProvider getContainer={() => overlayHost}>
      <div className="sa-window" data-theme="light">
        <ProjectSelector store={store} projectPath="/repo/app" {...props} />
      </div>
    </UNSAFE_PortalProvider>
  );
}

function renderProjectSelector(store, props = {}) {
  return render(projectSelectorTree(store, props));
}

describe('ProjectSelector', () => {
  it('has no local portal container owner or React Aria portal override', () => {
    expect(projectSelectorSource).not.toContain('UNSTABLE_portalContainer');
    expect(projectSelectorSource).not.toMatch(/\bportalContainer\b/);
  });

  it('keeps the disabled trigger closed without calling project actions', () => {
    const store = createStore();
    renderProjectSelector(store, { isDisabled: true });

    const trigger = screen.getByRole('button', { name: '选择项目' });
    expect(trigger).toBeDisabled();
    fireEvent.click(trigger);

    expect(screen.queryByRole('menu')).not.toBeInTheDocument();
    expect(store.setActiveProjectPath).not.toHaveBeenCalled();
    expect(store.addProjectFromPicker).not.toHaveBeenCalled();
    expect(store.removeProjectPath).not.toHaveBeenCalled();
  });

  it('closes an open menu when disabled and does not reopen after recovery', async () => {
    const store = createStore();
    const { rerender } = renderProjectSelector(store);

    fireEvent.click(screen.getByRole('button', { name: '选择项目' }));
    expect(await screen.findByRole('menu')).toBeInTheDocument();

    rerender(projectSelectorTree(store, { isDisabled: true }));
    await waitFor(() => expect(screen.queryByRole('menu')).not.toBeInTheDocument());
    expect(screen.getByRole('button', { name: '选择项目' })).toBeDisabled();

    rerender(projectSelectorTree(store));
    expect(screen.getByRole('button', { name: '选择项目' })).toBeEnabled();
    expect(screen.queryByRole('menu')).not.toBeInTheDocument();
  });

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

  it('mounts the project menu in overlay-root instead of the application shell', async () => {
    const store = createStore();
    const { container } = renderProjectSelector(store);

    fireEvent.click(screen.getByRole('button', { name: '选择项目' }));
    const popover = (await screen.findByRole('menu')).closest('.project-selector-popover');

    expect(popover).not.toBeNull();
    expect(overlayHost).toContainElement(popover);
    expect(container.querySelector('.sa-window')).not.toContainElement(popover);
  });
});
