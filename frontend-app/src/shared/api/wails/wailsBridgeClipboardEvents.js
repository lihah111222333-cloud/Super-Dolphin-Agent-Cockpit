// @ts-nocheck

import { writeBridgeLog } from './wailsBridgeLogRuntime.js';
import { METHOD_IDS } from './wailsBridgeConstants.js';
import { subscribeRuntimeEvent } from './wailsBridgeTraceEvents.js';
import { callAPI, callByID } from './wailsBridgeRpc.js';
import { normalizeBridgeInputString } from './wailsBridgeNativeFiles.js';

function isDebugRuntimeShim() {
  return typeof window !== 'undefined' && window.__WAILS_SHIM_DEBUG__ === true;
}

async function copyTextToClipboard(text) {
  const value = normalizeBridgeInputString(text);
  if (!value) throw new Error('clipboard text is empty');

  const failures = [];
  if (await copyTextViaNativeBridge(value, failures)) return true;
  if (await copyTextViaClipboardAPI(value, failures)) return true;
  if (copyTextViaExecCommand(value, failures)) return true;

  throw new Error(`clipboard copy failed: ${failures.join('; ')}`);
}

async function copyTextViaNativeBridge(value, failures) {
  if (isDebugRuntimeShim()) return false;
  try {
    const res = await callAPI('ui/copyText', { text: value });
    if (res?.ok) return true;
    failures.push(`native ui/copyText returned ok=false${res?.error ? `: ${res.error}` : ''}`);
  }
  catch (error) {
    failures.push(`native ui/copyText failed: ${error.message || String(error)}`);
  }
  return false;
}

async function copyTextViaClipboardAPI(value, failures) {
  if (!navigator?.clipboard?.writeText) {
    failures.push('browser clipboard.writeText is unavailable');
    return false;
  }
  let copied = false;
  try {
    await navigator.clipboard.writeText(value);
    copied = true;
  }
  catch (error) {
    failures.push(`browser clipboard.writeText failed: ${error.message || String(error)}`);
    writeBridgeLog('warn', 'ui.copyText.clipboard_api_failed', { error: error.message || String(error) });
  }
  return copied;
}

function copyTextViaExecCommand(value, failures) {
  let copied = false;
  try {
    if (!document?.body || typeof document.execCommand !== 'function') {
      throw new Error('document.execCommand is unavailable');
    }
    const textarea = createClipboardTextarea(value);
    document.body.appendChild(textarea);
    const selection = document.getSelection?.();
    const ranges = getSelectionRanges(selection);
    try {
      textarea.focus();
      textarea.select();
      textarea.setSelectionRange?.(0, value.length);
      copied = document.execCommand('copy');
      if (!copied) throw new Error("document.execCommand('copy') returned false");
    }
    finally {
      document.body.removeChild(textarea);
      if (selection) {
        selection.removeAllRanges();
        ranges.forEach((range) => selection.addRange(range));
      }
    }
  }
  catch (error) {
    failures.push(`document.execCommand fallback failed: ${error.message || String(error)}`);
    writeBridgeLog('warn', 'ui.copyText.exec_command_failed', { error: error.message || String(error) });
  }
  return copied;
}

function createClipboardTextarea(value) {
  const textarea = document.createElement('textarea');
  textarea.value = value;
  textarea.style.position = 'fixed';
  textarea.style.top = '0';
  textarea.style.left = '-9999px';
  textarea.style.opacity = '0';
  textarea.setAttribute('readonly', '');
  return textarea;
}

function getSelectionRanges(selection) {
  if (!selection) return [];
  return Array.from({ length: selection.rangeCount }, (_, index) => selection.getRangeAt(index));
}

