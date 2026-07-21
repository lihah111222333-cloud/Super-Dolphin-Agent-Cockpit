import { useCallback } from 'react';
import { textValue } from '../../shared/pageShared.js';
import { applyDagOps, dispatchDagNode } from '../services/workflowPageService.js';
import { withWorkflowActionTimeout } from '../services/workflowDagModel.js';
import { dagNodePatchFromForm } from '../services/workflowNodeModel.js';
import {
  refreshWorkflowAfterAction,
  refreshWorkflowDetailResult,
  workflowRefreshNotice,
} from './workflowActionRefresh.js';

const workflowNodeFacade = Object.freeze({ dispatchDagNode });

function useSaveAgentNodeAction({ actionState, derived, notices, refresh }) {
  return useCallback(async (form, node) => {
    if (!derived.dagKey || !node?.nodeKey) return;
    if (derived.baseVersion === null) {
      actionState.setError('自动化详情不完整，无法保存步骤');
      return;
    }
    const targetDagKey = derived.dagKey;
    actionState.setSavingNodeKey(node.nodeKey);
    actionState.setError('');
    notices.clearNotice();
    try {
      const patch = dagNodePatchFromForm(form, node);
      const ops = [{ node_key: node.nodeKey, op: 'update_node', patch }];
      await withWorkflowActionTimeout(applyDagOps({
        baseVersion: derived.baseVersion,
        dagKey: targetDagKey,
        ops,
      }));
      const refreshError = await refreshWorkflowDetailResult(refresh, targetDagKey);
      notices.showTaskNotice(
        workflowRefreshNotice('已保存步骤 ' + node.title, refreshError),
        targetDagKey,
      );
    } catch (err) {
      actionState.setError('保存步骤失败，请重试。');
      throw err;
    } finally {
      actionState.setSavingNodeKey('');
    }
  }, [actionState, derived, notices, refresh]);
}

function useDispatchDagNodeAction({ actionState, derived, list, notices, refresh }) {
  return useCallback(
    (node, assignedTo) => dispatchDagNodeAction(
      {
        actionState,
        derived,
        facade: workflowNodeFacade,
        notices,
        refreshContext: { list, refresh },
      },
      node,
      assignedTo,
    ),
    [actionState, derived, list, notices, refresh],
  );
}

async function dispatchDagNodeAction(
  { actionState, derived, facade, notices, refreshContext },
  node,
  assignedTo,
) {
  const assignee = textValue(assignedTo);
  if (!derived.dagKey || !node?.nodeKey) return;
  if (!derived.runId) {
    actionState.setError('派发节点失败：当前运行缺少 runId，无法定位 runtime node');
    return;
  }
  if (!assignee) {
    actionState.setError('派发节点失败：请填写执行者 assigned_to');
    return;
  }
  const targetDagKey = derived.dagKey;
  actionState.setDispatchingNodeKey(node.nodeKey);
  actionState.setError('');
  notices.clearNotice();
  try {
    await withWorkflowActionTimeout(facade.dispatchDagNode({
      dagKey: targetDagKey,
      runId: derived.runId,
      nodeKey: node.nodeKey,
      assignedTo: assignee,
    }));
    const refreshResult = await refreshWorkflowAfterAction({ ...refreshContext, targetDagKey });
    notices.showTaskNotice(
      workflowRefreshNotice(`已派发步骤 ${node.title || node.nodeKey}`, refreshResult.error),
      targetDagKey,
    );
  } catch (err) {
    actionState.setError('派发节点失败，请重试。');
    throw err;
  } finally {
    actionState.setDispatchingNodeKey('');
  }
}

export { dispatchDagNodeAction, useDispatchDagNodeAction, useSaveAgentNodeAction };
