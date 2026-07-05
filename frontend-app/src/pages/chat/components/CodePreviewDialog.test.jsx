import React from 'react';
import { render, screen } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import { CodePreviewDialog } from './CodePreviewDialog.jsx';

function renderDialog(overrides = {}) {
  const preview = {
    open: true,
    loading: false,
    saving: false,
    filePath: '/repo/app/src/one-line.js',
    relative: 'src/one-line.js',
    content: 'const snippet = true;',
    draft: 'const snippet = true;',
    error: '',
    status: '',
    previewKind: 'text',
    previewMode: 'snippet',
    language: 'javascript',
    editable: true,
    editing: true,
    image: false,
    imageSrc: '',
    imageFullSrc: '',
    mediaType: '',
    sizeBytes: 21,
    startLine: 1,
    endLine: 1,
    totalLines: 1,
    ...overrides,
  };
  return render(
    <CodePreviewDialog
      preview={preview}
      renderMarkdownPreview={(content) => <div>{content}</div>}
      onBeginEdit={vi.fn()}
      onCancelEdit={vi.fn()}
      onChangeDraft={vi.fn()}
      onClose={vi.fn()}
      onDirtyClose={vi.fn()}
      onSave={vi.fn()}
    />,
  );
}

describe('CodePreviewDialog', () => {
  it('hides save controls for snippet preview mode even when editable is true', () => {
    renderDialog();

    expect(screen.queryByRole('button', { name: '保存预览更改' })).not.toBeInTheDocument();
  });
});
