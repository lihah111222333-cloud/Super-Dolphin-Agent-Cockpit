import { reactive, ref } from '../../lib/vue.esm-browser.prod.js';
import { callAPI } from '../services/api.js';

export function memoryTemplateForType(type) {
  switch ((type || '').toString()) {
    case 'feedback':
      return 'rule\nWhy: \nHow to apply: ';
    case 'project':
      return 'fact\nWhy: \nHow to apply: ';
    case 'reference':
      return 'Pointer: \nWhy it matters: ';
    default:
      return 'User preference: ';
  }
}

function toErrorMessage(error) {
  return (
    (error && typeof error === 'object' && typeof error.message === 'string' ? error.message : '')
    || String(error || '')
  ).toString().trim();
}

export function resetMemoryForm(form, target = 'private') {
  Object.assign(form, {
    target,
    existingPath: '',
    name: '',
    description: '',
    type: 'project',
    content: memoryTemplateForType('project'),
  });
}

export function resetAgentForm(form, scope = 'project') {
  Object.assign(form, {
    scope,
    agentType: '',
    path: '',
    content: '',
  });
}

/**
 * Durable memory editor state + handlers (create / edit / save / delete / template fill).
 *
 * Callers provide:
 *   - currentCwd: ref-like with .value
 *   - setNotice: (level, message) => void
 *   - setBusy: (path) => void  — reports async load progress
 *   - emit: Vue emit
 */
export function useDurableMemoryEditor({ currentCwd, setNotice, setBusy, emit }) {
  const open = ref(false);
  const mode = ref('create');
  const saving = ref(false);
  const deleting = ref(false);
  const form = reactive({});
  resetMemoryForm(form);

  function openCreate(target) {
    resetMemoryForm(form, target || 'private');
    mode.value = 'create';
    open.value = true;
  }

  async function openEdit(target, entry) {
    const path = (entry?.path || '').toString().trim();
    if (!path) return;
    setBusy(`${target}:${path}`);
    try {
      const detail = await callAPI('ui/memory/entry/get', {
        cwd: currentCwd.value,
        target,
        path,
      });
      Object.assign(form, {
        target,
        existingPath: detail?.path || path,
        name: detail?.name || '',
        description: detail?.description || '',
        type: detail?.type || 'project',
        content: detail?.content || '',
      });
      mode.value = 'edit';
      open.value = true;
    } catch (error) {
      setNotice('error', `加载 durable memory 失败：${toErrorMessage(error)}`);
    } finally {
      setBusy('');
    }
  }

  function close() {
    open.value = false;
    resetMemoryForm(form, form.target || 'private');
  }

  async function save() {
    if (saving.value) return;
    saving.value = true;
    try {
      await callAPI('ui/memory/entry/upsert', {
        cwd: currentCwd.value,
        target: form.target,
        existingPath: form.existingPath,
        name: form.name,
        description: form.description,
        type: form.type,
        content: form.content,
      });
      close();
      setNotice('info', 'durable memory 已保存。');
      emit('refresh');
    } catch (error) {
      setNotice('error', `保存 durable memory 失败：${toErrorMessage(error)}`);
    } finally {
      saving.value = false;
    }
  }

  async function remove() {
    if (!form.existingPath || deleting.value) return;
    deleting.value = true;
    try {
      await callAPI('ui/memory/entry/delete', {
        cwd: currentCwd.value,
        target: form.target,
        path: form.existingPath,
      });
      close();
      setNotice('info', 'durable memory 已删除。');
      emit('refresh');
    } catch (error) {
      setNotice('error', `删除 durable memory 失败：${toErrorMessage(error)}`);
    } finally {
      deleting.value = false;
    }
  }

  function fillTemplate() {
    form.content = memoryTemplateForType(form.type);
  }

  // Wrap in reactive so property access in templates auto-unwraps nested refs.
  // Vue 3 only auto-unwraps refs that are returned directly from setup(); nested
  // refs inside a plain object require reactive() or explicit .value access.
  return reactive({ open, mode, saving, deleting, form, openCreate, openEdit, close, save, remove, fillTemplate });
}

/**
 * Agent-scoped MEMORY.md editor.
 */
export function useAgentMemoryEditor({ currentCwd, setNotice, setBusy, emit }) {
  const open = ref(false);
  const saving = ref(false);
  const form = reactive({});
  resetAgentForm(form);

  function openCreate(scope) {
    resetAgentForm(form, scope || 'project');
    open.value = true;
  }

  async function openEdit(scope, entry) {
    const agentType = (entry?.agentType || '').toString().trim();
    if (!agentType) return;
    setBusy(`agent:${scope}:${agentType}`);
    try {
      const detail = await callAPI('ui/memory/agent/get', {
        cwd: currentCwd.value,
        scope,
        agentType,
      });
      Object.assign(form, {
        scope,
        agentType: detail?.agentType || agentType,
        path: detail?.path || '',
        content: detail?.content || '',
      });
      open.value = true;
    } catch (error) {
      setNotice('error', `加载 Agent 记忆失败：${toErrorMessage(error)}`);
    } finally {
      setBusy('');
    }
  }

  function close() {
    open.value = false;
    resetAgentForm(form, form.scope || 'project');
  }

  async function save() {
    if (saving.value) return;
    saving.value = true;
    try {
      await callAPI('ui/memory/agent/save', {
        cwd: currentCwd.value,
        scope: form.scope,
        agentType: form.agentType,
        content: form.content,
      });
      close();
      setNotice('info', 'Agent 记忆已保存。清空内容后保存即可重置。');
      emit('refresh');
    } catch (error) {
      setNotice('error', `保存 Agent 记忆失败：${toErrorMessage(error)}`);
    } finally {
      saving.value = false;
    }
  }

  return reactive({ open, saving, form, openCreate, openEdit, close, save });
}

/**
 * Inline delete confirmation for durable memory cards.
 * Pairs with a modal in the template driven by `target` being non-null.
 */
export function useInlineDeleteConfirm({ currentCwd, setNotice, emit }) {
  const target = ref(null); // { target, path, name } or null
  const deleting = ref(false);

  function ask(requestTarget, entry) {
    if (!entry?.path) return;
    target.value = {
      target: requestTarget,
      path: entry.path,
      name: entry.name || entry.path,
    };
  }

  function cancel() {
    if (deleting.value) return;
    target.value = null;
  }

  async function confirm() {
    const req = target.value;
    if (!req || deleting.value) return;
    deleting.value = true;
    try {
      await callAPI('ui/memory/entry/delete', {
        cwd: currentCwd.value,
        target: req.target,
        path: req.path,
      });
      setNotice('info', `durable memory 已删除：${req.name}`);
      target.value = null;
      emit('refresh');
    } catch (error) {
      setNotice('error', `删除 durable memory 失败：${toErrorMessage(error)}`);
    } finally {
      deleting.value = false;
    }
  }

  return reactive({ target, deleting, ask, cancel, confirm });
}
