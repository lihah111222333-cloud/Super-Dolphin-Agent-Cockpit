import React from 'react';
import { envStatusLabel, vendorStatusLabel } from './ModelProvidersCardModel.js';

function ModelProviderList({ activeVendorId, modelCopy, onSelect, selectedVendor, vendors }) {
  return (
    <div className="settings-model-provider-list" aria-label={modelCopy.vendorList}>
      {vendors.map((vendor) => (
        <button
          type="button"
          key={vendor.id}
          className={vendor.id === selectedVendor?.id ? 'is-selected' : ''}
          onClick={() => onSelect(vendor.id)}
        >
          <strong>{vendor.label || vendor.id}</strong>
          <span>{vendorStatusLabel(vendor, activeVendorId, modelCopy)}</span>
        </button>
      ))}
    </div>
  );
}

function ModelProviderActions(props) {
  const { applying, canApply, canSave, loading, modelCopy, onApply, onLoad, onSave, saving } = props;
  const busy = loading || saving || applying;
  return (
    <div className="settings-action-row settings-action-inline settings-provider-actions">
      <button type="button" className="btn btn-secondary btn-toolbar-sm" onClick={onLoad} disabled={busy}>
        {loading ? modelCopy.loading : modelCopy.refresh}
      </button>
      <button type="button" className="btn btn-primary btn-toolbar-sm" onClick={onSave} disabled={busy || !canSave}>
        {saving ? modelCopy.saving : modelCopy.save}
      </button>
      <button type="button" className="btn btn-primary btn-toolbar-sm" onClick={onApply} disabled={busy || !canApply}>
        {applying ? modelCopy.applying : modelCopy.apply}
      </button>
    </div>
  );
}

function ModelProviderDetail(props) {
  const { disabled, modelCopy, onChange, onNestedChange, vendor, vendors } = props;
  if (!vendor) return <div className="settings-log-empty">{modelCopy.empty}</div>;
  return (
    <div className="settings-model-provider-detail">
      <div className="data-row-vue">
        <strong>{vendor.label || vendor.id}</strong>
        <span>{envStatusLabel(vendor, modelCopy)}</span>
      </div>
      <div className="data-row-vue">
        <strong>{modelCopy.envKey}</strong>
        <span>{vendor.envKey || modelCopy.none}</span>
      </div>
      <p className="settings-provider-note">{modelCopy.envOnly}</p>
      <div className="form-grid">
        <label className="checkbox-line"><input type="checkbox" checked={Boolean(vendor.enabled)} onChange={(event) => onChange('enabled', event.target.checked)} disabled={disabled} /> {modelCopy.enabled}</label>
        <label>{modelCopy.baseURL}<input value={vendor.baseURL} onChange={(event) => onChange('baseURL', event.target.value)} disabled={disabled} /></label>
        <label>{modelCopy.envKey}<input value={vendor.envKey} onChange={(event) => onChange('envKey', event.target.value)} disabled={disabled} /></label>
        <label>{modelCopy.codexModelProvider}<input value={vendor.codexModelProvider} onChange={(event) => onChange('codexModelProvider', event.target.value)} disabled={disabled} /></label>
        <label>{modelCopy.defaultModel}<input aria-label={modelCopy.defaultModel} value={vendor.defaultModel} onChange={(event) => onChange('defaultModel', event.target.value)} disabled={disabled} /></label>
        <label>{modelCopy.codexHome}<input value={vendor.codexHome} onChange={(event) => onChange('codexHome', event.target.value)} disabled={disabled} /></label>
        <label>{modelCopy.codexInstanceKey}<input value={vendor.codexInstanceKey} onChange={(event) => onChange('codexInstanceKey', event.target.value)} disabled={disabled} /></label>
        <label>{modelCopy.dailyBudget}<input type="number" value={vendor.budget.dailyUsd} onChange={(event) => onNestedChange('budget', 'dailyUsd', event.target.value)} disabled={disabled} /></label>
        <label>{modelCopy.monthlyBudget}<input type="number" value={vendor.budget.monthlyUsd} onChange={(event) => onNestedChange('budget', 'monthlyUsd', event.target.value)} disabled={disabled} /></label>
        <label>{modelCopy.tokenPriority}<input type="number" value={vendor.tokenPool.priority} onChange={(event) => onNestedChange('tokenPool', 'priority', event.target.value)} disabled={disabled} /></label>
        <label>{modelCopy.fallbackVendor}<select value={vendor.tokenPool.fallbackVendorId} onChange={(event) => onNestedChange('tokenPool', 'fallbackVendorId', event.target.value)} disabled={disabled}>
          <option value="">{modelCopy.none}</option>
          {vendors.map((item) => (item.id === vendor.id ? null : <option key={item.id} value={item.id}>{item.label || item.id}</option>))}
        </select></label>
      </div>
    </div>
  );
}

export { ModelProviderActions, ModelProviderDetail, ModelProviderList };
