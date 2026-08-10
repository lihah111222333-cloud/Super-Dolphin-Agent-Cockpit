import { useEffect, useMemo, useState } from 'react';
import { selectAgentBoardViewModel } from '../../../entities/client/model/helpers/agentBoard/selector.js';
import { resolveSelectedAgentId } from './agentBoardModel.js';

const AGENT_BOARD_COMPACT_VIEWPORT_WIDTH = 860;

/*
 * Agent 看板 UI 控制器。
 * UI 层只维护 rightPanelView（runtime/agents）、selectedAgentId 与展开/收起状态；
 * 业务数据一律通过 selectAgentBoardViewModel 从 client store 获取。
 */
function useAgentBoardController({ store, rightPanelOpen, setRightPanelOpen }) {
  const [rightPanelView, setRightPanelView] = useState('runtime');
  const [selectedAgentId, setSelectedAgentId] = useState('');
  const docked = rightPanelOpen && rightPanelView === 'agents';
  const agents = store.agents;
  const mainAgentId = store.mainAgentId;
  const activeThreadId = store.activeThreadId;
  const threads = store.threads;
  const loading = Boolean(store.activeThreadId && store.threadStateLoadingByThread?.[store.activeThreadId]);
  const error = store.error ? String(store.error) : null;
  const viewModel = useMemo(
    () => selectAgentBoardViewModel({ agents, mainAgentId }, {
      mode: docked ? 'docked' : 'floating',
      selectedAgentId,
      loading,
      error,
      activeThreadId,
      threads,
    }),
    [agents, mainAgentId, docked, selectedAgentId, loading, error, activeThreadId, threads],
  );
  const conversationActive = viewModel.counts.running > 0;
  const [floatingCollapsed, setFloatingCollapsed] = useState(!conversationActive);
  useEffect(() => {
    setFloatingCollapsed(!conversationActive);
  }, [activeThreadId, conversationActive]);
  const resolvedSelectedId = resolveSelectedAgentId(viewModel);
  useEffect(() => {
    if (resolvedSelectedId !== selectedAgentId) setSelectedAgentId(resolvedSelectedId);
  }, [resolvedSelectedId, selectedAgentId]);
  const expand = (agentId) => {
    if (typeof agentId === 'string' && agentId) setSelectedAgentId(agentId);
    setRightPanelView('agents');
    setRightPanelOpen(true);
  };
  const collapse = () => setRightPanelOpen(false);
  const showRuntime = () => setRightPanelView('runtime');
  const showAgents = () => setRightPanelView('agents');
  return {
    docked,
    rightPanelView,
    floatingCollapsed,
    viewModel: { ...viewModel, selectedAgentId: resolvedSelectedId },
    setFloatingCollapsed,
    selectAgent: setSelectedAgentId,
    expand,
    collapse,
    showRuntime,
    showAgents,
  };
}

export { AGENT_BOARD_COMPACT_VIEWPORT_WIDTH, useAgentBoardController };
