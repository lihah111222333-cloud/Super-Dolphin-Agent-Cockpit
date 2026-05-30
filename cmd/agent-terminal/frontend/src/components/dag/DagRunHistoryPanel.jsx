import React from 'react';
import { useVueSetup, val } from '../../utils/vue-compat.js';
import { DagRunHistoryPanel as VueComp } from './DagRunHistoryPanel.js';

export function DagRunHistoryPanel(props) {
  const emit = (event, ...args) => {
    const propName = 'on' + event.split('-').map(s => s[0].toUpperCase() + s.slice(1)).join('');
    props[propName]?.(...args);
  };
  const vm = useVueSetup(VueComp.setup, props, emit);

  const rows = val(vm.rows) || [];

  return (
    <section className="dag-detail-section dag-run-history" data-testid="dag-run-history">
      <div className="dag-section-title">最近运行</div>
      {rows.length === 0 ? (
        <div className="dag-console-muted" data-testid="dag-run-history-empty">暂无运行记录</div>
      ) : (
        <div className="dag-run-list">
          {rows.map((row) => (
            <button
              key={row.key}
              type="button"
              className={`dag-run-row ${props.selectedRunKey === row.key ? 'active' : ''}`}
              data-testid="dag-run-history-row"
              onClick={() => vm.selectRun(row)}
            >
              <span>{row.label}</span>
              <span>{row.status}</span>
              <small>{row.startedAt === '-' ? '时间未记录' : row.startedAt}</small>
            </button>
          ))}
        </div>
      )}
    </section>
  );
}
