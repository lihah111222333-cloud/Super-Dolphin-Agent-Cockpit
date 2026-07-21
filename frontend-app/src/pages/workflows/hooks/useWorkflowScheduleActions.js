import { useCallback } from 'react';
import { textValue } from '../../shared/pageShared.js';
import { applyDagOps } from '../services/workflowPageService.js';
import { isScheduledTrigger, withWorkflowActionTimeout } from '../services/workflowDagModel.js';
import {
  refreshWorkflowAfterAction,
  workflowRefreshNotice,
} from './workflowActionRefresh.js';

const workflowScheduleFacade = Object.freeze({ applyDagOps });

function useSaveScheduleAction({ actionState, derived, list, notices, refresh }) {
  return useCallback(
    (nextCronExpr = '') => saveScheduleAction(
      {
        actionState,
        derived,
        facade: workflowScheduleFacade,
        notices,
        refreshContext: { list, refresh },
      },
      nextCronExpr,
    ),
    [actionState, derived, list, notices, refresh],
  );
}

async function saveScheduleAction(
  { actionState, derived, facade, notices, refreshContext },
  nextCronExpr = '',
) {
  const cronExpr = textValue(nextCronExpr) || textValue(actionState.scheduleCron);
  if (!derived.dagKey || !cronExpr) return;
  if (derived.baseVersion === null) {
    actionState.setError('自动化详情不完整，无法保存定时任务');
    return;
  }
  if (derived.missingRootAssigneeWarning) {
    actionState.setError('保存定时任务失败：' + derived.missingRootAssigneeWarning);
    return;
  }
  const targetDagKey = derived.dagKey;
  actionState.setActioning('schedule');
  actionState.setError('');
  notices.clearNotice();
  try {
    const activeDetailDag = derived.activeDetailDag;
    const schedulePatch = { trigger: 'scheduled', cron_expr: cronExpr };
    const preservingExistingSchedule = isScheduledTrigger(activeDetailDag?.trigger)
      || Boolean(activeDetailDag?.cronExpr);
    if (preservingExistingSchedule && activeDetailDag?.scheduleEnabled === false) {
      schedulePatch.schedule_enabled = false;
    }
    const ops = [{ op: 'update_dag', patch: schedulePatch }];
    await withWorkflowActionTimeout(facade.applyDagOps({
      baseVersion: derived.baseVersion,
      dagKey: targetDagKey,
      ops,
    }));
    actionState.setScheduleOpen(false);
    const refreshResult = await refreshWorkflowAfterAction({ ...refreshContext, targetDagKey });
    notices.showTaskNotice(workflowRefreshNotice('已保存定时任务', refreshResult.error), targetDagKey);
  } catch (err) {
    actionState.setError('保存定时任务失败，请重试。');
    throw err;
  } finally {
    actionState.setActioning('');
  }
}

function useToggleScheduleAction({ actionState, derived, list, notices, refresh }) {
  return useCallback(async () => {
    if (!derived.dagKey) return;
    if (derived.baseVersion === null) {
      actionState.setError('自动化详情不完整，无法切换自动运行');
      return;
    }
    const targetDagKey = derived.dagKey;
    const enabled = !derived.activeDetailDag?.scheduleEnabled;
    actionState.setActioning('schedule-toggle');
    actionState.setError('');
    notices.clearNotice();
    try {
      const patch = { schedule_enabled: enabled };
      const ops = [{ op: 'update_dag', patch }];
      await withWorkflowActionTimeout(applyDagOps({
        baseVersion: derived.baseVersion,
        dagKey: targetDagKey,
        ops,
      }));
      const refreshResult = await refreshWorkflowAfterAction({ list, refresh, targetDagKey });
      notices.showTaskNotice(
        workflowRefreshNotice(enabled ? '已启用自动运行' : '已暂停自动运行', refreshResult.error),
        targetDagKey,
      );
    } catch (err) {
      actionState.setError('切换自动运行失败，请重试。');
      throw err;
    } finally {
      actionState.setActioning('');
    }
  }, [actionState, derived, list, notices, refresh]);
}

export { saveScheduleAction, useSaveScheduleAction, useToggleScheduleAction };
