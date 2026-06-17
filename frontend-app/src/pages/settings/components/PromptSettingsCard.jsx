import React from 'react';
import { SettingsPromptNotice } from './SettingsPromptNotice.jsx';
import './PromptSettingsCard.css';
import './SettingsPromptToggle.css';

function PromptSettingsCard({ copy, prompt }) {
  const promptCopy = copy.promptCard;
  return (
    <>
      <div className="section-header">{promptCopy.title}</div>
      <div className="data-card-vue settings-prompt-card" data-testid="settings-lsp-prompt-card">
        <PromptSummary prompt={prompt} promptCopy={promptCopy} />
        <PromptVisibilityToggle prompt={prompt} promptCopy={promptCopy} />
        <PromptTextareas prompt={prompt} promptCopy={promptCopy} />
        {prompt.notice.message ? <SettingsPromptNotice notice={prompt.notice} testId="settings-lsp-prompt-notice" /> : null}
        <PromptActions prompt={prompt} promptCopy={promptCopy} />
      </div>
    </>
  );
}

function PromptSummary({ prompt, promptCopy }) {
  return (
    <>
      <div className="data-row-vue"><strong>{promptCopy.autoInject}</strong><span>{prompt.modeLabel}</span></div>
      <div className="settings-prompt-desc">{promptCopy.description}</div>
      <div className="settings-prompt-meta" data-testid="settings-lsp-effective-cwd">{promptCopy.currentCwd} {prompt.currentScopeCwd || promptCopy.unknown}</div>
    </>
  );
}

function PromptVisibilityToggle({ prompt, promptCopy }) {
  return (
    <label className="settings-prompt-toggle" data-testid="settings-show-injected-toggle">
      <div className="settings-prompt-toggle-copy"><span className="settings-prompt-toggle-title">{promptCopy.showInjectedTitle}</span><span className="settings-prompt-toggle-desc">{promptCopy.showInjectedDesc}</span></div>
      <input type="checkbox" className="settings-prompt-toggle-input" data-testid="settings-show-injected-toggle-input" checked={prompt.showInjected} onChange={prompt.toggleVisibility} disabled={prompt.loading || prompt.showInjectedSaving} />
    </label>
  );
}

function PromptTextareas({ prompt, promptCopy }) {
  return (
    <>
      <div className="settings-prompt-meta">{promptCopy.stats.replace('{lines}', prompt.lineCount).replace('{chars}', prompt.charCount)}</div>
      <label className="settings-prompt-label" htmlFor="settings-lsp-effective-output">{promptCopy.effectiveContent}</label>
      <textarea id="settings-lsp-effective-output" className="settings-prompt-textarea settings-prompt-textarea-readonly" data-testid="settings-lsp-effective-output" rows={12} value={prompt.displayHint} readOnly />
      <label className="settings-prompt-label" htmlFor="settings-lsp-prompt-input">{promptCopy.customOverride}</label>
      <textarea id="settings-lsp-prompt-input" className="settings-prompt-textarea" data-testid="settings-lsp-prompt-input" rows={8} value={prompt.hint} onChange={(event) => prompt.setHint(event.target.value)} placeholder={prompt.defaultHint || promptCopy.placeholder} disabled={prompt.loading || prompt.saving} />
    </>
  );
}

function PromptActions({ prompt, promptCopy }) {
  return (
    <div className="settings-action-row settings-action-inline">
      <button type="button" className="btn btn-secondary btn-toolbar-sm" data-testid="settings-lsp-refresh-button" onClick={prompt.loadPrompt} disabled={prompt.saving}>{promptCopy.refresh}</button>
      <button type="button" className="btn btn-secondary btn-toolbar-sm" data-testid="settings-lsp-copy-button" onClick={prompt.copy} disabled={prompt.loading || prompt.saving}>{promptCopy.copy}</button>
      <button type="button" className="btn btn-secondary btn-toolbar-sm" data-testid="settings-lsp-reset-button" onClick={prompt.reset} disabled={prompt.loading || prompt.saving}>{promptCopy.reset}</button>
      <button type="button" className="btn btn-primary btn-toolbar-sm" data-testid="settings-lsp-save-button" onClick={prompt.save} disabled={prompt.loading || prompt.saving}>{prompt.saving ? promptCopy.saving : promptCopy.save}</button>
    </div>
  );
}

export { PromptSettingsCard };
