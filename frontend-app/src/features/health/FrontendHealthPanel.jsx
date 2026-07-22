import React, { useEffect, useState } from 'react';
import {
  clearFrontendHealth,
  frontendHealthIdentity,
  frontendHealthStateSnapshot,
  subscribeFrontendHealth,
} from '../../shared/diagnostics/frontendHealthStore.js';
import './FrontendHealthPanel.css';

function formatHealthTime(value) {
  return value.replace('T', ' ');
}

function useFrontendHealthState() {
  const [state, setState] = useState(() => frontendHealthStateSnapshot());
  useEffect(() => subscribeFrontendHealth(() => setState(frontendHealthStateSnapshot())), []);
  return state;
}

export function FrontendHealthPanel() {
  const { persistence, records } = useFrontendHealthState();
  return (
    <section className="settings-card frontend-health-panel fusion-surface" data-testid="frontend-health-panel">
      <div className="frontend-health-header">
        <div>
          <h2>Health</h2>
          <p>持久记录桌面操作失败；详细原因通过诊断 ID 定位，不在页面展示。</p>
        </div>
        {records.length > 0 ? <button type="button" className="btn secondary" onClick={clearFrontendHealth}>清空</button> : null}
      </div>
      {persistence.status === 'failed' ? (
        <div className="frontend-health-persistence-failure" role="alert">
          <strong>{persistence.title}</strong>
          <p>{persistence.message}</p>
          <code>{persistence.diagnosticId}</code>
        </div>
      ) : null}
      {records.length === 0 ? <div className="empty-state">当前没有操作失败记录</div> : (
        <ol className="frontend-health-list" aria-label="桌面 Health 记录">
          {records.map((record) => (
            <li key={frontendHealthIdentity(record)}>
              <div className="frontend-health-record-title">
                <strong>{record.title}</strong>
                <span>{record.actionId}</span>
              </div>
              <p>{record.message}</p>
              <div className="frontend-health-record-meta">
                <time dateTime={record.lastOccurredAt}>{formatHealthTime(record.lastOccurredAt)}</time>
                <span>出现 {record.occurrences} 次</span>
                <code>{record.diagnosticId}</code>
              </div>
            </li>
          ))}
        </ol>
      )}
    </section>
  );
}
