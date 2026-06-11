import React from 'react';
import { SettingsPromptNotice } from './SettingsPromptNotice.jsx';

function BuiltinToolsCard({ builtins }) {
  return (
    <>
      <div className="section-header">模型内置能力</div>
      <div className="data-card-vue" data-testid="settings-builtin-tools-card">
        <BuiltinToolsSummary builtins={builtins} />
        <BuiltinToolsContent builtins={builtins} />
        {builtins.notice.message ? <SettingsPromptNotice notice={builtins.notice} testId="settings-builtin-tools-notice" /> : null}
      </div>
    </>
  );
}

function BuiltinToolsSummary({ builtins }) {
  return (
    <>
      <div className="data-row-vue"><strong>内置能力开关</strong><span data-testid="settings-builtin-tools-summary">{builtins.loading ? '加载中...' : '已管控 ' + builtins.filteredCount + ' / ' + builtins.totalToolCount}</span></div>
      <div className="settings-prompt-desc">默认管控与本项目文件、命令、编排、计划、权限、插件管理重复，或会绕过项目治理的能力。</div>
    </>
  );
}

function BuiltinToolsContent({ builtins }) {
  if (builtins.tools.length === 0 && !builtins.loading) {
    return <div className="settings-log-empty" data-testid="settings-builtin-tools-empty">暂无可配置的内置工具</div>;
  }
  return (
    <div className="settings-builtin-tool-groups" data-testid="settings-builtin-tools-groups">
      {builtins.groups.map((group) => <BuiltinToolGroup builtins={builtins} group={group} key={group.key} />)}
    </div>
  );
}

function BuiltinToolGroup({ builtins, group }) {
  const isOpen = builtins.isOpen(group.key);
  return (
    <section className="settings-builtin-tool-group" data-testid={'settings-builtin-tool-group-' + group.key}>
      <button type="button" className="settings-builtin-tool-group-head" data-testid={'settings-builtin-tool-group-head-' + group.key} aria-expanded={isOpen ? 'true' : 'false'} onClick={() => builtins.toggleGroup(group.key)}>
        <span className={'settings-builtin-tool-group-chevron ' + (isOpen ? 'is-open' : '')}>▸</span><span className="settings-builtin-tool-group-name">{group.label}</span><span className="settings-builtin-tool-group-summary">{builtins.groupSummary(group)}</span>
      </button>
      {isOpen ? <BuiltinToolGroupBody builtins={builtins} group={group} /> : null}
    </section>
  );
}

function BuiltinToolGroupBody({ builtins, group }) {
  return (
    <div className="settings-builtin-tool-group-body">
      {group.note ? <p className="settings-builtin-tool-group-note" data-testid={'settings-builtin-tool-group-note-' + group.key}>{group.note}</p> : null}
      {group.tools.map((tool) => <BuiltinToolRow builtins={builtins} key={tool.id} tool={tool} />)}
    </div>
  );
}

function BuiltinToolRow({ builtins, tool }) {
  return (
    <label className={'settings-prompt-toggle ' + ((!tool.enabled || tool.replacedBy) ? 'is-disabled-tool' : '')} data-testid={'settings-builtin-tool-' + tool.id}>
      <div className="settings-prompt-toggle-copy"><span className="settings-prompt-toggle-title">{tool.label}</span><span className="settings-prompt-toggle-desc">{builtins.toolMetaText(tool)}</span></div>
      <input type="checkbox" className="settings-prompt-toggle-input" data-testid={'settings-builtin-tool-input-' + tool.id} checked={!tool.enabled || Boolean(tool.replacedBy)} disabled={Boolean(tool.replacedBy) || Boolean(builtins.savingIds[tool.id])} onChange={() => builtins.toggleTool(tool)} />
    </label>
  );
}

export { BuiltinToolsCard };
