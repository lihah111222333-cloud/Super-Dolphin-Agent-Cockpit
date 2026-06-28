import { computed } from '../../../lib/vue.esm-browser.prod.js';

function textValue(...values) {
  for (const value of values) {
    if (value === null || value === undefined) continue;
    const text = value.toString().trim();
    if (text) return text;
  }
  return '';
}

function nodeKey(node, index) {
  return textValue(node?.node_key, node?.nodeKey, node?.key, node?.id) || `node_${index + 1}`;
}

function nodeTitle(node, index) {
  return textValue(node?.title, node?.name) || `步骤 ${index + 1}`;
}

function dependencyKeys(node) {
  const raw = node?.depends_on || node?.dependsOn || [];
  if (Array.isArray(raw)) return raw.map((item) => textValue(item)).filter(Boolean);
  if (typeof raw !== 'string') return [];
  try {
    const parsed = JSON.parse(raw);
    return Array.isArray(parsed) ? parsed.map((item) => textValue(item)).filter(Boolean) : [];
  } catch {
    return raw.split(',').map((item) => item.trim()).filter(Boolean);
  }
}

function mermaidLabel(value) {
  return value.replace(/\\/g, '\\\\').replace(/"/g, '\\"');
}

export const DagTopologyPanel = {
  name: 'DagTopologyPanel',
  props: {
    nodes: { type: Array, default: () => [] },
  },
  setup(props) {
    const rows = computed(() => (Array.isArray(props.nodes) ? props.nodes : []).map((node, index) => {
      const key = nodeKey(node, index);
      return {
        key,
        title: nodeTitle(node, index),
        dependsOn: dependencyKeys(node),
      };
    }));
    const mermaidSource = computed(() => {
      const lines = ['flowchart TD'];
      const knownIds = new Map(rows.value.map((row, index) => [row.key, `n${index + 1}`]));
      const missingLabels = new Map();
      const missingIds = new Map();
      const idForDependency = (key) => {
        if (knownIds.has(key)) return knownIds.get(key);
        if (!missingIds.has(key)) missingIds.set(key, `d${missingIds.size + 1}`);
        return missingIds.get(key);
      };
      const labelForDependency = (key) => {
        if (!missingLabels.has(key)) missingLabels.set(key, `外部依赖 ${missingLabels.size + 1}`);
        return missingLabels.get(key);
      };
      for (const row of rows.value) {
        lines.push(`  ${knownIds.get(row.key)}["${mermaidLabel(row.title)}"]`);
      }
      const visibleRows = rows.value.map((row) => ({
        ...row,
        dependsOn: row.dependsOn.map((key) => ({
          key,
          label: knownIds.has(key)
            ? rows.value.find((item) => item.key === key)?.title || labelForDependency(key)
            : labelForDependency(key),
        })),
      }));
      for (const row of rows.value) {
        const rowId = knownIds.get(row.key);
        for (const dep of visibleRows.find((item) => item.key === row.key)?.dependsOn || []) {
          const depId = idForDependency(dep.key);
          if (!knownIds.has(dep.key)) lines.push(`  ${depId}["${mermaidLabel(dep.label)}"]`);
          lines.push(`  ${depId} --> ${rowId}`);
        }
      }
      return lines.join('\n');
    });
    const visibleRows = computed(() => {
      const knownKeys = new Set(rows.value.map((row) => row.key));
      const missingLabels = new Map();
      const labelForDependency = (key) => {
        if (!missingLabels.has(key)) missingLabels.set(key, `外部依赖 ${missingLabels.size + 1}`);
        return missingLabels.get(key);
      };
      return rows.value.map((row) => ({
        ...row,
        dependsOn: row.dependsOn.map((key) => ({
          key,
          label: knownKeys.has(key)
            ? rows.value.find((item) => item.key === key)?.title || labelForDependency(key)
            : labelForDependency(key),
        })),
      }));
    });
    return { rows: visibleRows, mermaidSource };
  },
  template: `
    <section class="dag-detail-section dag-topology-panel" data-testid="dag-topology-panel">
      <div class="dag-section-title">流程图</div>
      <div v-if="rows.length === 0" class="dag-console-muted" data-testid="dag-topology-empty">暂无流程图</div>
      <pre v-else class="dag-topology-source" data-testid="dag-topology-mermaid">{{ mermaidSource }}</pre>
    </section>
  `,
};
