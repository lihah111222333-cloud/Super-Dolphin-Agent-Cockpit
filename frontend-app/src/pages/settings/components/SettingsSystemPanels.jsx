import React from 'react';
import { Panel } from '../../shared/pageComponents.jsx';
import { SettingsPromptNotice } from './SettingsPromptNotice.jsx';

function AboutPanel({ buildInfo, cwd, runtime, updateCurrentVersion }) {
  const canInstallUpdate = Boolean(runtime.updateInfo?.available) && !runtime.updateInstalling;
  return (
    <Panel title="ABOUT">
      <dl>
        <dt>版本</dt><dd>Agent Orchestrator {buildInfo?.version || 'unknown'}</dd>
        <dt>运行时</dt><dd>{buildInfo?.runtime || 'unknown'}</dd>
        <dt>构建时间</dt><dd>{buildInfo?.buildTime || 'unknown'}</dd>
        <dt>Commit</dt><dd>{buildInfo?.commit || 'unknown'}</dd>
        <dt>当前项目</dt><dd>{cwd || '未选择项目'}</dd>
      </dl>
      <div className="data-card-vue settings-update-card" data-testid="settings-update-card">
        <div className="data-row-vue">
          <strong>应用更新</strong>
          <span>当前版本 {updateCurrentVersion}</span>
        </div>
        <div className="settings-action-row settings-action-inline">
          <button className="btn btn-secondary btn-toolbar-sm" type="button" data-testid="settings-update-check-button" onClick={() => void runtime.checkForUpdate()} disabled={runtime.updateBusy || runtime.updateInstalling}>{runtime.updateBusy ? '检查中...' : '检查更新'}</button>
          {canInstallUpdate ? <button className="btn btn-primary btn-toolbar-sm" type="button" data-testid="settings-update-install-button" onClick={() => void runtime.installUpdate()} disabled={runtime.updateInstalling}>安装更新</button> : null}
        </div>
        {runtime.updateNotice.message ? <SettingsPromptNotice notice={runtime.updateNotice} testId="settings-update-notice" /> : null}
      </div>
    </Panel>
  );
}

function RuntimeSettingsPanels({ runtime }) {
  const { form, saveRuntimeSettings, updateForm } = runtime;
  return (
    <>
      <Panel title="TURN TRACKER">
        <div className="form-line">
          <label>统一超时阈值<input aria-label="统一超时阈值" data-testid="settings-stall-threshold-input" type="number" min="30" value={form.stallThresholdSec} onChange={updateForm('stallThresholdSec')} /> 秒</label>
          <button className="btn btn-primary" type="button" data-testid="settings-stall-threshold-save-button" onClick={() => void saveRuntimeSettings()}>保存超时阈值</button>
        </div>
      </Panel>
      <ContextUsagePanel form={form} onSave={saveRuntimeSettings} updateForm={updateForm} />
    </>
  );
}

function ContextUsagePanel({ form, onSave, updateForm }) {
  return (
    <Panel title="CONTEXT USAGE ALERT" data-testid="settings-ctx-thresholds-card">
      <div className="form-line">
        <label>Warn 阈值<input aria-label="Warn 阈值" type="number" min="1" max="100" value={form.contextWarn} onChange={updateForm('contextWarn')} /></label>
        <label>Danger 阈值<input aria-label="Danger 阈值" type="number" min="1" max="100" value={form.contextDanger} onChange={updateForm('contextDanger')} /></label>
        <label>Critical 阈值<input aria-label="Critical 阈值" type="number" min="1" max="100" value={form.contextCritical} onChange={updateForm('contextCritical')} /></label>
        <button className="btn btn-primary" type="button" data-testid="settings-ctx-thresholds-save-button" onClick={() => void onSave()}>保存运行阈值</button>
      </div>
    </Panel>
  );
}

export { AboutPanel, RuntimeSettingsPanels };
