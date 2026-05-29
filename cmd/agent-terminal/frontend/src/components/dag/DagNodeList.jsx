import React from 'react';
import { useVueSetup, val } from '../../utils/vue-compat.js';
import { DagNodeList as VueComp } from './DagNodeList.js';

export function DagNodeList(props) {
  const emit = (event, ...args) => {
    const propName = 'on' + event.split('-').map(s => s[0].toUpperCase() + s.slice(1)).join('');
    props[propName]?.(...args);
  };
  const vm = useVueSetup(VueComp.setup, props, emit);

  const rows = val(vm.rows) || [];

  return (
    <section className="dag-detail-section dag-node-list" data-testid="dag-node-list">
      <div className="dag-section-title">步骤</div>
      {rows.length === 0 ? (
        <div className="dag-console-muted" data-testid="dag-node-list-empty">暂无步骤</div>
      ) : (
        <div className="dag-node-list-grid">
          {rows.map((row) => (
            <article key={row.key} className="dag-node-card">
              <div className="dag-node-card-head">
                <strong>{row.title}</strong>
                <span>{row.status}</span>
              </div>
              {row.spawningThreadId !== '-' && (
                <button
                  type="button"
                  className="dag-link-button"
                  data-testid="dag-node-open-chat"
                  onClick={() => vm.openChat(row)}
                >
                  {row.chatLabel}
                </button>
              )}
            </article>
          ))}
        </div>
      )}
    </section>
  );
}
