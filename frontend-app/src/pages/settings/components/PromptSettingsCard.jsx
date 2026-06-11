import React from 'react';
import { SettingsPromptNotice } from './SettingsPromptNotice.jsx';

function PromptSettingsCard({ prompt }) {
  return (
    <>
      <div className="section-header">PROMPT</div>
      <div className="data-card-vue settings-prompt-card" data-testid="settings-lsp-prompt-card">
        <PromptSummary prompt={prompt} />
        <PromptVisibilityToggle prompt={prompt} />
        <PromptTextareas prompt={prompt} />
        {prompt.notice.message ? <SettingsPromptNotice notice={prompt.notice} testId="settings-lsp-prompt-notice" /> : null}
        <PromptActions prompt={prompt} />
      </div>
    </>
  );
}

function PromptSummary({ prompt }) {
  return (
    <>
      <div className="data-row-vue"><strong>自动注入提示词 (LSP / Playwright / json-render)</strong><span>{prompt.modeLabel}</span></div>
      <div className="settings-prompt-desc">下方“生效内容”是后端每轮实际注入文本：“覆盖编辑”用于调试，留空保存可恢复默认。</div>
      <div className="settings-prompt-meta" data-testid="settings-lsp-effective-cwd">当前作用 CWD: {prompt.currentScopeCwd || '未知'}</div>
    </>
  );
}

function PromptVisibilityToggle({ prompt }) {
  return (
    <label className="settings-prompt-toggle" data-testid="settings-show-injected-toggle">
      <div className="settings-prompt-toggle-copy"><span className="settings-prompt-toggle-title">聊天区显示自动注入内容（调试）</span><span className="settings-prompt-toggle-desc">开启后将保留首发消息里的“已注入 ...”段。</span></div>
      <input type="checkbox" className="settings-prompt-toggle-input" data-testid="settings-show-injected-toggle-input" checked={prompt.showInjected} onChange={prompt.toggleVisibility} disabled={prompt.loading || prompt.showInjectedSaving} />
    </label>
  );
}

function PromptTextareas({ prompt }) {
  return (
    <>
      <div className="settings-prompt-meta">生效行数 {prompt.lineCount} · 字符 {prompt.charCount}</div>
      <label className="settings-prompt-label" htmlFor="settings-lsp-effective-output">当前生效内容（只读）</label>
      <textarea id="settings-lsp-effective-output" className="settings-prompt-textarea settings-prompt-textarea-readonly" data-testid="settings-lsp-effective-output" rows={12} value={prompt.displayHint} readOnly />
      <label className="settings-prompt-label" htmlFor="settings-lsp-prompt-input">自定义覆盖（可编辑，空=默认）</label>
      <textarea id="settings-lsp-prompt-input" className="settings-prompt-textarea" data-testid="settings-lsp-prompt-input" rows={8} value={prompt.hint} onChange={(event) => prompt.setHint(event.target.value)} placeholder={prompt.defaultHint || '请输入提示词'} disabled={prompt.loading || prompt.saving} />
    </>
  );
}

function PromptActions({ prompt }) {
  return (
    <div className="settings-action-row settings-action-inline">
      <button type="button" className="btn btn-secondary btn-toolbar-sm" data-testid="settings-lsp-refresh-button" onClick={prompt.loadPrompt} disabled={prompt.saving}>刷新</button>
      <button type="button" className="btn btn-secondary btn-toolbar-sm" data-testid="settings-lsp-copy-button" onClick={prompt.copy} disabled={prompt.loading || prompt.saving}>复制生效提示词</button>
      <button type="button" className="btn btn-secondary btn-toolbar-sm" data-testid="settings-lsp-reset-button" onClick={prompt.reset} disabled={prompt.loading || prompt.saving}>恢复默认</button>
      <button type="button" className="btn btn-primary btn-toolbar-sm" data-testid="settings-lsp-save-button" onClick={prompt.save} disabled={prompt.loading || prompt.saving}>{prompt.saving ? '保存中...' : '保存提示词'}</button>
    </div>
  );
}

export { PromptSettingsCard };
