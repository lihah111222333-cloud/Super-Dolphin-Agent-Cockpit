import React from 'react';
import { fireEvent, render, screen } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import { ComposerTextarea } from './ComposerTextarea.jsx';

describe('ComposerTextarea', () => {
  it('renders the current draft and forwards input events', () => {
    const onChange = vi.fn();
    const onPaste = vi.fn();
    const onCompositionStart = vi.fn();
    const onCompositionEnd = vi.fn();
    const onKeyDown = vi.fn();

    render(
      <ComposerTextarea
        ariaActiveDescendant="slash-option-review"
        ariaControls="slash-listbox"
        ariaExpanded
        draft="hello"
        onChange={onChange}
        onPaste={onPaste}
        onCompositionStart={onCompositionStart}
        onCompositionEnd={onCompositionEnd}
        onKeyDown={onKeyDown}
      />,
    );

    const textarea = screen.getByRole('combobox', { name: '输入给 Agent 的内容' });
    expect(textarea).toHaveValue('hello');
    expect(textarea).toHaveAttribute('data-file-drop-target', '');
    expect(textarea).toHaveAttribute('id', 'composer-input');
    expect(textarea).toHaveAttribute('aria-controls', 'slash-listbox');
    expect(textarea).toHaveAttribute('aria-expanded', 'true');
    expect(textarea).toHaveAttribute('aria-activedescendant', 'slash-option-review');
    expect(textarea).toHaveAttribute('aria-autocomplete', 'list');
    expect(textarea).toHaveAttribute('aria-haspopup', 'listbox');

    fireEvent.change(textarea, { target: { value: 'hello world' } });
    fireEvent.paste(textarea);
    fireEvent.compositionStart(textarea);
    fireEvent.compositionEnd(textarea);
    fireEvent.keyDown(textarea, { key: 'Enter' });

    expect(onChange).toHaveBeenCalledTimes(1);
    expect(onPaste).toHaveBeenCalledTimes(1);
    expect(onCompositionStart).toHaveBeenCalledTimes(1);
    expect(onCompositionEnd).toHaveBeenCalledTimes(1);
    expect(onKeyDown).toHaveBeenCalledTimes(1);
  });
});
