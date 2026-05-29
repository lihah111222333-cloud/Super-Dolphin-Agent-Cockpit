import React from 'react';
import { useVueSetup, val } from '../../utils/vue-compat.js';
import { DagTopologyPanel as VueComp } from './DagTopologyPanel.js';

export function DagTopologyPanel(props) {
  const emit = (event, ...args) => {
    const propName = 'on' + event.split('-').map(s => s[0].toUpperCase() + s.slice(1)).join('');
    props[propName]?.(...args);
  };
  const vm = useVueSetup(VueComp.setup, props, emit);

  const rows = val(vm.rows) || [];
  const mermaidSource = val(vm.mermaidSource);

  return (
    <section className="dag-detail-section dag-topology-panel" data-testid="dag-topology-panel">
      <div className="dag-section-title">流程图</div>
      {rows.length === 0 ? (
        <div className="dag-console-muted" data-testid="dag-topology-empty">暂无流程图</div>
      ) : (
        <pre className="dag-topology-source" data-testid="dag-topology-mermaid">{mermaidSource}</pre>
      )}
    </section>
  );
}
