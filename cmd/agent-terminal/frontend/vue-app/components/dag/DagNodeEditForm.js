import { computed, reactive, watch } from '../../../lib/vue.esm-browser.prod.js';
import {
  appendCurrentOption,
  canonicalizeModelValue,
  MODEL_OPTIONS_BY_PROVIDER,
} from '../../provider-config-options.js';

function textValue(...values) {
  for (const value of values) {
    if (value === null || value === undefined) continue;
    const text = value.toString().trim();
    if (text) return text;
  }
  return '';
}

function parseObject(value) {
  if (!value) return {};
  if (typeof value === 'object') return value;
  if (typeof value !== 'string') return {};
  try {
    const parsed = JSON.parse(value);
    return parsed && typeof parsed === 'object' ? parsed : {};
  } catch {
    return {};
  }
}

function parseList(value) {
  if (Array.isArray(value)) return value.map((item) => textValue(item)).filter(Boolean);
  if (typeof value !== 'string') return [];
  return value.split(',').map((item) => item.trim()).filter(Boolean);
}

function nodeKey(node, index) {
  return textValue(node?.node_key, node?.nodeKey, node?.key, node?.id) || `node_${index + 1}`;
}

function nodeType(node) {
  return textValue(node?.node_type, node?.nodeType, node?.type);
}

function nodeDependsOn(node) {
  const raw = node?.depends_on || node?.dependsOn || [];
  if (Array.isArray(raw)) return raw;
  if (typeof raw !== 'string') return [];
  try {
    const parsed = JSON.parse(raw);
    return Array.isArray(parsed) ? parsed : [];
  } catch {
    return raw.split(',');
  }
}

function clonePlainObject(value) {
  return JSON.parse(JSON.stringify(value && typeof value === 'object' ? value : {}));
}

function configFromNode(node) {
  return clonePlainObject(parseObject(node?.config));
}

function selectedAgent(nodes, selectedKey) {
  const agents = nodes.filter((node) => nodeType(node) === 'agent');
  return agents.find((node, index) => nodeKey(node, index) === selectedKey) || agents[0] || null;
}

