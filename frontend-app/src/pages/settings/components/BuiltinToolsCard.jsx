import React from 'react';
import { SettingsPromptNotice } from './SettingsPromptNotice.jsx';
import './BuiltinToolsCard.css';
import './SettingsPromptToggle.css';

function BuiltinToolsCard({ builtins, copy }) {
  const builtinsCopy = copy.builtins;
  return (
    <>
      <div className="section-header">{builtinsCopy.title}</div>
      <div className="data-card-vue" data-testid="settings-builtin-tools-card">
        <BuiltinToolsSummary builtins={builtins} builtinsCopy={builtinsCopy} />
        <BuiltinToolsContent builtins={builtins} builtinsCopy={builtinsCopy} />
        {builtins.notice.message ? <SettingsPromptNotice notice={builtins.notice} testId="settings-builtin-tools-notice" /> : null}
      </div>
    </>
  );
}

function BuiltinToolsSummary({ builtins, builtinsCopy }) {
  return (
    <>
      <div className="data-row-vue"><strong>{builtinsCopy.switchTitle}</strong><span data-testid="settings-builtin-tools-summary">{builtins.loading ? builtinsCopy.loading : builtinsCopy.controlled + ' ' + builtins.filteredCount + ' / ' + builtins.totalToolCount}</span></div>
      <div className="settings-prompt-desc">{builtinsCopy.description}</div>
    </>
  );
}

function BuiltinToolsContent({ builtins, builtinsCopy }) {
  if (builtins.tools.length === 0 && !builtins.loading) {
    return <div className="settings-log-empty" data-testid="settings-builtin-tools-empty">{builtinsCopy.empty}</div>;
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
