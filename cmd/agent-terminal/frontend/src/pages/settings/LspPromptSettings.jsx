import React from 'react';
import { useVueSetup, val } from '../../utils/vue-compat.js';
import { LspPromptSettings as VueComp } from './LspPromptSettings.ts';

export function LspPromptSettings(props) {
  const emit = (event, ...args) => {
    const propName = 'on' + event.split('-').map(s => s[0].toUpperCase() + s.slice(1)).join('');
    props[propName]?.(...args);
  };
  const vm = useVueSetup(VueComp.setup, props, emit);

  const lspPromptHint = val(vm.lspPromptHint);
  const lspPromptDefaultHint = val(vm.lspPromptDefaultHint);
  const lspPromptUsingDefault = val(vm.lspPromptUsingDefault);
  const lspPromptLoading = val(vm.lspPromptLoading);
  const lspPromptSaving = val(vm.lspPromptSaving);
  const lspPromptNotice = vm.lspPromptNotice || {};
  const showInjectedPromptInChat = val(vm.showInjectedPromptInChat);
  const showInjectedPromptSaving = val(vm.showInjectedPromptSaving);
  const currentScopeCwd = val(vm.currentScopeCwd);
  const lspPromptDisplayHint = val(vm.lspPromptDisplayHint);
  const lspPromptLineCount = val(vm.lspPromptLineCount);
  const lspPromptCharCount = val(vm.lspPromptCharCount);

  let lspStatusText = '自定义覆盖';
  if (lspPromptLoading) {
    lspStatusText = '加载中...';
  } else if (lspPromptUsingDefault) {
    lspStatusText = '默认注入';
  }

  return (
    <>
      <div className="section-header">PROMPT</div>
      <div className="data-card-vue settings-prompt-card" data-testid="settings-lsp-prompt-card">
        <div className="data-row-vue">
          <strong>自动注入提示词（LSP / Playwright / json-render / code_run）</strong>
          <span>{lspStatusText}</span>
        </div>
        <div className="settings-prompt-desc">下方“生效内容”是后端每轮实际注入文本；“覆盖编辑”用于调试，留空保存可恢复默认。</div>
        <div className="settings-prompt-meta" data-testid="settings-lsp-effective-cwd">
          当前作用 CWD：{currentScopeCwd || '未知'}
        </div>
        <label className="settings-prompt-toggle" data-testid="settings-show-injected-toggle">
          <div className="settings-prompt-toggle-copy">
            <span className="settings-prompt-toggle-title">聊天区显示自动注入内容（调试）</span>
            <span className="settings-prompt-toggle-desc">开启后将保留每轮消息里的“已注入 ...”段。</span>
          </div>
          <input
            type="checkbox"
            className="settings-prompt-toggle-input"
            data-testid="settings-show-injected-toggle-input"
            checked={showInjectedPromptInChat}
            disabled={lspPromptLoading || showInjectedPromptSaving}
            onChange={(e) => {
              vm.showInjectedPromptInChat.value = e.target.checked;
              vm.saveInjectedPromptVisibility();
            }}
          />
        </label>
        <div className="settings-prompt-meta">生效行数 {lspPromptLineCount} · 字符 {lspPromptCharCount}</div>
        <div className="settings-prompt-label">当前生效内容（只读）</div>
        <textarea
          className="settings-prompt-textarea settings-prompt-textarea-readonly"
          data-testid="settings-lsp-effective-output"
          rows={12}
          value={lspPromptDisplayHint}
          readOnly
        ></textarea>
        <div className="settings-prompt-label">自定义覆盖（可编辑，空=默认）</div>
        <textarea
          className="settings-prompt-textarea"
          data-testid="settings-lsp-prompt-input"
          rows={8}
          value={lspPromptHint}
          placeholder={lspPromptDefaultHint || '请输入提示词'}
          disabled={lspPromptLoading || lspPromptSaving}
          onChange={(e) => { vm.lspPromptHint.value = e.target.value; }}
        ></textarea>
        {lspPromptNotice.message && (
          <div className={`settings-prompt-notice is-${lspPromptNotice.level}`} data-testid="settings-lsp-prompt-notice">
            {lspPromptNotice.message}
          </div>
        )}
        <div className="settings-action-row settings-action-inline">
          <button className="btn btn-secondary btn-toolbar-sm" data-testid="settings-lsp-refresh-button" onClick={vm.loadLSPPromptHint} disabled={lspPromptSaving}>刷新</button>
          <button className="btn btn-secondary btn-xs" data-testid="settings-lsp-copy-button" onClick={vm.copyEffectivePromptHint} disabled={lspPromptLoading || lspPromptSaving}>复制生效提示词</button>
          <button className="btn btn-secondary btn-xs" data-testid="settings-lsp-reset-button" onClick={vm.resetLSPPromptHint} disabled={lspPromptLoading || lspPromptSaving}>恢复默认</button>
          <button className="btn btn-primary btn-toolbar-sm" data-testid="settings-lsp-save-button" onClick={vm.saveLSPPromptHint} disabled={lspPromptLoading || lspPromptSaving}>
            {lspPromptSaving ? '保存中...' : '保存提示词'}
          </button>
        </div>
      </div>
    </>
  );
}
