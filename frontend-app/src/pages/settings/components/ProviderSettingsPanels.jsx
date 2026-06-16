import React from 'react';
import { Panel } from '../../shared/pageComponents.jsx';
import { SettingsPromptNotice } from './SettingsPromptNotice.jsx';
import './ProviderSettingsPanels.css';

const SUMMARY_MODE_OPTIONS = Object.freeze([
  ['detailed', 'detailed（详细摘要，推荐）'], ['auto', 'auto（自动）'], ['concise', 'concise（简洁）'], ['none', 'none（关闭）'],
]);
const APPROVAL_MODE_OPTIONS = Object.freeze([
  ['on-request', 'on-request（按需，默认）'], ['untrusted', 'untrusted（始终询问）'], ['on-failure', 'on-failure（失败后询问）'], ['never', 'never（全部放行）'],
]);

function ProviderSettingsPanel({ runtime, viewConfig }) {
  const { changeActiveProvider, form, saveProviderSettings, updateForm } = runtime;
  return (
    <Panel title="PROVIDER">
      <ProviderSettingsForm changeActiveProvider={changeActiveProvider} form={form} updateForm={updateForm} viewConfig={viewConfig} />
      <div className="settings-actions"><button className="btn btn-primary" type="button" onClick={() => void saveProviderSettings()}>保存 Provider 设置</button></div>
    </Panel>
  );
}

function ProviderSettingsForm({ changeActiveProvider, form, updateForm, viewConfig }) {
  const modelOptions = viewConfig.appendCurrentOption(
    viewConfig.modelOptionsByProvider[form.activeProvider] || viewConfig.modelOptionsByProvider.codex,
    form.providerModel,
  );
  const baseEffortOptions = viewConfig.effortModesByProvider[form.activeProvider] || viewConfig.effortModesByProvider.codex;
  const filteredEffortOptions = form.activeProvider === 'claude' && !viewConfig.isClaudeOpusFamilyModel(form.providerModel)
    ? baseEffortOptions.filter((item) => item.value !== 'max')
    : baseEffortOptions;
  const effortOptions = viewConfig.appendCurrentOption(
    filteredEffortOptions,
    viewConfig.normalizeProviderEffortSetting(form.activeProvider, form.providerModel, form.providerEffort),
  );
  return (
    <div className="form-grid">
      <label>Active Provider<select value={form.activeProvider} onChange={changeActiveProvider}><option value="codex">Codex</option></select></label>
      <label>Provider Model<select aria-label="Provider Model" value={form.providerModel} onChange={updateForm('providerModel')}>{modelOptions.map((item) => <option key={item.value} value={item.value}>{item.label}</option>)}</select></label>
      <label>Provider Effort<select aria-label="Provider Effort" value={form.providerEffort} onChange={updateForm('providerEffort')}>{effortOptions.map((item) => <option key={item.value} value={item.value}>{item.label}</option>)}</select></label>
      <label>Personality<select aria-label="Personality" value={form.personality} onChange={updateForm('personality')}>{viewConfig.appendCurrentOption(viewConfig.personalityOptions, form.personality).map((item) => <option key={item.value} value={item.value}>{item.label}</option>)}</select></label>
      {form.activeProvider === 'codex' ? <label>Codex Home<input aria-label="Codex Home" value={form.codexHome} onChange={updateForm('codexHome')} /></label> : null}
      {form.activeProvider === 'codex' ? <label>Instance Key<input aria-label="Instance Key" value={form.codexInstanceKey} onChange={updateForm('codexInstanceKey')} /></label> : null}
      <label>Sandbox Policy<select aria-label="Sandbox Policy" value={form.sandboxPolicy} onChange={updateForm('sandboxPolicy')}><option value="workspaceWrite">workspaceWrite</option><option value="readOnly">readOnly</option><option value="dangerFullAccess">dangerFullAccess</option></select></label>
      {form.sandboxPolicy === 'readOnly' ? <label>Read Only Mode<select aria-label="Read Only Mode" value={form.readOnlyMode} onChange={updateForm('readOnlyMode')}><option value="fullAccess">fullAccess（全量只读）</option><option value="restricted">restricted（限定目录）</option></select></label> : null}
      {form.sandboxPolicy === 'workspaceWrite' ? <label className="checkbox-line"><input type="checkbox" checked={form.networkAccess} onChange={updateForm('networkAccess')} /> Network Access</label> : null}
      {form.sandboxPolicy === 'workspaceWrite' ? <label className="wide">Writable Roots<textarea aria-label="Writable Roots" value={form.writableRoots} onChange={updateForm('writableRoots')} placeholder="每行一个绝对路径" /></label> : null}
      {form.sandboxPolicy === 'readOnly' && form.readOnlyMode === 'restricted' ? <label className="wide">Readable Roots<textarea aria-label="Readable Roots" value={form.readableRoots} onChange={updateForm('readableRoots')} placeholder="每行一个绝对路径" /></label> : null}
    </div>
  );
}

function ProviderPropertiesCard({ provider }) {
  return (
    <>
      <div className="section-header">PROPERTIES</div>
      <div className="data-card-vue" data-testid="settings-provider-sandbox-card">
        <ProviderSelectRow id="provider-summary-mode-select" label="推理摘要 (Summary)" value={provider.summaryMode} onChange={provider.setSummaryMode} options={SUMMARY_MODE_OPTIONS} />
        <ProviderSelectRow id="provider-approval-mode-select" label="审批策略 (ApprovalPolicy)" value={provider.approvalMode} onChange={provider.setApprovalMode} options={APPROVAL_MODE_OPTIONS} />
        {provider.notice.message ? <SettingsPromptNotice notice={provider.notice} className="settings-provider-notice" /> : null}
        <div className="settings-action-row settings-action-inline settings-provider-actions">
          <button type="button" className="btn btn-secondary btn-toolbar-sm" onClick={provider.load} disabled={provider.saving}>刷新</button>
          <button type="button" className="btn btn-primary btn-toolbar-sm" data-testid="provider-sandbox-save-button" onClick={provider.save} disabled={provider.saving}>{provider.saving ? '保存中...' : '保存'}</button>
        </div>
      </div>
    </>
  );
}

function ProviderSelectRow({ id, label, onChange, options, value }) {
  return (
    <div className="settings-stall-row settings-provider-control-row">
      <label className="settings-stall-label" htmlFor={id}>{label}</label>
      <select id={id} className="settings-stall-input settings-provider-select" data-testid={id} value={value} onChange={(event) => onChange(event.target.value)}>
        {options.map(([optionValue, optionLabel]) => <option key={optionValue} value={optionValue}>{optionLabel}</option>)}
      </select>
    </div>
  );
}

export { ProviderPropertiesCard, ProviderSettingsPanel };
