import React from 'react';
import { CodePreviewMarkdown } from '../markdown/MarkdownMessage.jsx';
import { CodePreviewDialog } from '../runtime/CodePreviewDialog.jsx';
import { PathChoiceDialog } from '../runtime/PathChoiceDialog.jsx';
import { emptyPathChoiceState } from '../adapters/runtimeCodeAdapter.js';

function renderCodePreviewMarkdown(content) {
  return <CodePreviewMarkdown content={content} />;
}

function CodePreviewControllerDialogs({
  codePreview,
  closeCodePreview,
  onChangeDraft,
  onDirtyClose,
  openChosenPath,
  pathChoice,
  savePreviewChanges,
  setCodePreview,
  setPathChoice,
}) {
  return (
    <>
      {codePreview.open ? (
        <CodePreviewDialog
          preview={codePreview}
          renderMarkdownPreview={renderCodePreviewMarkdown}
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

export { CodePreviewControllerDialogs };
