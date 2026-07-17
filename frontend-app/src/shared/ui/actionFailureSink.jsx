import React, { useEffect, useSyncExternalStore } from 'react';
import {
  clearVisibleActionFailure,
  subscribeVisibleActionFailure,
  visibleActionFailureSnapshot,
} from './actionFailureSink.js';
import { OverlayPortal } from './OverlayPortal.jsx';

export function ActionFailureSink() {
  const failure = useSyncExternalStore(
    subscribeVisibleActionFailure,
    visibleActionFailureSnapshot,
    visibleActionFailureSnapshot,
  );
  useEffect(() => () => clearVisibleActionFailure(), []);
  if (!failure) return null;
  const { publicError, retry } = failure;
  const runRetry = () => {
    clearVisibleActionFailure();
    retry();
  };
  return (
    <OverlayPortal>
      <output className="global-action-failure" role="alert" data-testid="global-action-failure">
        <span>
          <strong>{publicError.title}</strong>
          <span>{publicError.message}</span>
          <small>诊断 ID：{publicError.diagnosticId}</small>
        </span>
        {publicError.retryable && typeof retry === 'function' ? (
          <button type="button" onClick={runRetry}>重试</button>
        ) : null}
        <button type="button" aria-label="关闭错误提示" onClick={clearVisibleActionFailure}>关闭</button>
      </output>
    </OverlayPortal>
  );
}
