import React from 'react';
import { cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { FocusTrapDialog } from './FocusTrapDialog.jsx';

let caller;
let overlayHost;

beforeEach(() => {
  caller = document.createElement('div');
  caller.dataset.testid = 'dialog-caller';
  const hosts = document.querySelectorAll('#overlay-root');
  overlayHost = hosts[0];
  if (hosts.length !== 1 || !(overlayHost instanceof HTMLElement)) {
    throw new Error('FocusTrapDialog tests require one overlay-root fixture.');
  }
  document.body.append(caller);
});

afterEach(() => {
  cleanup();
  caller.remove();
  overlayHost.remove();
});

function renderDialog(props = {}, children = <button type="button">Focusable action</button>) {
  return render(
    <FocusTrapDialog ariaLabel="Test dialog" onClose={vi.fn()} {...props}>
      {children}
    </FocusTrapDialog>,
    { container: caller },
  );
}

describe('FocusTrapDialog', () => {
  it('mounts the dialog in overlay-root instead of the caller', () => {
    renderDialog();

    const dialog = screen.getByRole('dialog', { name: 'Test dialog' });
    expect(within(overlayHost).getByRole('dialog', { name: 'Test dialog' })).toBe(dialog);
    expect(within(caller).queryByRole('dialog')).not.toBeInTheDocument();
  });

  it('renders dialog content and calls close on Escape', () => {
    const onClose = vi.fn();
    renderDialog({ onClose });

    fireEvent.keyDown(screen.getByRole('dialog'), { key: 'Escape' });
    expect(screen.getByRole('dialog', { name: 'Test dialog' }).tagName).toBe('DIALOG');
    expect(onClose).toHaveBeenCalledTimes(1);
  });

  it('focuses the first action by default and honors initialFocusSelector', async () => {
    const view = renderDialog({}, (
      <>
        <button type="button">First action</button>
        <button type="button" data-primary-action>Primary action</button>
      </>
    ));

    await waitFor(() => expect(screen.getByRole('button', { name: 'First action' })).toHaveFocus());

    view.rerender(
      <FocusTrapDialog
        ariaLabel="Test dialog"
        initialFocusSelector="[data-primary-action]"
        onClose={vi.fn()}
      >
        <button type="button">First action</button>
        <button type="button" data-primary-action>Primary action</button>
      </FocusTrapDialog>,
    );

    await waitFor(() => expect(screen.getByRole('button', { name: 'Primary action' })).toHaveFocus());
  });

  it('wraps Tab from last to first and Shift+Tab from first to last', async () => {
    renderDialog({}, (
      <>
        <button type="button">First action</button>
        <button type="button">Last action</button>
      </>
    ));
    const dialog = screen.getByRole('dialog');
    const first = screen.getByRole('button', { name: 'First action' });
    const last = screen.getByRole('button', { name: 'Last action' });
    await waitFor(() => expect(first).toHaveFocus());

    last.focus();
    fireEvent.keyDown(dialog, { key: 'Tab' });
    expect(first).toHaveFocus();

    first.focus();
    fireEvent.keyDown(dialog, { key: 'Tab', shiftKey: true });
    expect(last).toHaveFocus();
  });

  it('closes on an enabled backdrop click and blocks close while disabled', () => {
    const onClose = vi.fn();
    const view = renderDialog({ closeOnOverlayClick: true, onClose });

    fireEvent.click(screen.getByRole('button', { name: '关闭Test dialog' }));
    expect(onClose).toHaveBeenCalledTimes(1);

    view.rerender(
      <FocusTrapDialog
        ariaLabel="Test dialog"
        closeDisabled
        closeOnOverlayClick
        onClose={onClose}
      >
        <button type="button">Focusable action</button>
      </FocusTrapDialog>,
    );
    expect(screen.queryByRole('button', { name: '关闭Test dialog' })).not.toBeInTheDocument();
    fireEvent.keyDown(screen.getByRole('dialog'), { key: 'Escape' });
    expect(onClose).toHaveBeenCalledTimes(1);
  });

  it('restores the previously active element on unmount', async () => {
    const trigger = document.createElement('button');
    trigger.textContent = 'Open dialog';
    document.body.insertBefore(trigger, caller);
    trigger.focus();

    const view = renderDialog();
    await waitFor(() => expect(screen.getByRole('button', { name: 'Focusable action' })).toHaveFocus());
    view.unmount();

    expect(trigger).toHaveFocus();
    trigger.remove();
  });
});
