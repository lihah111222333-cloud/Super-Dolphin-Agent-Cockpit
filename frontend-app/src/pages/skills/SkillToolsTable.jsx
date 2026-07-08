const SKILL_TOOLS_UI = Object.freeze({
  actions: '操作',
  description: '说明',
  disabled: '未启用',
  enabled: '已启用',
  methodName: '工具方法',
  status: '状态',
});

export function SkillToolsTable({ tools }) {
  return (
    <div className="skill-tools-table-wrap">
      <table className="skill-tools-table">
        <thead><tr><th>{SKILL_TOOLS_UI.methodName}</th><th>{SKILL_TOOLS_UI.description}</th><th>{SKILL_TOOLS_UI.status}</th><th>{SKILL_TOOLS_UI.actions}</th></tr></thead>
        <tbody>{tools.map((tool) => <SkillToolRow key={tool.id} tool={tool} />)}</tbody>
      </table>
    </div>
  );
}

export function SkillToolsState(props) {
  const { cwd, error, errorMessage, isError, isLoading, tools } = props;
  return (
    <>
      {isError ? <p className="skill-tools-error" role="alert">{SKILL_TOOLS_UI.errorPrefix}{errorMessage(error)}</p> : null}
      {cwd && !isLoading && !isError && tools.length === 0 ? <p className="skill-tools-empty">{SKILL_TOOLS_UI.empty}</p> : null}
      {tools.length > 0 ? <SkillToolsTable tools={tools} /> : null}
    </>
  );
}

function SkillToolRow({ tool }) {
  return <tr><td className="skill-tool-name-cell"><strong>{tool.name}</strong>{tool.methodName !== tool.name ? <span>{tool.methodName}</span> : null}</td><td>{tool.description || '-'}</td><td><SkillToolStatus tool={tool} /></td><td><code>{skillToolCommandText(tool)}</code></td></tr>;
}

function SkillToolStatus({ tool }) {
  return <span className={`skill-tool-status ${tool.enabled ? 'is-enabled' : 'is-disabled'}`}>{tool.enabled ? SKILL_TOOLS_UI.enabled : SKILL_TOOLS_UI.disabled}</span>;
}

function skillToolCommandText(tool) {
  return [tool.command, ...tool.args].filter(Boolean).join(' ') || '-';
}
