import { ref, computed, onMounted, onBeforeUnmount } from '../../lib/vue.esm-browser.prod.js';
import { logDebug } from '../services/log.js';

export const ProjectSelect = {
  name: 'ProjectSelect',
  props: {
    modelValue: { type: String, default: '.' },
    options: { type: Array, default: () => [] },
  },
  emits: ['update:modelValue', 'add-project', 'remove-project'],
  setup(props, { emit }) {
    const open = ref(false);
    const wrapRef = ref(null);
    const triggerRef = ref(null);
    const dropdownStyle = ref({});

    const selectedLabel = computed(() => {
      const match = (props.options || []).find((o) => o.value === props.modelValue);
      return match ? match.label : props.modelValue || '.';
    });

    function toggle() {
      if (!open.value && triggerRef.value) {
        const rect = triggerRef.value.getBoundingClientRect();
        dropdownStyle.value = {
          position: 'fixed',
          top: (rect.bottom + 4) + 'px',
          left: rect.left + 'px',
          minWidth: Math.max(rect.width, 220) + 'px',
        };
      }
      open.value = !open.value;
    }

    function selectItem(value) {
      logDebug('ui', 'projectSelect.changed', { value: value || '.' });
      emit('update:modelValue', value);
      open.value = false;
    }

    function removeItem(ev, value) {
      ev.stopPropagation();
      logDebug('ui', 'projectSelect.remove', { value });
      emit('remove-project', value);
    }

    function onAddProject() {
      logDebug('ui', 'projectSelect.add.click', {});
      emit('add-project');
      open.value = false;
    }

    function onClickOutside(ev) {
      if (wrapRef.value && !wrapRef.value.contains(ev.target)) {
        open.value = false;
      }
    }

    onMounted(() => {
      document.addEventListener('pointerdown', onClickOutside, true);
    });
    onBeforeUnmount(() => {
      document.removeEventListener('pointerdown', onClickOutside, true);
    });

    return {
      open,
      wrapRef,
      triggerRef,
      dropdownStyle,
      selectedLabel,
      toggle,
      selectItem,
      removeItem,
      onAddProject,
    };
  },
  template: `
    <div class="project-select-wrap" ref="wrapRef">
      <button ref="triggerRef" class="project-selector" @click="toggle" :title="modelValue">
        <svg class="project-selector-icon" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.7" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
          <path d="M3 7a2 2 0 0 1 2-2h4l2 2h8a2 2 0 0 1 2 2v8a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2V7z"></path>
        </svg>
        <span class="project-selector-text">{{ selectedLabel }}</span>
        <svg class="project-selector-chevron" viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
          <path d="M4 6l4 4 4-4"></path>
        </svg>
      </button>
      <div v-if="open" class="project-dropdown" :style="dropdownStyle">
        <div
          v-for="item in options"
          :key="item.value"
          class="project-dropdown-item"
          :class="{ selected: item.value === modelValue }"
          :title="item.full"
          @click="selectItem(item.value)"
        >
          <svg class="project-dropdown-item-icon" viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
            <path v-if="item.value === modelValue" d="M3 8l3 3 7-7"></path>
          </svg>
          <span class="project-dropdown-label">{{ item.label }}</span>
          <button
            v-if="item.value !== '.'"
            class="project-dropdown-remove"
            title="移除此项目"
            @click="removeItem($event, item.value)"
          >×</button>
        </div>
        <div class="project-dropdown-divider"></div>
        <div class="project-dropdown-item project-dropdown-add" @click="onAddProject">
          <svg viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true" style="width:13px;height:13px;flex-shrink:0;opacity:0.6">
            <path d="M8 3v10M3 8h10"></path>
          </svg>
          <span>添加项目</span>
        </div>
      </div>
    </div>
  `,
};

