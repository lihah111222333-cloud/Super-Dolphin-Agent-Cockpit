import React from 'react';
import { fireEvent, render, screen } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import { FocusTrapDialog } from './FocusTrapDialog.jsx';

describe('FocusTrapDialog', () => {
  it('renders dialog content and calls close on Escape', () => {
    const onClose = vi.fn();
    render(
      <FocusTrapDialog ariaLabel="Test dialog" onClose={onClose}>
        <button type="button">Focusable action</button>
      </FocusTrapDialog>,
    );

    fireEvent.keyDown(screen.getByRole('dialog'), { key: 'Escape' });
    expect(screen.getByRole('dialog', { name: 'Test dialog' }).tagName).toBe('DIALOG');
    expect(onClose).toHaveBeenCalledTimes(1);
  });
});
