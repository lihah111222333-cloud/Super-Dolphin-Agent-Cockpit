export function attachActiveThreadRpcRuntime(runtime, deps) {
  const {
    activeThreadInterruptTarget,
    backendThreadIdForState,
    cleanObject,
  } = deps;
  const { get, requireCwd, notifyAction, addWarning } = runtime;

  const activeThreadRPC = async (action, rpc) => {
    const currentState = get();
    const interruptTarget = action === 'thread.interrupt' ? activeThreadInterruptTarget(currentState) : null;
    const threadId = interruptTarget?.threadId || backendThreadIdForState(currentState, currentState.activeThreadId);
    if (!threadId) {
      notifyAction('当前没有可操作的后端线程', 'warning');
      return false;
    }
    const actionLabels = {
      'thread.interrupt': '中断当前执行',
      'thread.force_complete': '强制完成当前执行',
      'thread.compact': '压缩上下文',
      'thread.recover': '恢复连接',
    };
    try {
      const cwd = requireCwd(action);
      let payload = { cwd, threadId };
      if (action === 'thread.interrupt') {
        const target = interruptTarget || activeThreadInterruptTarget(currentState);
        if (!target.interruptible) {
          notifyAction('当前没有可中断任务', 'warning', { threadId });
          return false;
        }
        payload = cleanObject({ cwd, threadId: target.threadId, source: 'ui_stop' });
      }
      await rpc(cleanObject(payload));
      notifyAction({
        'thread.interrupt': '已发送中断请求',
        'thread.force_complete': '已发送强制完成请求',
        'thread.compact': '已发送压缩请求',
        'thread.recover': '已发送恢复请求',
      }[action] || '线程操作已提交', 'success', { threadId });
      return true;
    }
    catch (error) {
      const message = error?.message || String(error);
      notifyAction(`${actionLabels[action] || '线程操作'}失败：${message}`, 'error', { threadId });
      addWarning('error', `${action}.failed`, { threadId, error: message });
      return false;
    }
  };

  Object.assign(runtime, { activeThreadRPC });
}
