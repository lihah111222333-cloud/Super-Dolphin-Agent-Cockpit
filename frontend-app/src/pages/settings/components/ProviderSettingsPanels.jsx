import React from 'react';
import { ProviderSettingsForm } from './ProviderSettingsForm.jsx';
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
