import React from 'react';
import { useVueSetup, val } from '../../utils/vue-compat.js';
import { ProviderSettings as VueComp } from './ProviderSettings.ts';

export function ProviderSettings(props) {
  const emit = (event, ...args) => {
    const propName = 'on' + event.split('-').map(s => s[0].toUpperCase() + s.slice(1)).join('');
    props[propName]?.(...args);
  };
  const vm = useVueSetup(VueComp.setup, props, emit);

  const activeProvider = val(vm.activeProvider);
  const normalizedActiveProvider = val(vm.normalizedActiveProvider);
  const PROVIDER_OPTIONS = val(vm.PROVIDER_OPTIONS) || [];
  const SANDBOX_MODES = val(vm.SANDBOX_MODES) || [];
  const SUMMARY_MODES = val(vm.SUMMARY_MODES) || [];
  const APPROVAL_MODES = val(vm.APPROVAL_MODES) || [];
  const PERSONALITY_OPTIONS = val(vm.PERSONALITY_OPTIONS) || [];
  const sandboxMode = val(vm.sandboxMode);
  const writablePaths = val(vm.writablePaths);
  const networkAccess = val(vm.networkAccess);
  const readOnlyMode = val(vm.readOnlyMode);
  const readablePaths = val(vm.readablePaths);
  const sandboxNotice = vm.sandboxNotice || {};
  const sandboxSaving = val(vm.sandboxSaving);
  const writablePathsError = val(vm.writablePathsError);
  const summaryMode = val(vm.summaryMode);
  const approvalMode = val(vm.approvalMode);
  const effortMode = val(vm.effortMode);
  const providerModel = val(vm.providerModel);
  const personality = val(vm.personality);
  const codexHome = val(vm.codexHome);
  const codexInstanceKey = val(vm.codexInstanceKey);
  const codexModelProvider = val(vm.codexModelProvider);

  const providerModelOptions = val(vm.providerModelOptions) || [];
  const providerEffortOptions = val(vm.providerEffortOptions) || [];

  return (
    <>
      <div className="section-header">PROVIDER</div>

      <div className="data-card-vue" data-testid="settings-provider-sandbox-card">
        <div className="data-row-vue">
          <strong>Active Provider</strong>
          <span>当前生效的底层模型驱动</span>
        </div>
        <div className="settings-stall-row" style={{ marginTop: '8px', marginBottom: '12px' }}>
          <select
            value={activeProvider}
            className="settings-stall-input"
            data-testid="settings-provider-active-select"
            style={{ width: '220px' }}
            onChange={(e) => {
              vm.activeProvider.value = e.target.value;
              vm.onActiveProviderChange();
            }}
          >
            {PROVIDER_OPTIONS.map((p) => (
              <option key={p.value} value={p.value}>{p.label}</option>
            ))}
          </select>
        </div>

        {normalizedActiveProvider === 'codex' && (
          <>
            <div className="data-row-vue">
              <strong>Codex Identity</strong>
              <span>用于选择/复用正确的 codex app-server，不再依赖启动脚本环境变量</span>
            </div>
            <div className="settings-stall-row" style={{ marginTop: '8px' }}>
              <label className="settings-stall-label">Codex Home</label>
              <input
                value={codexHome}
                className="settings-stall-input"
                data-testid="provider-codex-home-input"
                style={{ width: '360px' }}
                placeholder="~/.codex"
                onChange={(e) => { vm.codexHome.value = e.target.value; }}
              />
            </div>
            <div className="settings-stall-row" style={{ marginTop: '8px' }}>
              <label className="settings-stall-label">Instance Key</label>
              <input
                value={codexInstanceKey}
                className="settings-stall-input"
                data-testid="provider-codex-instance-key-input"
                style={{ width: '220px' }}
                placeholder="default"
                onChange={(e) => { vm.codexInstanceKey.value = e.target.value; }}
              />
            </div>
            <div className="settings-stall-row" style={{ marginTop: '8px', marginBottom: '12px' }}>
              <label className="settings-stall-label">Model Provider</label>
              <input
                value={codexModelProvider}
                className="settings-stall-input"
                data-testid="provider-codex-model-provider-input"
                style={{ width: '220px' }}
                placeholder="openai"
                onChange={(e) => { vm.codexModelProvider.value = e.target.value; }}
              />
            </div>
          </>
        )}

        <div className="data-row-vue">
          <strong>Sandbox Policy</strong>
          <span>新建 Thread 时生效的沙箱策略</span>
        </div>
        <div className="settings-stall-row" style={{ marginTop: '8px' }}>
          <select
            value={sandboxMode}
            className="settings-stall-input"
            data-testid="provider-sandbox-mode-select"
            style={{ width: '220px' }}
            onChange={(e) => { vm.sandboxMode.value = e.target.value; }}
          >
            {SANDBOX_MODES.map((m) => (
              <option key={m.value} value={m.value}>{m.label}</option>
            ))}
          </select>
        </div>

        {sandboxMode === 'workspaceWrite' && (
          <>
            <div className="settings-prompt-label" style={{ marginTop: '10px' }}>可写目录（每行一个绝对路径，必填）</div>
            <textarea
              className="settings-prompt-textarea"
              data-testid="provider-writable-paths-input"
              rows={3}
              value={writablePaths}
              placeholder="/abs/path/to/workspace"
              onChange={(e) => { vm.writablePaths.value = e.target.value; }}
            ></textarea>
            {writablePathsError && <div className="settings-prompt-notice is-error">{writablePathsError}</div>}
            <label className="settings-prompt-toggle" style={{ marginTop: '8px' }}>
              <div className="settings-prompt-toggle-copy">
                <span className="settings-prompt-toggle-title">允许网络访问</span>
              </div>
              <input
                type="checkbox"
                className="settings-prompt-toggle-input"
                checked={networkAccess}
                onChange={(e) => { vm.networkAccess.value = e.target.checked; }}
              />
            </label>
          </>
        )}

        {sandboxMode === 'readOnly' && (
          <>
            <div className="settings-stall-row" style={{ marginTop: '10px' }}>
              <select
                value={readOnlyMode}
                className="settings-stall-input"
                style={{ width: '160px' }}
                onChange={(e) => { vm.readOnlyMode.value = e.target.value; }}
              >
                <option value="fullAccess">fullAccess（全量只读）</option>
                <option value="restricted">restricted（限定目录）</option>
              </select>
            </div>
            {readOnlyMode === 'restricted' && (
              <>
                <div className="settings-prompt-label" style={{ marginTop: '8px' }}>可读目录（每行一个绝对路径）</div>
                <textarea
                  className="settings-prompt-textarea"
                  rows={3}
                  value={readablePaths}
                  placeholder="/abs/path/to/read"
                  onChange={(e) => { vm.readablePaths.value = e.target.value; }}
                ></textarea>
              </>
            )}
          </>
        )}

        <div className="settings-stall-row" style={{ marginTop: '12px' }}>
          <label className="settings-stall-label">模型（Model）</label>
          <select
            value={providerModel}
            className="settings-stall-input"
            data-testid="provider-model-select"
            style={{ width: '260px' }}
            onChange={(e) => { vm.providerModel.value = e.target.value; }}
          >
            {providerModelOptions.map((m) => (
              <option key={m.value} value={m.value}>{m.label}</option>
            ))}
          </select>
        </div>
        <div className="settings-stall-row" style={{ marginTop: '8px' }}>
          <label className="settings-stall-label">推理力度（Effort）</label>
          <select
            value={effortMode}
            className="settings-stall-input"
            data-testid="provider-effort-mode-select"
            style={{ width: '260px' }}
            onChange={(e) => { vm.effortMode.value = e.target.value; }}
          >
            {providerEffortOptions.map((m) => (
              <option key={m.value} value={m.value}>{m.label}</option>
            ))}
          </select>
        </div>
        <div className="settings-stall-row" style={{ marginTop: '8px' }}>
          <label className="settings-stall-label">回复风格（Personality）</label>
          <select
            value={personality}
            className="settings-stall-input"
            data-testid="provider-personality-select"
            style={{ width: '260px' }}
            onChange={(e) => { vm.personality.value = e.target.value; }}
          >
            {PERSONALITY_OPTIONS.map((m) => (
              <option key={m.value} value={m.value}>{m.label}</option>
            ))}
          </select>
        </div>
        <div className="settings-stall-row" style={{ marginTop: '8px' }}>
          <label className="settings-stall-label">推理摘要（Summary）</label>
          <select
            value={summaryMode}
            className="settings-stall-input"
            data-testid="provider-summary-mode-select"
            style={{ width: '260px' }}
            onChange={(e) => { vm.summaryMode.value = e.target.value; }}
          >
            {SUMMARY_MODES.map((m) => (
              <option key={m.value} value={m.value}>{m.label}</option>
            ))}
          </select>
        </div>
        <div className="settings-stall-row" style={{ marginTop: '8px' }}>
          <label className="settings-stall-label">审批策略（ApprovalPolicy）</label>
          <select
            value={approvalMode}
            className="settings-stall-input"
            data-testid="provider-approval-mode-select"
            style={{ width: '260px' }}
            onChange={(e) => { vm.approvalMode.value = e.target.value; }}
          >
            {APPROVAL_MODES.map((m) => (
              <option key={m.value} value={m.value}>{m.label}</option>
            ))}
          </select>
        </div>

        {sandboxNotice.message && (
          <div className={`settings-prompt-notice is-${sandboxNotice.level}`}>{sandboxNotice.message}</div>
        )}
        <div className="settings-action-row settings-action-inline" style={{ marginTop: '10px' }}>
          <button className="btn btn-secondary btn-toolbar-sm" onClick={vm.loadProviderSettings} disabled={sandboxSaving}>刷新</button>
          <button className="btn btn-primary btn-toolbar-sm" data-testid="provider-sandbox-save-button" onClick={vm.saveProviderSettings} disabled={sandboxSaving}>
            {sandboxSaving ? '保存中...' : '保存'}
          </button>
        </div>
      </div>
    </>
  );
}
