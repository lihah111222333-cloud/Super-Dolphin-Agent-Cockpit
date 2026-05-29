import React from 'react';
import { useVueSetup, val } from '../utils/vue-compat.js';
import { SettingsPage as VueComp } from './SettingsPage.ts';
import { ProviderSettings } from './settings/ProviderSettings.jsx';
import { LspPromptSettings } from './settings/LspPromptSettings.jsx';
import { BuiltinToolsSettings } from './settings/BuiltinToolsSettings.jsx';

export function SettingsPage(props) {
  const emit = (event, ...args) => {
    const propName = 'on' + event.split('-').map(s => s[0].toUpperCase() + s.slice(1)).join('');
    props[propName]?.(...args);
  };
  const vm = useVueSetup(VueComp.setup, props, emit);

  const versionText = val(vm.versionText);
  const runtimeText = val(vm.runtimeText);
  const buildTimeText = val(vm.buildTimeText);
  const commitText = val(vm.commitText);
  const logLevel = val(vm.logLevel);
  const logEntries = val(vm.logEntries) || [];
  const LOG_LEVEL_OPTIONS = vm.LOG_LEVEL_OPTIONS || [];

  const stallThreshold = val(vm.stallThreshold);
  const stallLoading = val(vm.stallLoading);
  const stallNotice = vm.stallNotice || {};

  const ctxWarn = val(vm.ctxWarn);
  const ctxDanger = val(vm.ctxDanger);
  const ctxCritical = val(vm.ctxCritical);
  const ctxLoading = val(vm.ctxLoading);
  const ctxNotice = vm.ctxNotice || {};

  return (
    <section id="page-settings" className="page active" data-testid="settings-page">
      <div className="panel-header">
        <div className="ph-bar"></div>
        <div className="ph-text"><h2>设置</h2></div>
      </div>

      <div className="panel-body" data-testid="settings-panel-body">
        <div className="section-header">ABOUT</div>
        <div className="data-card-vue" data-testid="settings-about-card">
          <div className="data-row-vue"><strong>版本</strong><span>{versionText}</span></div>
          <div className="data-row-vue"><strong>运行时</strong><span>{runtimeText}</span></div>
          <div className="data-row-vue"><strong>构建时间</strong><span>{buildTimeText}</span></div>
          <div className="data-row-vue"><strong>Commit</strong><span>{commitText}</span></div>
        </div>
        <div className="settings-action-row">
          <button className="btn btn-secondary" data-testid="settings-refresh-build-button" onClick={vm.refresh}>刷新构建信息</button>
        </div>

        <div className="section-header">TURN TRACKER</div>
        <div className="data-card-vue settings-stall-card" data-testid="settings-stall-card">
          <div className="data-row-vue">
            <strong>统一超时超时阈值</strong>
            <span>统一控制 Stall 检测、Watchdog 与流读取超时</span>
          </div>
          <div className="settings-stall-row">
            <input
              type="number"
              className="settings-stall-input"
              data-testid="settings-stall-threshold-input"
              value={stallThreshold}
              min="30"
              step="30"
              disabled={stallLoading}
              onChange={(e) => { vm.stallThreshold.value = Number(e.target.value); }}
            />
            <span className="settings-stall-unit">秒 ({Math.round(stallThreshold / 60)} 分钟)</span>
            <button className="btn btn-primary btn-toolbar-sm" data-testid="settings-stall-threshold-save-button" onClick={vm.saveStallThreshold} disabled={stallLoading}>保存</button>
          </div>
          {stallNotice.message && (
            <div className={`settings-prompt-notice is-${stallNotice.level}`} data-testid="settings-stall-notice">
              {stallNotice.message}
            </div>
          )}
        </div>

        <div className="section-header">CONTEXT USAGE ALERT</div>
        <div className="data-card-vue" data-testid="settings-ctx-thresholds-card">
          <div className="data-row-vue">
            <strong>上下文使用率警报阈值</strong>
            <span>分别对应 warn / danger / critical 三档颜色与顶部横幅</span>
          </div>
          <div className="settings-stall-row">
            <input type="number" className="settings-stall-input" data-testid="settings-ctx-warn-input" value={ctxWarn} min={1} max={99} disabled={ctxLoading} onChange={(e) => { vm.ctxWarn.value = Number(e.target.value); }} />
            <span className="settings-stall-unit">% warn</span>
            <input type="number" className="settings-stall-input" data-testid="settings-ctx-danger-input" value={ctxDanger} min={1} max={99} disabled={ctxLoading} onChange={(e) => { vm.ctxDanger.value = Number(e.target.value); }} />
            <span className="settings-stall-unit">% danger</span>
            <input type="number" className="settings-stall-input" data-testid="settings-ctx-critical-input" value={ctxCritical} min={1} max={99} disabled={ctxLoading} onChange={(e) => { vm.ctxCritical.value = Number(e.target.value); }} />
            <span className="settings-stall-unit">% critical</span>
            <button className="btn btn-primary btn-toolbar-sm" data-testid="settings-ctx-thresholds-save-button" onClick={vm.saveContextThresholds} disabled={ctxLoading}>保存</button>
          </div>
          {ctxNotice.message && (
            <div className={`settings-prompt-notice is-${ctxNotice.level}`} data-testid="settings-ctx-thresholds-notice">
              {ctxNotice.message}
            </div>
          )}
        </div>

        <ProviderSettings projectStore={props.projectStore} />
        <LspPromptSettings projectStore={props.projectStore} />
        <BuiltinToolsSettings projectStore={props.projectStore} />

        <div className="section-header">UI LOG</div>
        <div className="data-card-vue settings-log-card" data-testid="settings-log-card">
          <div className="data-row-vue">
            <strong>日志级别</strong>
            <span>{logLevel}</span>
          </div>
          <div className="settings-stall-row" style={{ marginTop: '8px', marginBottom: '12px' }}>
            <select
              className="settings-stall-input"
              data-testid="settings-log-level-select"
              style={{ width: '220px' }}
              value={logLevel}
              onChange={(e) => vm.applyLogLevelChange(e.target.value)}
            >
              {LOG_LEVEL_OPTIONS.map((opt) => (
                <option key={opt.value} value={opt.value}>{opt.label}</option>
              ))}
            </select>
            <span className="settings-stall-unit">立即生效（跨 tab 同步）</span>
          </div>
          <div className="settings-action-row">
            <button className="btn btn-secondary btn-toolbar-sm" data-testid="settings-log-refresh-button" onClick={vm.refreshLogPanel}>刷新日志</button>
          </div>
          {logEntries.length === 0 ? (
            <div className="settings-log-empty" data-testid="settings-log-empty">暂无日志</div>
          ) : (
            <div className="settings-log-list" data-testid="settings-log-list">
              {logEntries.map((entry) => (
                <div
                  key={entry.seq}
                  className="settings-log-item"
                >
                  <span className="settings-log-time">{vm.formatLogTime(entry.ts)}</span>
                  <span className={`settings-log-level is-${entry.level}`}>{entry.level}</span>
                  <span className="settings-log-event">{entry.scope}.{entry.event}</span>
                </div>
              ))}
            </div>
          )}
        </div>
      </div>
    </section>
  );
}
