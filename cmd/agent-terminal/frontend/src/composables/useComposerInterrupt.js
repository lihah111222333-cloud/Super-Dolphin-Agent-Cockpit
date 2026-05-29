import { ref } from '../../lib/vue.esm-browser.prod.js';
import { logDebug, logInfo } from '../services/log.js';

/**
 * 管理 ComposerBar 的中断/暂停状态与行为。
 *
 * @param {object} props - ComposerBar props
 * @param {(event: string, ...args: any[]) => void} emit
 * @param {{ hasReadyInput: () => boolean, onSend: (event?: any) => void }} deps
 */
export function useComposerInterrupt(props, emit, deps) {
  const pauseAcknowledged = ref(false);
  const interruptPending = ref(false);
  const interruptRequestThreadId = ref('');
  const interruptTimeoutId = ref(0);

  function clearInterruptTimeout() {
    if (!interruptTimeoutId.value) return;
    window.clearTimeout(interruptTimeoutId.value);
    interruptTimeoutId.value = 0;
  }

  function resetInterruptState() {
    clearInterruptTimeout();
    pauseAcknowledged.value = false;
    interruptPending.value = false;
    interruptRequestThreadId.value = '';
  }

  function onInterruptConfirmed(meta = {}) {
    const payload = meta || {};
    const currentThreadID = (props.threadId || '').toString();
    const requestThreadID = (payload.threadId || interruptRequestThreadId.value || '').toString();
    if (requestThreadID && currentThreadID && requestThreadID !== currentThreadID) {
      logDebug('ui', 'composerBar.interrupt.confirmed.ignored', {
        request_thread_id: requestThreadID,
        current_thread_id: currentThreadID,
      });
      return;
    }
    clearInterruptTimeout();
    interruptPending.value = false;
    interruptRequestThreadId.value = '';
    pauseAcknowledged.value = true;
    logInfo('ui', 'composerBar.interrupt.confirmed', {
      mode: (payload.mode || '').toString(),
    });
  }

  function onInterruptRejected(meta = {}) {
    const payload = meta || {};
    const currentThreadID = (props.threadId || '').toString();
    const requestThreadID = (payload.threadId || interruptRequestThreadId.value || '').toString();
    if (requestThreadID && currentThreadID && requestThreadID !== currentThreadID) {
      logDebug('ui', 'composerBar.interrupt.rejected.ignored', {
        request_thread_id: requestThreadID,
        current_thread_id: currentThreadID,
      });
      return;
    }
    clearInterruptTimeout();
    interruptPending.value = false;
    interruptRequestThreadId.value = '';
    logInfo('ui', 'composerBar.interrupt.rejected', {
      reason: (payload.reason || '').toString(),
      mode: (payload.mode || '').toString(),
    });
  }

  function armInterruptTimeout(requestThreadID) {
    clearInterruptTimeout();
    interruptTimeoutId.value = window.setTimeout(() => {
      interruptTimeoutId.value = 0;
      if (!interruptPending.value) return;
      onInterruptRejected({
        reason: 'timeout',
        mode: 'timeout',
        threadId: requestThreadID,
      });
    }, 15000);
  }

  function isPauseMode() {
    return Boolean(props.interruptible);
  }

  function emitInterrupt(requestThreadID, logEventName) {
    interruptPending.value = true;
    interruptRequestThreadId.value = requestThreadID;
    armInterruptTimeout(requestThreadID);
    logInfo('ui', logEventName, {
      disabled: props.disabled,
      pause_ack: pauseAcknowledged.value,
      pending: true,
      has_input: Boolean(deps.hasReadyInput()),
      thread_id: requestThreadID,
    });
    emit('interrupt', {
      threadId: requestThreadID,
      confirm: (meta) => onInterruptConfirmed({
        ...meta,
        threadId: requestThreadID,
      }),
      reject: (meta) => onInterruptRejected({
        ...meta,
        threadId: requestThreadID,
      }),
    });
  }

  function onPrimaryAction(event) {
    if (isPauseMode()) {
      if (interruptPending.value) return;
      const requestThreadID = (props.threadId || '').toString();
      emitInterrupt(requestThreadID, 'composerBar.interrupt.click');
      return;
    }
    deps.onSend(event);
  }

  function onEscape(event) {
    if (!Boolean(props.interruptible)) return;
    if (interruptPending.value) {
      if (typeof event?.preventDefault === 'function') event.preventDefault();
      return;
    }
    const requestThreadID = (props.threadId || '').toString();
    if (!requestThreadID) return;
    if (typeof event?.preventDefault === 'function') event.preventDefault();
    emitInterrupt(requestThreadID, 'composerBar.interrupt.escape');
  }

  return {
    pauseAcknowledged,
    interruptPending,
    interruptRequestThreadId,
    interruptTimeoutId,
    clearInterruptTimeout,
    resetInterruptState,
    armInterruptTimeout,
    onInterruptConfirmed,
    onInterruptRejected,
    isPauseMode,
    onPrimaryAction,
    onEscape,
  };
}
