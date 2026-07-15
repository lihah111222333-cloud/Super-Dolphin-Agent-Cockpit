import { Power, PowerOff } from 'lucide-react';

function textFromValue(value) {
  if (value === null || value === undefined) return '';
  return value.toString();
}

export function MCPToolCard({ errorMessage, mcpServerStatus, state, tool }) {
  const model = mcpToolCardModel({ errorMessage, mcpServerStatus, state, tool });
  const Icon = tool.Icon;
  return (
    <section className="mcp-tool-card fusion-surface" aria-label={`${tool.title} 控制`}>
      <div className={`mcp-tool-icon fusion-surface-glass ${tool.id}`} aria-hidden="true"><Icon size={20} /></div>
      <MCPToolMain model={model} tool={tool} />
      <span className={`mcp-tool-status is-${model.status.tone}`} data-testid={tool.testId}>{model.status.label}</span>
      <div className="mcp-tool-actions">
        <button
          type="button"
          aria-label={`${model.actionLabel} ${tool.title}`}
          className={`suiyuan-switch-btn mcp-tool-toggle ${model.nextAction === 'stop' ? 'active is-stop' : 'is-start'}`}
          onClick={() => { void state.runMCPAction(tool, model.nextAction); }}
          disabled={!state.projectReady || Boolean(model.action)}
        >
          <div className="suiyuan-switch-track">
            <div className="suiyuan-switch-thumb" />
          </div>
        </button>
      </div>
    </section>
  );
}

function mcpToolCardModel({ errorMessage, mcpServerStatus, state, tool }) {
  const status = mcpServerStatus(state.projectReady, state.mcpStatusQuery, tool.id);
  const action = textFromValue(state.mcpActions[tool.id]);
  const notice = textFromValue(state.mcpNotices[tool.id]);
  const error = textFromValue(state.mcpErrors[tool.id]);
  const nextAction = status.tone === 'enabled' ? 'stop' : 'start';
  const isError = state.mcpServersIsError || Boolean(error);
  return {
    action,
    actionLabel: nextAction === 'start' ? '开启' : '关闭',
    ActionIcon: nextAction === 'start' ? Power : PowerOff,
    busyLabel: nextAction === 'start' ? '开启中...' : '关闭中...',
    feedback: mcpToolFeedback({ error, errorMessage, notice, state, tool }),
    feedbackRole: isError ? 'alert' : notice ? 'status' : undefined,
    isError,
    nextAction,
    status,
  };
}

function MCPToolMain({ model, tool }) {
  return (
    <div className="mcp-tool-main">
      <div className="mcp-tool-title-line"><h2 title={tool.description}>{tool.title}</h2></div>
      {model.feedback ? <p className={`mcp-tool-notice${model.isError ? ' is-error' : ''}`} role={model.feedbackRole}>{model.feedback}</p> : null}
    </div>
  );
}

function mcpToolFeedback({ error, errorMessage, notice, state, tool }) {
  if (!state.projectReady) return '请选择项目后再管理 MCP 工具。';
  if (state.mcpServersIsError) return `读取 ${tool.title} 状态失败：${errorMessage(state.mcpServersError)}`;
  return error || notice;
}
