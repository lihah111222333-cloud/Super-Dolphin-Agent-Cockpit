import { useCallback, useRef } from 'react';
import { firstText, textValue } from '../../shared/pageShared.js';
import { deleteDag, startDag, terminateDagRun } from '../services/workflowPageService.js';
import { dagCategoryOf, runKeyOf, withWorkflowActionTimeout } from '../services/workflowDagModel.js';
import { uniqueWorkflowActionKey } from '../services/workflowEnterpriseTemplateModel.js';
import {
  isIdempotencyKeyExhaustedError,
  refreshWorkflowAfterAction,
  workflowRefreshNotice,
} from './workflowActionRefresh.js';

const workflowLifecycleFacade = Object.freeze({ terminateDagRun });

function useRunSelectedDagAction({ actionState, derived, list, notices, refresh }) {
  const runIntentsRef = useRef(new Map());
  return useCallback(async () => {
    if (derived.startDisabledReason) return;
    const targetDagKey = derived.dagKey;
    let intent = runIntentsRef.current.get(targetDagKey);
    if (!intent) {
      intent = { idempotencyKey: uniqueWorkflowActionKey('ui'), pending: false };
      runIntentsRef.current.set(targetDagKey, intent);
    }
    if (intent.pending) return;
    intent.pending = true;
    actionState.setActioning('start');
    actionState.setError('');
    notices.clearNotice();
    try {
      const result = await startDag({
        dagKey: targetDagKey,
        triggerSource: 'manual',
        idempotencyKey: intent.idempotencyKey,
      });
      runIntentsRef.current.delete(targetDagKey);
      const runKey = runKeyOf(result);
      const refreshResult = await refreshWorkflowAfterAction({ list, refresh, runKey, targetDagKey });
      const warning = textValue(result?.warning);
      const message = warning ? '已启动，后端提示：' + warning : '已启动自动化';
      notices.showTaskNotice(workflowRefreshNotice(message, refreshResult.error), targetDagKey);
    } catch (err) {
      if (isIdempotencyKeyExhaustedError(err)) {
        runIntentsRef.current.delete(targetDagKey);
      } else {
        intent.pending = false;
      }
      actionState.setError('启动自动化失败，请重试。');
      throw err;
    } finally {
      actionState.setActioning('');
    }
  }, [actionState, derived, list, notices, refresh]);
}

function useStopSelectedDagAction({ actionState, derived, list, notices, refresh }) {
  return useCallback(
    () => stopSelectedDagAction({
      actionState,
      derived,
      facade: workflowLifecycleFacade,
      notices,
      refreshContext: { list, refresh },
    }),
    [actionState, derived, list, notices, refresh],
  );
}

async function stopSelectedDagAction({ actionState, derived, facade, notices, refreshContext }) {
  if (!derived.dagKey || !derived.activeRunKey) return;
  const targetDagKey = derived.dagKey;
  actionState.setActioning('stop');
  actionState.setError('');
  notices.clearNotice();
  try {
    await withWorkflowActionTimeout(facade.terminateDagRun({
      dagKey: targetDagKey,
      runKey: derived.activeRunKey,
      reason: 'user_requested',
    }));
    const refreshResult = await refreshWorkflowAfterAction({ ...refreshContext, targetDagKey });
    notices.showTaskNotice(workflowRefreshNotice('已停止运行', refreshResult.error), targetDagKey);
  } catch (err) {
    actionState.setError('停止运行失败，请重试。');
    throw err;
  } finally {
    actionState.setActioning('');
  }
}

function useDeleteDagAction({ actionState, derived, list, notices, selection }) {
  return useCallback(async () => {
    const target = actionState.deleteTarget;
    const targetKey = target?.dagKey || derived.dagKey;
    if (!targetKey) return;
    if (derived.activeRunKey) {
      actionState.setDeleteTarget(null);
      actionState.setError('删除自动化失败：已有运行正在进行，请先停止运行后再删除。');
      return;
    }
    actionState.setActioning('delete');
    actionState.setError('');
    notices.clearNotice();
    try {
      await withWorkflowActionTimeout(deleteDag({ dagKey: targetKey }));
      actionState.setDeleteTarget(null);
      const fallback = list.items.filter((item) => item.dagKey !== targetKey);
      const refreshResult = await refreshWorkflowAfterAction({
        fallbackItems: fallback,
        list,
        targetDagKey: targetKey,
      });
      const nextItems = refreshResult.items || fallback;
      selection.setSelectedDagKey(nextWorkflowSelectionKey(nextItems, selection.activeCategory));
      notices.showTaskNotice(
        workflowRefreshNotice('已删除 ' + (target?.title || targetKey), refreshResult.error),
        targetKey,
      );
    } catch (err) {
      actionState.setError('删除自动化失败，请重试。');
      throw err;
    } finally {
      actionState.setActioning('');
    }
  }, [actionState, derived, list, notices, selection]);
}

function nextWorkflowSelectionKey(items, activeCategory) {
  return firstText(
    items.find((item) => dagCategoryOf(item) === activeCategory)?.dagKey,
    items[0]?.dagKey,
  );
}

export {
  stopSelectedDagAction,
  useDeleteDagAction,
  useRunSelectedDagAction,
  useStopSelectedDagAction,
};
