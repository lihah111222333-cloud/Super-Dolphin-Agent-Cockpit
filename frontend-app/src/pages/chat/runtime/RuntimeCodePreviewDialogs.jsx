import React from 'react';
import { CodePreviewDialog } from './CodePreviewDialog.jsx';
import { PathChoiceDialog } from './PathChoiceDialog.jsx';
import { emptyPathChoiceState } from '../adapters/runtimeCodeAdapter.js';

function RuntimeCodePreviewDialogs({
  codePreview,
  closeCodePreview,
  onChangeDraft,
  onDirtyClose,
  openChosenPath,
  pathChoice,
  renderMarkdownPreview,
  savePreviewChanges,
  setCodePreview,
  setPathChoice,
}) {
  return (
    <>
      {codePreview.open ? (
        <CodePreviewDialog
          preview={codePreview}
          renderMarkdownPreview={renderMarkdownPreview}
          onBeginEdit={() => setCodePreview((current) => ({ ...current, editing: true, error: '', status: '' }))}
          onCancelEdit={() => setCodePreview((current) => ({ ...current, editing: false, draft: current.content, error: '', status: '' }))}
          onChangeDraft={onChangeDraft}
          onClose={closeCodePreview}
          onDirtyClose={onDirtyClose}
          onSave={savePreviewChanges}
        />
      ) : null}
      {pathChoice.open ? (
        <PathChoiceDialog
          choice={pathChoice}
          onClose={() => setPathChoice(emptyPathChoiceState())}
          onSelect={(filePath) => { void openChosenPath(filePath); }}
        />
      ) : null}
    </>
  );
}

export { RuntimeCodePreviewDialogs };
