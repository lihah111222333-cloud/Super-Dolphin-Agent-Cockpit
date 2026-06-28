import { reactive, ref } from '../../lib/vue.esm-browser.prod.js';
import { callAPI } from '../services/api.js';

export function memoryTemplateForType(type) {
  switch ((type || '').toString()) {
    case 'feedback':
      return '规则\n原因：\n如何应用：';
    case 'project':
      return '事实\n原因：\n如何应用：';
    case 'reference':
      return '指向：\n为什么重要：';
    default:
      return '用户偏好：';
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
    title: '',
    type: 'project',
    content: memoryTemplateForType('project'),
  });
}

/**
 * Memory editor state + handlers (create / edit / save / delete / template fill).
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
        title: detail?.title || '',
        type: detail?.type || 'project',
        content: detail?.content || '',
      });
      mode.value = 'edit';
      open.value = true;
    } catch (error) {
      setNotice('error', `加载失败：${toErrorMessage(error)}`);
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
    const name = (form.name || '').toString().trim();
    const description = (form.description || '').toString().trim();
    const content = (form.content || '').toString().trim();
    if (!name) {
      setNotice('error', '请先填写名称');
      return;
    }
    if (!description) {
      setNotice('error', '请先填写描述');
      return;
    }
    if (!content) {
      setNotice('error', '内容不能为空');
      return;
    }
    saving.value = true;
    try {
      await callAPI('ui/memory/entry/upsert', {
        cwd: currentCwd.value,
        target: form.target,
        existingPath: form.existingPath,
        name,
        description,
        title: (form.title || '').toString().trim(),
        type: form.type,
        content,
      });

      close();
      setNotice('info', '已保存');
      emit('refresh');
    } catch (error) {
      setNotice('error', `保存失败：${toErrorMessage(error)}`);
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
      setNotice('info', '已删除');
      emit('refresh');
    } catch (error) {
      setNotice('error', `删除失败：${toErrorMessage(error)}`);
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
 * Inline delete confirmation for memory cards.
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
      setNotice('info', `已删除：${req.name}`);
      target.value = null;
      emit('refresh');
    } catch (error) {
      setNotice('error', `删除失败：${toErrorMessage(error)}`);
    } finally {
      deleting.value = false;
    }
  }

  return reactive({ target, deleting, ask, cancel, confirm });
}
