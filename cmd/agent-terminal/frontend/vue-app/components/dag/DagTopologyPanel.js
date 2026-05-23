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

function nodeTitle(node, key) {
  return textValue(node?.title, node?.name, key) || key;
}

function dependsOn(node) {
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
        title: nodeTitle(node, key),
        dependsOn: dependsOn(node),
      };
    }));
    const mermaidSource = computed(() => {
      const lines = ['flowchart TD'];
      const knownIds = new Map(rows.value.map((row, index) => [row.key, `n${index + 1}`]));
      const missingIds = new Map();
      const idForDependency = (key) => {
        if (knownIds.has(key)) return knownIds.get(key);
        if (!missingIds.has(key)) missingIds.set(key, `d${missingIds.size + 1}`);
        return missingIds.get(key);
      };
      for (const row of rows.value) {
        lines.push(`  ${knownIds.get(row.key)}["${mermaidLabel(row.title)}"]`);
      }
      for (const row of rows.value) {
        const rowId = knownIds.get(row.key);
        for (const dep of row.dependsOn) {
          const depId = idForDependency(dep);
          if (!knownIds.has(dep)) lines.push(`  ${depId}["${mermaidLabel(dep)}"]`);
          lines.push(`  ${depId} --> ${rowId}`);
        }
      }
      return lines.join('\n');
    });
    return { rows, mermaidSource };
  },
  template: `
    <section class="dag-detail-section dag-topology-panel" data-testid="dag-topology-panel">
      <div class="dag-section-title">Topology</div>
      <div v-if="rows.length === 0" class="dag-console-muted" data-testid="dag-topology-empty">暂无拓扑</div>
      <pre v-else class="dag-topology-source" data-testid="dag-topology-mermaid">{{ mermaidSource }}</pre>
    </section>
  `,
};
