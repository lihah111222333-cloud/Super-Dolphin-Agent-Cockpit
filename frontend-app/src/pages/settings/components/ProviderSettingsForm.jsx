import React from 'react';

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
      <label>Provider Model<select aria-label="Provider Model" value={form.providerModel} onChange={updateForm('providerModel')}>{modelOptions.map((item) => <option key={item.value} value={item.value}>{item.label}</option>)}</select></label>
      <label>Provider Effort<select aria-label="Provider Effort" value={form.providerEffort} onChange={updateForm('providerEffort')}>{effortOptions.map((item) => <option key={item.value} value={item.value}>{item.label}</option>)}</select></label>
      <label>Personality<select aria-label="Personality" value={form.personality} onChange={updateForm('personality')}>{personalityOptions.map((item) => <option key={item.value} value={item.value}>{item.label}</option>)}</select></label>
      {form.activeProvider === 'codex' ? <label>Codex Home<input aria-label="Codex Home" value={form.codexHome} onChange={updateForm('codexHome')} /></label> : null}
      {form.activeProvider === 'codex' ? <label>Instance Key<input aria-label="Instance Key" value={form.codexInstanceKey} onChange={updateForm('codexInstanceKey')} /></label> : null}
      <label>Sandbox Policy<select aria-label="Sandbox Policy" value={form.sandboxPolicy} onChange={updateForm('sandboxPolicy')}><option value="workspaceWrite">workspaceWrite</option><option value="readOnly">readOnly</option><option value="dangerFullAccess">dangerFullAccess</option></select></label>
      {form.sandboxPolicy === 'readOnly' ? (
        <label>
          Read Only Mode
          <select aria-label="Read Only Mode" value={form.readOnlyMode} onChange={updateForm('readOnlyMode')}>
            <option value="fullAccess">{copy.provider.readOnlyFull}</option>
            <option value="restricted">{copy.provider.readOnlyRestricted}</option>
          </select>
        </label>
      ) : null}
      {form.sandboxPolicy === 'workspaceWrite' ? <label className="checkbox-line"><input type="checkbox" checked={form.networkAccess} onChange={updateForm('networkAccess')} /> Network Access</label> : null}
      {form.sandboxPolicy === 'workspaceWrite' ? <label className="wide">Writable Roots<textarea aria-label="Writable Roots" value={form.writableRoots} onChange={updateForm('writableRoots')} placeholder={copy.provider.rootPlaceholder} /></label> : null}
      {form.sandboxPolicy === 'readOnly' && form.readOnlyMode === 'restricted' ? <label className="wide">Readable Roots<textarea aria-label="Readable Roots" value={form.readableRoots} onChange={updateForm('readableRoots')} placeholder={copy.provider.rootPlaceholder} /></label> : null}
    </div>
  );
}

export { ProviderSettingsForm };