function beginTextClipboardWrite() {
  if (
    typeof navigator === 'undefined' ||
    typeof navigator.clipboard?.write !== 'function' ||
    typeof ClipboardItem === 'undefined' ||
    typeof Blob === 'undefined'
  ) {
    return null;
  }

  let settled = false;
  let resolveBlob;
  let rejectBlob;
  const blobPromise = new Promise((resolve, reject) => {
    resolveBlob = resolve;
    rejectBlob = reject;
  });

  let writePromise;
  try {
    writePromise = navigator.clipboard.write([
      new ClipboardItem({
        'text/plain': blobPromise,
      }),
    ]);
  }
  catch {
    return null;
  }

  writePromise.catch(() => {
    // commit() awaits writePromise and surfaces the clipboard write failure to the caller.
  });

  return {
    async commit(text) {
      if (settled) throw new Error('prepared clipboard write is already settled');
      const value = normalizeBridgeInputString(text);
      if (!value) {
        settled = true;
        rejectBlob(new Error('clipboard text is empty'));
        throw new Error('clipboard text is empty');
      }
      settled = true;
      resolveBlob(new Blob([value], { type: 'text/plain' }));
      await writePromise;
      return true;
    },
    cancel(reason) {
      if (settled) return;
      settled = true;
      rejectBlob(reason instanceof Error ? reason : new Error('clipboard write cancelled'));
    },
  };
}

async function resolveThreadIdentity(threadId) {
  const id = normalizeBridgeInputString(threadId);
  if (!id) return {};
  const res = await callAPI('thread/resolve', { threadId: id });
  return res && typeof res === 'object' ? res : {};
}

async function getBuildInfo() {
  const raw = await callByID(METHOD_IDS.GET_BUILD_INFO);
  return raw && typeof raw === 'object' ? raw : {};
}

function onAgentEvent(callback) {
  return subscribeRuntimeEvent('agent-event', callback, {
    callbackFailedLog: 'agent.callback.failed',
    subscribeUnavailableLog: 'agent.subscribe.unavailable',
    subscribeReadyLog: 'agent.subscribe.ready',
    unsubscribeDoneLog: 'agent.unsubscribe.done',
  });
}

function onBridgeEvent(callback, options = {}) {
  return subscribeRuntimeEvent('bridge-event', callback, {
    callbackFailedLog: 'bridge.callback.failed',
    subscribeUnavailableLog: 'bridge.subscribe.unavailable',
    subscribeReadyLog: 'bridge.subscribe.ready',
    unsubscribeDoneLog: 'bridge.unsubscribe.done',
    ...options,
  });
}

function onFilesDropped(callback) {
  return subscribeRuntimeEvent('files-dropped', callback, {
    callbackFailedLog: 'filesDropped.callback.failed',
    subscribeUnavailableLog: 'filesDropped.subscribe.unavailable',
    subscribeReadyLog: 'filesDropped.subscribe.ready',
    unsubscribeDoneLog: 'filesDropped.unsubscribe.done',
  });
}

function onAppWillQuit(callback) {
  return subscribeRuntimeEvent('app-will-quit', callback, {
    callbackFailedLog: 'appWillQuit.callback.failed',
    subscribeUnavailableLog: 'appWillQuit.subscribe.unavailable',
    subscribeReadyLog: 'appWillQuit.subscribe.ready',
    unsubscribeDoneLog: 'appWillQuit.unsubscribe.done',
  });
}

function onRuntimeReconnect(callback) {
  return subscribeRuntimeEvent('wails:loaded', callback, {
    callbackFailedLog: 'reconnect.callback.failed',
    subscribeUnavailableLog: 'reconnect.subscribe.unavailable',
    subscribeReadyLog: 'reconnect.subscribe.ready',
    unsubscribeDoneLog: 'reconnect.unsubscribe.done',
  });
}

export {
  isDebugRuntimeShim, copyTextViaNativeBridge, copyTextViaClipboardAPI, copyTextViaExecCommand, createClipboardTextarea, getSelectionRanges,
  copyTextToClipboard, beginTextClipboardWrite, resolveThreadIdentity, getBuildInfo, onAgentEvent, onBridgeEvent, onFilesDropped,
  onAppWillQuit, onRuntimeReconnect,
};
