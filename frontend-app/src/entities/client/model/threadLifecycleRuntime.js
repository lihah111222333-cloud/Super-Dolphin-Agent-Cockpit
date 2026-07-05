export function attachActiveThreadRpcRuntime(runtime, deps) {
  const {
    activeThreadInterruptTarget,
    backendThreadIdForState,
    cleanObject,
  } = deps;
  const { get, requireCwd, notifyAction, addWarning } = runtime;

  // 中断接口明确返回 ok:false 时必须带诊断，否则按桥接异常处理。
  const interruptFailureMessage = (result) => {
    for (const value of [result?.error, result?.message, result?.reason, result?.status, result?.mode]) {
      const message = (value || '').toString().trim();
      if (message) return message;
    }
    throw new Error('thread.interrupt ok:false response message is required');
  };

  const activeThreadRPC = async (action, rpc) => {
    const currentState = get();
    const requiresActiveTurn = action === 'thread.interrupt' || action === 'thread.force_complete';
    const activeTurnTarget = requiresActiveTurn ? activeThreadInterruptTarget(currentState) : null;
    const threadId = activeTurnTarget?.threadId || backendThreadIdForState(currentState, currentState.activeThreadId);
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
      if (requiresActiveTurn) {
        const target = activeTurnTarget || activeThreadInterruptTarget(currentState);
        if (!target.interruptible) {
          notifyAction(action === 'thread.interrupt' ? '当前没有可中断任务' : '当前没有可强制完成任务', 'warning', { threadId });
          return false;
        }
        payload = action === 'thread.interrupt'
          ? cleanObject({ cwd, threadId: target.threadId, source: 'ui_stop' })
          : cleanObject({ cwd, threadId: target.threadId });
      }
      const result = await rpc(cleanObject(payload));
      if (action === 'thread.interrupt' && result?.ok === false) {
        const message = interruptFailureMessage(result);
        notifyAction(`${actionLabels[action]}失败：${message}`, 'warning', { threadId });
        addWarning('warn', `${action}.failed`, { threadId, error: message });
        return false;
      }
      if (action === 'thread.force_complete' && (result?.ok === false || result?.forceCompleted === false)) {
        const message = (result?.error || result?.message || result?.errorCode || 'force complete target not found').toString().trim();
        if (!message) throw new Error('thread.force_complete ok:false response message is required');
        notifyAction(`${actionLabels[action]}失败：${message}`, 'warning', { threadId, error: message });
        addWarning('warn', `${action}.failed`, { threadId, error: message });
        return false;
      }
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
