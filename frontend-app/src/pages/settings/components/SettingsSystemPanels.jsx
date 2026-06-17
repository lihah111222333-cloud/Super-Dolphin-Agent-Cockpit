import React from 'react';
import { Panel } from '../../shared/pageComponents.jsx';
import { SettingsPromptNotice } from './SettingsPromptNotice.jsx';

function AboutPanel({ buildInfo, copy, cwd, runtime, updateCurrentVersion }) {
  const canInstallUpdate = Boolean(runtime.updateInfo?.available) && !runtime.updateInstalling;
  const aboutCopy = copy.about;
  const updateCopy = copy.update;
  return (
    <Panel title="ABOUT">
      <dl>
        <dt>{aboutCopy.version}</dt><dd>Agent Orchestrator {buildInfo?.version || 'unknown'}</dd>
        <dt>{aboutCopy.runtime}</dt><dd>{buildInfo?.runtime || 'unknown'}</dd>
        <dt>{aboutCopy.buildTime}</dt><dd>{buildInfo?.buildTime || 'unknown'}</dd>
        <dt>{aboutCopy.commit}</dt><dd>{buildInfo?.commit || 'unknown'}</dd>
        <dt>{aboutCopy.currentProject}</dt><dd>{cwd || copy.noProject}</dd>
      </dl>
      <div className="data-card-vue settings-update-card" data-testid="settings-update-card">
        <div className="data-row-vue">
          <strong>{updateCopy.title}</strong>
          <span>{updateCopy.currentVersion} {updateCurrentVersion}</span>
        </div>
        <div className="settings-action-row settings-action-inline">
          <button className="btn btn-secondary btn-toolbar-sm" type="button" data-testid="settings-update-check-button" onClick={() => void runtime.checkForUpdate()} disabled={runtime.updateBusy || runtime.updateInstalling}>{runtime.updateBusy ? updateCopy.checking : updateCopy.check}</button>
          {canInstallUpdate ? <button className="btn btn-primary btn-toolbar-sm" type="button" data-testid="settings-update-install-button" onClick={() => void runtime.installUpdate()} disabled={runtime.updateInstalling}>{updateCopy.install}</button> : null}
        </div>
        {runtime.updateNotice.message ? <SettingsPromptNotice notice={runtime.updateNotice} testId="settings-update-notice" /> : null}
      </div>
    </Panel>
  );
}

function RuntimeSettingsPanels({ copy, runtime }) {
  const { form, saveRuntimeSettings, updateForm } = runtime;
  const runtimeCopy = copy.runtime;
  return (
    <>
      <Panel title="TURN TRACKER">
        <div className="form-line">
          <label>{runtimeCopy.stallThreshold}<input aria-label={runtimeCopy.stallThreshold} data-testid="settings-stall-threshold-input" type="number" min="30" value={form.stallThresholdSec} onChange={updateForm('stallThresholdSec')} /> {runtimeCopy.seconds}</label>
          <button className="btn btn-primary" type="button" data-testid="settings-stall-threshold-save-button" onClick={() => void saveRuntimeSettings()}>{runtimeCopy.saveTimeout}</button>
        </div>
      </Panel>
      <ContextUsagePanel form={form} runtimeCopy={runtimeCopy} onSave={saveRuntimeSettings} updateForm={updateForm} />
    </>
  );
}

function ContextUsagePanel({ form, onSave, runtimeCopy, updateForm }) {
  return (
    <Panel title="CONTEXT USAGE ALERT" data-testid="settings-ctx-thresholds-card">
      <div className="form-line">
        <label>{runtimeCopy.warnThreshold}<input aria-label={runtimeCopy.warnThreshold} type="number" min="1" max="100" value={form.contextWarn} onChange={updateForm('contextWarn')} /></label>
        <label>{runtimeCopy.dangerThreshold}<input aria-label={runtimeCopy.dangerThreshold} type="number" min="1" max="100" value={form.contextDanger} onChange={updateForm('contextDanger')} /></label>
        <label>{runtimeCopy.criticalThreshold}<input aria-label={runtimeCopy.criticalThreshold} type="number" min="1" max="100" value={form.contextCritical} onChange={updateForm('contextCritical')} /></label>
        <button className="btn btn-primary" type="button" data-testid="settings-ctx-thresholds-save-button" onClick={() => void onSave()}>{runtimeCopy.saveRuntimeThresholds}</button>
      </div>
    </Panel>
  );
}

export { AboutPanel, RuntimeSettingsPanels };
