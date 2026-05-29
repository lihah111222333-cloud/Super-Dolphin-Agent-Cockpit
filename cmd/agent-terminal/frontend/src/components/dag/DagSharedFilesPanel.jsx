import React from 'react';
import { useVueSetup, val } from '../../utils/vue-compat.js';
import { DagSharedFilesPanel as VueComp } from './DagSharedFilesPanel.js';

export function DagSharedFilesPanel(props) {
  const emit = (event, ...args) => {
    const propName = 'on' + event.split('-').map(s => s[0].toUpperCase() + s.slice(1)).join('');
    props[propName]?.(...args);
  };
  const vm = useVueSetup(VueComp.setup, props, emit);

  const rows = val(vm.rows) || [];

  return (
    <section className="dag-detail-section dag-sharedfiles-panel" data-testid="dag-sharedfiles-panel">
      <div className="dag-section-title">工作文件</div>
      {rows.length === 0 ? (
        <div className="dag-console-muted" data-testid="dag-sharedfiles-empty">暂无工作文件读写</div>
      ) : (
        <div className="dag-sharedfile-list">
          {rows.map((row) => (
            <div key={`${row.nodeKey}:${row.mode}:${row.path}`} className="dag-sharedfile-row">
              <span>{row.stepLabel}</span>
              <strong>{row.path}</strong>
              <small>{row.accessLabel}</small>
            </div>
          ))}
        </div>
      )}
    </section>
  );
}
