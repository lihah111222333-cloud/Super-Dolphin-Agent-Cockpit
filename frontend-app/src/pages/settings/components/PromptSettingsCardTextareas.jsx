import React from 'react';

function PromptTextareas({ prompt, promptCopy }) {
  return (
    <>
      <div className="settings-prompt-meta">{promptCopy.stats.replace('{lines}', prompt.lineCount).replace('{chars}', prompt.charCount)}</div>
      <label className="settings-prompt-label" htmlFor="settings-lsp-effective-output">{promptCopy.effectiveContent}</label>
      <textarea id="settings-lsp-effective-output" className="settings-prompt-textarea settings-prompt-textarea-readonly" data-testid="settings-lsp-effective-output" rows={12} value={prompt.displayHint} readOnly />
      <label className="settings-prompt-label" htmlFor="settings-lsp-prompt-input">{promptCopy.customOverride}</label>
      <textarea
        id="settings-lsp-prompt-input"
        className="settings-prompt-textarea"
        data-testid="settings-lsp-prompt-input"
        rows={8}
        value={prompt.hint}
        onChange={(event) => prompt.setHint(event.target.value)}
        placeholder={prompt.defaultHint || promptCopy.placeholder}
        disabled={prompt.loading || prompt.saving}
      />
    </>
  );
}

export { PromptTextareas };
