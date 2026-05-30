// RoleBar.js — 角色横栏组件（角色选择 + CRUD 弹窗）
import { h, defineComponent, ref, reactive, computed } from '../../lib/vue.esm-browser.prod.js';

const PRESET_EMOJIS = [
  '💻','📋','🎨','🔧','📊','🧪','📝','🎯','🛠️','📦',
  '🔍','🌐','🤖','📐','💡','🔒','🎮','📈','🧩','⚙️',
];

export const RoleBar = defineComponent({
  name: 'RoleBar',
  props: {
    roles:        { type: Array,   default: () => [] },
    activeKey:    { type: String,  default: 'all' },
    promptCounts: { type: Object,  default: () => ({}) },
    disabled:     { type: Boolean, default: false },
  },
  emits: ['select', 'update:roles'],
  setup(props, { emit }) {
    // ── internal state ──
    const editorOpen         = ref(false);
    const editorMode         = ref('create');        // 'create' | 'edit'
    const editForm           = reactive({ key: '', name: '', icon: '💻' });
    const editingOriginalKey = ref('');

    // ── computed ──
    const totalCount = computed(() => {
      return Object.values(props.promptCounts || {}).reduce((a, b) => a + b, 0);
    });

    // ── helpers ──
    function resetForm() {
      editForm.key  = '';
      editForm.name = '';
      editForm.icon = '💻';
      editingOriginalKey.value = '';
    }

    function openCreate() {
      if (props.disabled) return;
      editorMode.value = 'create';
      resetForm();
      editorOpen.value = true;
    }

    function openEdit(role) {
      if (props.disabled) return;
      editorMode.value = 'edit';
      editForm.key  = role.key;
      editForm.name = role.name;
      editForm.icon = role.icon || '💻';
      editingOriginalKey.value = role.key;
      editorOpen.value = true;
    }

    function closeEditor() {
      editorOpen.value = false;
      resetForm();
    }

    function save() {
      if (props.disabled) return;
      const trimmed = editForm.name.trim();
      if (!trimmed) return;

      const roles = [...(props.roles || [])];

      if (editorMode.value === 'create') {
        const newKey = 'role_' + Date.now().toString(36);
        roles.push({ key: newKey, name: trimmed, icon: editForm.icon });
      } else {
        // edit — keep original key
        const idx = roles.findIndex(r => r.key === editingOriginalKey.value);
        if (idx !== -1) {
          roles[idx] = { ...roles[idx], name: trimmed, icon: editForm.icon };
        }
      }

      emit('update:roles', roles);
      closeEditor();
    }

    function deleteRole() {
      if (props.disabled) return;
      const roles = (props.roles || []).filter(r => r.key !== editingOriginalKey.value);
      emit('update:roles', roles);
      closeEditor();
    }

    // ── render ──
    return () => {
      const children = [];

      // ---- role cards ----
      (props.roles || []).forEach(role => {
        const isActive = props.activeKey === role.key;
        const count    = (props.promptCounts || {})[role.key] || 0;

        children.push(
          h('div', {
            class: ['sp-role-card', { active: isActive }],
            onClick: () => { if (!props.disabled) emit('select', role.key); },
          }, [
            // edit button (top-right, visible on hover via CSS)
            h('span', {
              class: 'sp-role-edit-btn',
              onClick: (ev) => { ev.stopPropagation(); openEdit(role); },
            }, '✎'),
            h('span', { class: 'sp-role-icon' }, role.icon || '💻'),
            h('span', { class: 'sp-role-name' }, role.name),
            h('span', { class: 'sp-role-count' }, `${count} 条`),
          ]),
        );
      });

      // ---- "全部" card ----
      children.push(
        h('div', {
          class: ['sp-role-card', { active: props.activeKey === 'all' }],
          onClick: () => { if (!props.disabled) emit('select', 'all'); },
        }, [
          h('span', { class: 'sp-role-icon' }, '📂'),
          h('span', { class: 'sp-role-name' }, '全部'),
          h('span', { class: 'sp-role-count' }, `${totalCount.value} 条`),
        ]),
      );

      // ---- "+ 新建角色" card ----
      children.push(
        h('div', {
          class: 'sp-role-card sp-role-add',
          onClick: openCreate,
        }, [
          h('span', { class: 'sp-role-icon' }, '+'),
          h('span', { class: 'sp-role-name' }, '新建角色'),
        ]),
      );

      // ---- bar wrapper ----
      const bar = h('div', { class: 'sp-role-bar' }, children);

      // ---- editor modal (overlay) ----
      let modal = null;
      if (editorOpen.value) {
        const isCreate = editorMode.value === 'create';

        // emoji grid
        const emojiItems = PRESET_EMOJIS.map(e =>
          h('span', {
            class: ['sp-emoji-item', { selected: e === editForm.icon }],
            onClick: () => { editForm.icon = e; },
          }, e),
        );

        const actionButtons = [];

        // delete button (edit mode only)
        if (!isCreate) {
          actionButtons.push(
            h('button', {
              class: 'is-danger',
              disabled: props.disabled,
              onClick: deleteRole,
            }, '删除角色'),
          );
        }

        // cancel
        actionButtons.push(
          h('button', { onClick: closeEditor }, '取消'),
        );

        // save
        actionButtons.push(
          h('button', {
            class: 'sp-save-btn',
            disabled: props.disabled || !editForm.name.trim(),
            onClick: save,
          }, '保存'),
        );

        modal = h('div', { class: 'sp-role-editor-overlay' }, [
          h('div', { class: 'sp-role-editor-modal' }, [
            // head
            h('div', { class: 'sp-role-editor-head' }, [
              h('span', null, isCreate ? '新建角色' : '编辑角色'),
              h('button', { onClick: closeEditor }, '×'),
            ]),
            // body
            h('div', { class: 'sp-role-editor-body' }, [
              h('label', null, '角色名称'),
              h('input', {
                value: editForm.name,
                placeholder: '如：运营、测试',
                onInput: (ev) => { editForm.name = ev.target.value; },
              }),
              h('label', null, '选择图标'),
              h('div', { class: 'sp-emoji-grid' }, emojiItems),
            ]),
            // actions
            h('div', { class: 'sp-role-editor-actions' }, actionButtons),
          ]),
        ]);
      }

      return h('div', null, [bar, modal]);
    };
  },
});
