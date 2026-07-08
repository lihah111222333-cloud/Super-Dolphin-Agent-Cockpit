import React from 'react';
import { Panel } from '../../shared/pageComponents.jsx';
import { SettingsPromptNotice } from './SettingsPromptNotice.jsx';
import './ProviderSettingsPanels.css';

function ProviderSettingsPanel({ copy, runtime, viewConfig }) {
  const { changeActiveProvider, form, saveProviderSettings, updateForm } = runtime;
  return (
    <Panel title="PROVIDER">
      <div data-testid="settings-provider-runtime-card">
        <ProviderSettingsForm changeActiveProvider={changeActiveProvider} copy={copy} form={form} updateForm={updateForm} viewConfig={viewConfig} />
        <div className="settings-actions">
          <button className="btn btn-primary" type="button" data-testid="settings-provider-save-button" onClick={() => void saveProviderSettings()}>{copy.provider.saveSettings}</button>
        </div>
      </div>
    </Panel>
  );
}

function ProviderSettingsForm({ changeActiveProvider, copy, form, updateForm, viewConfig }) {
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
  ).map((item) => ({ ...item, label: copy.provider.effortOptions[item.value] || item.label }));
  const personalityOptions = viewConfig.appendCurrentOption(viewConfig.personalityOptions, form.personality)
    .map((item) => ({ ...item, label: copy.provider.personalityOptions[item.value] || item.label }));
  return (
    <div className="form-grid">
      <label>Active Provider<select value={form.activeProvider} onChange={changeActiveProvider}><option value="codex">Codex</option></select></label>
      <label>Provider Model<select aria-label="Provider Model" data-testid="settings-provider-model" value={form.providerModel} onChange={updateForm('providerModel')}>{modelOptions.map((item) => <option key={item.value} value={item.value}>{item.label}</option>)}</select></label>
      <label>Provider Effort<select aria-label="Provider Effort" data-testid="settings-provider-effort" value={form.providerEffort} onChange={updateForm('providerEffort')}>{effortOptions.map((item) => <option key={item.value} value={item.value}>{item.label}</option>)}</select></label>
      <label>Personality<select aria-label="Personality" data-testid="settings-provider-personality" value={form.personality} onChange={updateForm('personality')}>{personalityOptions.map((item) => <option key={item.value} value={item.value}>{item.label}</option>)}</select></label>
      {form.activeProvider === 'codex' ? <label>Codex Home<input aria-label="Codex Home" data-testid="settings-provider-codex-home" value={form.codexHome} onChange={updateForm('codexHome')} /></label> : null}
      {form.activeProvider === 'codex' ? <label>Instance Key<input aria-label="Instance Key" data-testid="settings-provider-instance-key" value={form.codexInstanceKey} onChange={updateForm('codexInstanceKey')} /></label> : null}
      <label>Sandbox Policy<select aria-label="Sandbox Policy" value={form.sandboxPolicy} onChange={updateForm('sandboxPolicy')}><option value="workspaceWrite">workspaceWrite</option><option value="readOnly">readOnly</option><option value="dangerFullAccess">dangerFullAccess</option></select></label>
      {form.sandboxPolicy === 'readOnly' ? <label>Read Only Mode<select aria-label="Read Only Mode" value={form.readOnlyMode} onChange={updateForm('readOnlyMode')}><option value="fullAccess">{copy.provider.readOnlyFull}</option><option value="restricted">{copy.provider.readOnlyRestricted}</option></select></label> : null}
      {form.sandboxPolicy === 'workspaceWrite' ? <label className="checkbox-line"><input type="checkbox" data-testid="settings-provider-network-access" checked={form.networkAccess} onChange={updateForm('networkAccess')} /> Network Access</label> : null}
      {form.sandboxPolicy === 'workspaceWrite' ? <label className="wide">Writable Roots<textarea aria-label="Writable Roots" data-testid="settings-provider-writable-roots" value={form.writableRoots} onChange={updateForm('writableRoots')} /></label> : null}
      {form.sandboxPolicy === 'readOnly' && form.readOnlyMode === 'restricted' ? <label className="wide">Readable Roots<textarea aria-label="Readable Roots" value={form.readableRoots} onChange={updateForm('readableRoots')} placeholder={copy.provider.rootPlaceholder} /></label> : null}
    </div>
  );
}

function ProviderPropertiesCard({ copy, provider }) {
  const providerCopy = copy.provider;
  return (
    <>
      <div className="section-header">{providerCopy.properties}</div>
      <div className="data-card-vue" data-testid="settings-provider-sandbox-card">
        <ProviderSelectRow id="provider-summary-mode-select" label={providerCopy.summary} value={provider.summaryMode} onChange={provider.setSummaryMode} options={providerOptions(providerCopy.summaryOptions)} />
        <ProviderSelectRow id="provider-approval-mode-select" label={providerCopy.approval} value={provider.approvalMode} onChange={provider.setApprovalMode} options={providerOptions(providerCopy.approvalOptions)} />
        {provider.notice.message ? <SettingsPromptNotice notice={provider.notice} className="settings-provider-notice" /> : null}
        <div className="settings-action-row settings-action-inline settings-provider-actions">
          <button type="button" className="btn btn-secondary btn-toolbar-sm" onClick={provider.load} disabled={provider.saving}>{providerCopy.refresh}</button>
          <button type="button" className="btn btn-primary btn-toolbar-sm" data-testid="provider-sandbox-save-button" onClick={provider.save} disabled={provider.saving}>{provider.saving ? providerCopy.saving : providerCopy.save}</button>
        </div>
      </div>
    </>
  );
}

function providerOptions(options) {
  return Object.entries(options);
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
