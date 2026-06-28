// @ts-nocheck
// Phase 2: Composer 上方的「新建继承对话」草稿卡片。
// 状态来自父级传入的 forkDraft（reactive）；本组件只负责 UI 与事件。
import { ref, computed, watch, nextTick } from '../../lib/vue.esm-browser.prod.js';

export const ComposerForkDraftCard = {
  name: 'ComposerForkDraftCard',
  props: {
    forkDraft: { type: Object, required: true },
    submitting: { type: Boolean, default: false },
    error: { type: String, default: '' },
    sourceThreadName: { type: String, default: '' },
    contextUsedPercent: { type: Number, default: 0 },
    // 由父级（UnifiedChatPage）从 app.js 透传下来；用于内联选择器，避免再让用户敲路径。
    // null = 尚未拉取（loading）；数组 = 已拉取（可为空）。
    availableSharedFiles: { default: null, validator: (v) => v === null || Array.isArray(v) },
  },
  emits: ['close', 'submit', 'add-shared-file', 'remove-shared-file'],
  setup(props, { emit }) {
    const pickerOpen = ref(false);
    const pickerQuery = ref('');
    const cardRef = ref(null);

    // 卡片从关闭变到开启时 autofocus，让 Esc 关闭默认可用，不需用户先点卡片。
    watch(
      () => props.forkDraft?.active,
      async (active) => {
        if (!active) return;
        await nextTick();
        const el = cardRef.value;
        if (el && typeof el.focus === 'function') el.focus({ preventScroll: true });
      },
      { immediate: true },
    );

    // 区分 “未拉过” / “拉中” / “拉了但空库” 三种状态。availableSharedFiles 在 picker 首次打开后才拉，
    // 拉前用 null/undefined 表示 unknown，拉后是数组（可能为空）。这里根据冷启动 vs 会话原生 区分。
    const isLoadingShared = computed(() => {
      if (!pickerOpen.value) return false;
      const list = props.availableSharedFiles;
      // 父级传下来是个 ref-unwrapped 数组；undefined / 非数组 表示还在拉。
      return !Array.isArray(list);
    });

    const filteredAvailableFiles = computed(() => {
      const all = Array.isArray(props.availableSharedFiles) ? props.availableSharedFiles : [];
      const mounted = new Set(props.forkDraft?.sharedFilePaths || []);
      const q = pickerQuery.value.trim().toLowerCase();
      return all
        .map((file) => ({
          path: (file?.path || '').toString(),
          updatedBy: (file?.updated_by || file?.updatedBy || '').toString(),
        }))
        .filter((file) => file.path && !mounted.has(file.path))
        .filter((file) => !q || file.path.toLowerCase().includes(q) || file.updatedBy.toLowerCase().includes(q))
        .slice(0, 30);
    });

    function openPicker() {
      pickerQuery.value = '';
      pickerOpen.value = true;
    }
    function closePicker() {
      pickerOpen.value = false;
      pickerQuery.value = '';
    }
    function pickFile(path) {
      const value = (path || '').toString().trim();
      if (!value) return;
      emit('add-shared-file', value);
      closePicker();
    }

    function onCardKeydown(event) {
      // Esc：picker 打开时先关 picker，否则关整张卡片。
      if (event.key !== 'Escape') return;
      if (props.submitting) return;
      if (pickerOpen.value) {
        closePicker();
        event.stopPropagation();
        return;
      }
      emit('close');
      event.stopPropagation();
    }

    return {
      cardRef,
      pickerOpen,
      pickerQuery,
      filteredAvailableFiles,
      isLoadingShared,
      openPicker,
      closePicker,
      pickFile,
      onCardKeydown,
      onRemove: (path) => emit('remove-shared-file', path),
      onClose: () => emit('close'),
      onSubmit: () => emit('submit'),
    };
  },
  template: `
    <div
      v-if="forkDraft.active"
      ref="cardRef"
      class="composer-fork-draft-card"
      data-testid="composer-fork-draft-card"
      role="region"
      aria-label="新建继承对话草稿"
      tabindex="0"
      @keydown="onCardKeydown"
    >
      <div class="composer-fork-draft-head">
        <span class="composer-fork-draft-title">新建继承对话</span>
        <span v-if="sourceThreadName" class="composer-fork-draft-source" :title="sourceThreadName">
          继承自：{{ sourceThreadName }}
        </span>
        <span v-if="contextUsedPercent > 0" class="composer-fork-draft-pct">
          当前 {{ Math.round(contextUsedPercent) }}%
        </span>
        <button type="button" class="btn btn-ghost btn-xs" @click="onClose" :disabled="submitting" aria-label="关闭草稿（Esc）" title="关闭（Esc）">×</button>
      </div>

      <div class="composer-fork-draft-body">
        <div class="composer-fork-draft-row">
          <span class="composer-fork-draft-label">摘要来源：</span>
          <span class="composer-fork-draft-value">当前对话历史（截断 ≤ 2400 字）</span>
        </div>
        <div class="composer-fork-draft-row">
          <span class="composer-fork-draft-label">挂载共享文件：</span>
          <button
            type="button"
            class="btn btn-ghost btn-xs"
            data-testid="composer-fork-draft-add-shared"
            @click="pickerOpen ? closePicker() : openPicker()"
            :disabled="submitting"
          >{{ pickerOpen ? '收起' : '+ 选择文件' }}</button>
        </div>

        <div v-if="pickerOpen" class="composer-fork-draft-picker" data-testid="composer-fork-draft-picker">
          <input
            type="text"
            class="composer-fork-draft-picker-input"
            placeholder="搜索路径或维护人..."
            v-model="pickerQuery"
            :disabled="submitting"
            data-testid="composer-fork-draft-picker-input"
          />
          <div v-if="isLoadingShared" class="composer-fork-draft-picker-empty" data-testid="composer-fork-draft-picker-loading">
            加载中…
          </div>
          <ul v-else-if="filteredAvailableFiles.length > 0" class="composer-fork-draft-picker-list">
            <li
              v-for="file in filteredAvailableFiles"
              :key="file.path"
            >
              <button
                type="button"
                class="composer-fork-draft-picker-item"
                @click="pickFile(file.path)"
                :disabled="submitting"
                :title="file.path"
              >
                <span class="composer-fork-draft-picker-path">{{ file.path }}</span>
                <span v-if="file.updatedBy" class="composer-fork-draft-picker-meta">{{ file.updatedBy }}</span>
              </button>
            </li>
          </ul>
          <div v-else class="composer-fork-draft-picker-empty">
            {{ pickerQuery ? '没有匹配的共享文件' : '暂无可挂载的共享文件（去「共享文件」页创建）' }}
          </div>
        </div>

        <ul v-if="forkDraft.sharedFilePaths.length > 0" class="composer-fork-draft-files">
          <li v-for="path in forkDraft.sharedFilePaths" :key="path">
            <span class="composer-fork-draft-file-path" :title="path">{{ path }}</span>
            <button type="button" class="btn btn-ghost btn-xs" @click="onRemove(path)" :disabled="submitting" aria-label="移除挂载">×</button>
          </li>
        </ul>
        <div v-else class="composer-fork-draft-empty">未挂载共享文件（仅用对话摘要新建）</div>
      </div>

      <div v-if="error" class="composer-fork-draft-error" role="alert">{{ error }}</div>

      <div class="composer-fork-draft-actions">
        <button type="button" class="btn btn-ghost btn-xs" @click="onClose" :disabled="submitting">取消</button>
        <button
          type="button"
          class="btn btn-primary btn-xs"
          data-testid="composer-fork-draft-submit"
          @click="onSubmit"
          :disabled="submitting"
        >{{ submitting ? '创建中…' : '创建并继续' }}</button>
      </div>
    </div>
  `,
};
