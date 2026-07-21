import { runUIAction } from '../../../shared/ui/runUIAction.js';
import {
  useDeleteDagAction,
  useRunSelectedDagAction,
  useStopSelectedDagAction,
} from './useWorkflowLifecycleActions.js';
import {
  useDispatchDagNodeAction,
  useSaveAgentNodeAction,
} from './useWorkflowNodeActions.js';
import {
  useSaveScheduleAction,
  useToggleScheduleAction,
} from './useWorkflowScheduleActions.js';
import {
  useCreateAndStartTemplateAction,
  useStartDesignFlowAction,
} from './useWorkflowTemplateActions.js';

function useWorkflowActions(options) {
  const {
    actionState,
    derived,
    list,
    notices,
    refresh,
    selection,
    setDesignSession,
    store,
    workflowCwd,
  } = options;
  /*
   * workflow actions 只提交操作并刷新数据。
   * DAG 的真实状态以后端刷新结果为准，本地只放按钮和提示状态。
   */
  const runSelectedDag = useRunSelectedDagAction({ actionState, derived, list, notices, refresh });
  const stopSelectedDag = useStopSelectedDagAction({ actionState, derived, list, notices, refresh });
  const confirmDeleteDAG = useDeleteDagAction({ actionState, derived, list, notices, selection });
  const saveSchedule = useSaveScheduleAction({ actionState, derived, list, notices, refresh });
  const toggleScheduleEnabled = useToggleScheduleAction({ actionState, derived, list, notices, refresh });
  const saveAgentNode = useSaveAgentNodeAction({ actionState, derived, notices, refresh });
  const dispatchNode = useDispatchDagNodeAction({ actionState, derived, list, notices, refresh });
  const createAndStartTemplate = useCreateAndStartTemplateAction({
    actionState,
    list,
    notices,
    refresh,
    workflowCwd,
  });
  const startDesignFlow = useStartDesignFlowAction({
    actionState,
    notices,
    setDesignSession,
    store,
    workflowCwd,
  });
  return {
    confirmDeleteDAG: (...args) => runUIAction('workflow.delete', () => confirmDeleteDAG(...args)),
    createAndStartTemplate: (...args) => runUIAction(
      'workflow.template.create-start',
      () => createAndStartTemplate(...args),
    ),
    dispatchNode: (...args) => runUIAction('workflow.node.dispatch', () => dispatchNode(...args)),
    runSelectedDag: (...args) => runUIAction('workflow.run', () => runSelectedDag(...args)),
    saveAgentNode: (...args) => runUIAction('workflow.node.save', () => saveAgentNode(...args)),
    saveSchedule: (...args) => runUIAction('workflow.schedule.save', () => saveSchedule(...args)),
    startDesignFlow: (...args) => runUIAction('workflow.design.start', () => startDesignFlow(...args)),
    stopSelectedDag: (...args) => runUIAction('workflow.stop', () => stopSelectedDag(...args)),
    toggleScheduleEnabled: (...args) => runUIAction('workflow.schedule.toggle', () => toggleScheduleEnabled(...args)),
  };
}

export { useWorkflowActions };
