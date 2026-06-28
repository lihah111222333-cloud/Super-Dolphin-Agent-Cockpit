import {
  ref,
  computed,
  watch,
  nextTick,
} from '../../lib/vue.esm-browser.prod.js';

function normalizeOptions(options) {
  if (!Array.isArray(options)) return [];
  return options
    .map((item) => (item || '').toString().trim())
    .filter(Boolean);
}

export const PathChoiceModal = {
  name: 'PathChoiceModal',
  props: {
    show: { type: Boolean, default: false },
    options: { type: Array, default: () => [] },
    title: { type: String, default: '选择文件路径' },
    truncated: { type: Boolean, default: false },
    onConfirm: { type: Function, default: null },
    onCancel: { type: Function, default: null },
  },
  setup(props) {
    const modalRef = ref(null);
    const normalizedOptions = computed(() => normalizeOptions(props.options));

    async function focusModal() {
      if (!props.show) return;
      await nextTick();
      modalRef.value?.focus?.();
    }

    function confirmPath(path) {
      const selectedPath = (path || '').toString().trim();
      if (!selectedPath) return;
      if (typeof props.onConfirm === 'function') props.onConfirm(selectedPath);
    }

    function cancelPathChoice() {
      if (typeof props.onCancel === 'function') props.onCancel();
    }

    function onEscapeKey(event) {
      if (typeof event?.stopPropagation === 'function') event.stopPropagation();
      if (typeof event?.preventDefault === 'function') event.preventDefault();
      cancelPathChoice();
    }

    watch(() => props.show, (show) => {
      if (!show) return;
      focusModal();
    }, { immediate: true });

    return {
      modalRef,
      normalizedOptions,
      confirmPath,
      cancelPathChoice,
      onEscapeKey,
    };
  },
  template: `
    <div
      v-if="show"
      ref="modalRef"
      class="modal-overlay"
      role="dialog"
      aria-modal="true"
      :aria-label="title || '选择文件路径'"
      tabindex="-1"
      data-testid="path-choice-modal"
      @click.self="cancelPathChoice"
      @keydown.esc.prevent="onEscapeKey"
    >
      <div class="modal-box" style="min-width:420px;max-width:760px;">
        <div class="modal-title">{{ title || '选择文件路径' }}</div>
        <div style="display:flex;flex-direction:column;gap:8px;max-height:320px;overflow:auto;">
          <div
            v-if="!normalizedOptions.length"
            data-testid="path-choice-empty"
            style="color:var(--text-muted);font-size:13px;padding:12px 0;text-align:center;"
          >没有可选路径</div>
          <button
            v-for="(option, index) in normalizedOptions"
            :key="option + ':' + index"
            class="btn btn-ghost"
            type="button"
            :title="option"
            :data-testid="'path-choice-option-' + index"
            @click="confirmPath(option)"
            style="display:flex;justify-content:flex-start;width:100%;cursor:pointer;overflow:hidden;"
          >
            <span style="font-family:var(--font-mono);text-align:left;overflow:hidden;text-overflow:ellipsis;white-space:nowrap;width:100%;">{{ option }}</span>
          </button>
        </div>
        <div
          v-if="truncated"
          data-testid="path-choice-truncated"
          style="margin-top:12px;color:var(--text-muted);font-size:12px;"
        >结果已截断，仅显示部分结果</div>
        <div class="modal-btns">
          <button class="btn btn-ghost" type="button" data-testid="path-choice-cancel" @click="cancelPathChoice">取消</button>
        </div>
      </div>
    </div>
  `,
};