export const DagNodeEditForm = {
  name: 'DagNodeEditForm',
  props: {
    nodes: { type: Array, default: () => [] },
    savingNodeKey: { type: String, default: '' },
    saveError: { type: [String, Object], default: null },
  },
  emits: ['save-agent-node'],
  setup(props, { emit }) {
    const form = reactive({
      nodeKey: '',
      title: '',
      provider: '',
      model: '',
      promptKey: '',
      firstTurn: '',
      dependsOn: '',
      fromNodes: '',
      fromSharedfiles: '',
      toSharedfilePath: '',
      lockMode: 'exclusive',
      toNodeResult: false,
    });
    const agentNodes = computed(() => (Array.isArray(props.nodes) ? props.nodes : []).filter((node) => nodeType(node) === 'agent'));
    const selected = computed(() => selectedAgent(agentNodes.value, form.nodeKey));
    const saveErrorText = computed(() => {
      const err = props.saveError;
      if (!err) return '';
      if (typeof err === 'string') return err;
      return err.message || JSON.stringify(err);
    });
    const modelOptions = computed(() => {
      const provider = form.provider;
      const options = MODEL_OPTIONS_BY_PROVIDER[provider] || [];
      return appendCurrentOption(options, form.model, (value) => value);
    });

    watch(
      () => selected.value,
      (node) => {
        if (!node) return;
        const key = nodeKey(node, agentNodes.value.indexOf(node));
        const config = configFromNode(node);
        const exec = parseObject(config.exec);
        const inputs = parseObject(config.inputs);
        const outputs = parseObject(config.outputs);
        const target = parseObject(outputs.to_sharedfile);
        form.nodeKey = key;
        form.title = textValue(node.title, node.name, key);
        form.provider = textValue(exec.provider);
        form.model = canonicalizeModelValue(form.provider, exec.model);
        form.promptKey = textValue(exec.prompt_key, exec.promptKey);
        form.firstTurn = textValue(config.first_turn, config.firstTurn);
        form.dependsOn = parseList(nodeDependsOn(node)).join(', ');
        form.fromNodes = parseList(inputs.from_nodes).join(', ');
        form.fromSharedfiles = parseList(inputs.from_sharedfiles).join(', ');
        form.toSharedfilePath = textValue(target.path);
        form.lockMode = textValue(target.lock_mode, target.lockMode) || 'exclusive';
        form.toNodeResult = Boolean(outputs.to_node_result || outputs.toNodeResult);
      },
      { immediate: true },
    );

    function chooseNode(event) {
      form.nodeKey = event?.target?.value || '';
    }

    function buildConfig() {
      const current = configFromNode(selected.value);
      const exec = parseObject(current.exec);
      if (form.provider) {
        exec.provider = form.provider;
      } else {
        delete exec.provider;
      }
      exec.model = form.model;
      exec.prompt_key = form.promptKey;
      current.exec = exec;
      current.first_turn = form.firstTurn;
      current.inputs = {
        ...parseObject(current.inputs),
        from_nodes: parseList(form.fromNodes),
        from_sharedfiles: parseList(form.fromSharedfiles),
      };
      current.outputs = {
        ...parseObject(current.outputs),
        to_node_result: Boolean(form.toNodeResult),
      };
      if (form.toSharedfilePath.trim()) {
        current.outputs.to_sharedfile = {
          path: form.toSharedfilePath.trim(),
          lock_mode: form.lockMode,
        };
      } else {
        delete current.outputs.to_sharedfile;
      }
      return current;
    }

    function submit() {
      emit('save-agent-node', {
        nodeKey: form.nodeKey,
        title: form.title,
        dependsOn: parseList(form.dependsOn),
        config: buildConfig(),
      });
    }

    return { agentNodes, chooseNode, form, modelOptions, parseList, saveErrorText, submit };
  },
  template: `
    <section class="dag-detail-section dag-node-edit-form" data-testid="dag-node-edit-form">
      <div class="dag-section-title">Agent Node</div>
      <div v-if="agentNodes.length === 0" class="dag-console-muted" data-testid="dag-node-edit-empty">暂无 agent 节点</div>
      <form v-else class="dag-node-edit-grid" @submit.prevent="submit">
        <label>
          <span>Node</span>
          <select :value="form.nodeKey" data-testid="dag-node-edit-select" @change="chooseNode">
            <option v-for="(node, index) in agentNodes" :key="node.node_key || node.nodeKey || index" :value="node.node_key || node.nodeKey || node.key || node.id">
              {{ node.title || node.name || node.node_key || node.nodeKey }}
            </option>
          </select>
        </label>
        <label>
          <span>Title</span>
          <input v-model="form.title" data-testid="dag-node-edit-title" />
        </label>
        <label>
          <span>Provider</span>
          <select v-model="form.provider" data-testid="dag-node-edit-provider">
            <option value="">默认</option>
            <option value="claude">claude</option>
            <option value="codex">codex</option>
          </select>
        </label>
        <label>
          <span>Model</span>
          <select v-model="form.model" data-testid="dag-node-edit-model">
            <option value="">默认</option>
            <option v-for="option in modelOptions" :key="option.value" :value="option.value">{{ option.label }}</option>
          </select>
        </label>
        <label>
          <span>Prompt Key</span>
          <input v-model="form.promptKey" data-testid="dag-node-edit-prompt-key" />
        </label>
        <label>
          <span>Depends On</span>
          <input v-model="form.dependsOn" data-testid="dag-node-edit-depends-on" />
        </label>
        <label class="dag-node-edit-wide">
          <span>Prompt</span>
          <textarea v-model="form.firstTurn" rows="4" data-testid="dag-node-edit-first-turn"></textarea>
        </label>
        <label>
          <span>Input Nodes</span>
          <input v-model="form.fromNodes" data-testid="dag-node-edit-input-nodes" />
        </label>
        <label>
          <span>Input Shared Files</span>
          <input v-model="form.fromSharedfiles" data-testid="dag-node-edit-input-sharedfiles" />
        </label>
        <label>
          <span>Output File</span>
          <input v-model="form.toSharedfilePath" data-testid="dag-node-edit-output-file" />
        </label>
        <label>
          <span>Lock</span>
          <select v-model="form.lockMode" data-testid="dag-node-edit-lock-mode">
            <option value="exclusive">exclusive</option>
            <option value="append">append</option>
            <option value="shared">shared</option>
          </select>
        </label>
        <label class="dag-node-edit-check">
          <input type="checkbox" v-model="form.toNodeResult" data-testid="dag-node-edit-to-result" />
          <span>to_node_result</span>
        </label>
        <div v-if="saveErrorText" class="dag-console-error-inline dag-node-edit-wide" data-testid="dag-node-edit-error">{{ saveErrorText }}</div>
        <div class="dag-node-edit-actions">
          <button type="submit" class="btn btn-primary" data-testid="dag-node-edit-save" :disabled="savingNodeKey === form.nodeKey">
            {{ savingNodeKey === form.nodeKey ? '保存中' : 'Save Node' }}
          </button>
        </div>
      </form>
    </section>
  `,
};
