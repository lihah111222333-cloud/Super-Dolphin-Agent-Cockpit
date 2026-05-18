import { computed, watch, nextTick, onMounted, onUpdated, onBeforeUnmount } from '../../lib/vue.esm-browser.prod.js';
import { logDebug } from '../services/log.js';
import { useComposerDragDrop } from '../composables/useComposerDragDrop.js';
import { useComposerInterrupt } from '../composables/useComposerInterrupt.js';
import { useComposerThreadConfig } from '../composables/useComposerThreadConfig.js';
import { useComposerTextarea } from '../composables/useComposerTextarea.js';

export { applyComposerTextareaAutoHeight } from '../utils/composer-textarea-height.js';

export const ComposerBar = {
  name: 'ComposerBar',
  props: {
    composer: { type: Object, required: true },
    disabled: { type: Boolean, default: false },
    threadId: { type: String, default: '' },
    launchSkillSelectionEnabled: { type: Boolean, default: false },
    interruptible: { type: Boolean, default: false },
    compacting: { type: Boolean, default: false },
    canCompact: { type: Boolean, default: true },
    compactResultText: { type: String, default: '' },
    compactResultTone: { type: String, default: '' },
    compactSuccessCount: { type: Number, default: 0 },
    tokenInline: { type: String, default: '' },
    tokenTooltip: { type: String, default: '' },
    // 'normal' | 'warn' | 'danger' | 'critical'：带颜色提示 tokenInline。
    tokenLevel: { type: String, default: 'normal' },
    skillMatches: { type: Array, default: () => [] },
    skillMatchesLoading: { type: Boolean, default: false },
    selectedSkillNames: { type: Array, default: () => [] },
    selectedSkillRefs: { type: Array, default: () => [] },
    isCmd: { type: Boolean, default: false },
    threadConfigProvider: { type: String, default: '' },
    threadConfigSupportsOverride: { type: Boolean, default: false },
    threadConfigDraftModel: { type: String, default: '' },
    threadConfigDraftEffort: { type: String, default: '' },
    threadConfigLoading: { type: Boolean, default: false },
    threadConfigSaving: { type: Boolean, default: false },
    threadConfigNotice: { type: String, default: '' },
    threadConfigNoticeLevel: { type: String, default: 'info' },
    threadConfigMeta: { type: Object, default: () => ({ override: {}, effective: {} }) },
    // Phase 2.2a：thread 是否已升级为自动化任务（runtime.taskId 非空）。
    // 决定下拉里渲染的是「升级」按钮还是「已是任务」状态行。
    threadIsTask: { type: Boolean, default: false },
    // Phase 2.2a：promote-task RPC in-flight，按钮 disable + 改文案。
    promotingTask: { type: Boolean, default: false },
    // Phase 2.2a：已是任务时显示当前 taskId 让用户确认是哪条任务。
    threadTaskId: { type: String, default: '' },
    // Phase 2.2a：promote-task RPC 上一次错误（usePromoteTask.lastError）。
    // 非空时按钮下方显示一行红字反馈，让用户知道为什么没升级成功。
    promoteTaskError: { type: String, default: '' },
  },
  emits: [
    'send', 'interrupt', 'compact', 'toggle-skill', 'select-all-skills', 'clear-skills',
    'update-thread-config-model', 'update-thread-config-effort', 'save-thread-config', 'restore-thread-config-inherit',
    'open-fork-draft', 'promote-task',
  ],
  setup(props, { emit }) {
    const {
      isComposing,
      syncComposerInputHeight,
      setComposerInputRef,
      onInput,
      onCompositionStart,
      onCompositionEnd,
    } = useComposerTextarea();
    const dragDrop = useComposerDragDrop(props);
    const { resetDropState, bindNativeFileDrop, unbindNativeFileDrop } = dragDrop;
    const threadConfig = useComposerThreadConfig(props, emit);
    const { onThreadConfigClickOutside } = threadConfig;
    const threadConfigInlineNotice = computed(() => {
      if (!threadConfig.threadConfigVisible.value || !threadConfig.threadConfigEditable.value) {
        return '';
      }
      const explicitNotice = (props.threadConfigNotice || '').toString().trim();
      return explicitNotice || '';
    });
    const threadConfigInlineNoticeColor = computed(() => {
      const hasExplicitNotice = Boolean((props.threadConfigNotice || '').toString().trim());
      if (!hasExplicitNotice) {
        return 'var(--text-muted)';
      }
      const level = (props.threadConfigNoticeLevel || 'info').toString().trim().toLowerCase();
      if (level === 'error') return 'var(--error, #f87171)';
      if (level === 'warning') return 'var(--warning, #fbbf24)';
      return 'var(--info, #60a5fa)';
    });
    function hasReadyInput() {
      return props.composer.canSend.value;
    }

    // Phase 2.2a · 配置面板基础入口。已是 task 或 RPC in-flight 时不发；
    // 单向升级（plan §2.2a「UI 必须只展示单向升级语义」），不提供取消 toggle。
    function onPromoteTask() {
      if (props.promotingTask || props.threadIsTask) return;
      emit('promote-task');
    }

    const showLegacySkillSelector = computed(() => {
      const hasThreadId = Boolean((props.threadId || '').toString().trim());
      if (!hasThreadId && props.launchSkillSelectionEnabled) {
        return false;
      }
      return true;
    });

    const interrupt = useComposerInterrupt(props, emit, { hasReadyInput, onSend });
    const { pauseAcknowledged, resetInterruptState } = interrupt;
    function onPaste(event) {
      logDebug('ui', 'composerBar.paste', {});
      props.composer.handlePaste(event);
    }




    function onSend(event) {
      const keyCode = Number(event?.keyCode || event?.which || 0);
      const key = (event?.key || '').toString();
      const imeLikely = event?.isComposing || isComposing.value || keyCode === 229 || key === 'Process' || key === 'Unidentified';
      if (event?.type === 'keydown' && imeLikely) {
        logDebug('ui', 'composerBar.send.blockedByComposition', {
          key_code: keyCode,
          key,
          composing: Boolean(event?.isComposing || isComposing.value),
        });
        return;
      }
      if (!hasReadyInput()) {
        logDebug('ui', 'composerBar.send.skipped.noInput', {
          trigger: event?.type || '',
        });
        return;
      }
      if (event?.type === 'keydown' && typeof event.preventDefault === 'function') {
        event.preventDefault();
      }
      pauseAcknowledged.value = false;
      logDebug('ui', 'composerBar.send.click', {
        disabled: props.disabled,
      });
      emit('send');
    }



    function onCompact() {
      if (props.disabled) return;
      if (props.compacting) return;
      if (!props.canCompact) return;
      if (!(props.threadId || '').toString().trim()) return;
      emit('compact');
    }

    function compactResultToneClass() {
      if (!props.compactResultText) return '';
      const tone = (props.compactResultTone || '').toString().trim().toLowerCase();
      if (tone === 'success') return 'is-success';
      if (tone === 'error') return 'is-error';
      return '';
    }

    function onAttach() {
      logDebug('ui', 'composerBar.attach.click', {
        disabled: props.disabled || props.composer.state.attaching,
      });
      props.composer.attachByPicker();
    }

    function onRemoveAttachment(index) {
      logDebug('ui', 'composerBar.attachment.remove', { index });
      props.composer.removeAttachment(index);
    }

    function normalizeSkillMatchType(match) {
      const type = (match?.matchedBy || '').toString().trim().toLowerCase();
      if (type === 'force') return 'force';
      if (type === 'explicit') return 'explicit';
      return 'trigger';
    }

    function skillMatchClass(match) {
      return normalizeSkillMatchType(match);
    }

    function skillMatchReason(match) {
      const type = normalizeSkillMatchType(match);
      let typeLabel = '关键词';
      if (type === 'force') typeLabel = '自动推荐';
      else if (type === 'explicit') typeLabel = '直接提到';
      const terms = Array.isArray(match?.matchedTerms)
        ? match.matchedTerms.map((item) => (item || '').toString().trim()).filter(Boolean)
        : [];
      if (terms.length === 0) return typeLabel;
      return `${typeLabel}: ${terms.join(' / ')}`;
    }

    function skillMatchKey(match, index) {
      const name = (match?.name || '').toString().trim();
      const reason = skillMatchReason(match);
      return `${name}|${reason}|${index}`;
    }

    function skillSelectionKey(rawSkill) {
      const directKey = (rawSkill?.key || '').toString().trim().toLowerCase();
      if (directKey) return directKey;
      const name = (rawSkill?.name || rawSkill || '').toString().trim().toLowerCase();
      if (!name) return '';
      const scope = (rawSkill?.scope || '').toString().trim().toLowerCase();
      const personalType = (rawSkill?.personal_type || rawSkill?.personalType || '').toString().trim().toLowerCase();
      const path = (rawSkill?.dir || rawSkill?.skill_file || rawSkill?.path || '').toString().trim().toLowerCase();
      return scope || personalType || path ? [scope, personalType, name, path].join(':') : '';
    }

    function isSkillSelected(rawSkill) {
      const refKey = skillSelectionKey(rawSkill);
      const hasSelectedRefs = Array.isArray(props.selectedSkillRefs);
      const selectedRefs = hasSelectedRefs ? props.selectedSkillRefs : [];
      if (refKey && selectedRefs.some((item) => skillSelectionKey(item) === refKey)) return true;
      if (refKey && hasSelectedRefs) return false;
      const name = (rawSkill?.name || rawSkill || '').toString().trim().toLowerCase();
      if (!name) return false;
      const selectedNames = Array.isArray(props.selectedSkillNames) ? props.selectedSkillNames : [];
      return selectedNames.some((item) => (item || '').toString().trim().toLowerCase() === name);
    }

    function onToggleSkill(rawSkill) {
      if (rawSkill && typeof rawSkill === 'object') {
        emit('toggle-skill', rawSkill);
        return;
      }
      emit('toggle-skill', (rawSkill || '').toString().trim());
    }

    function onSelectAllSkills() {
      emit('select-all-skills');
    }

    function onClearSkills() {
      emit('clear-skills');
    }

    watch(
      () => props.threadId,
      (next, prev) => {
        const nextID = (next || '').toString();
        const prevID = (prev || '').toString();
        if (nextID === prevID) return;
        resetInterruptState();
        isComposing.value = false;
        resetDropState();
        logDebug('ui', 'composerBar.thread.switch.reset', {
          from_thread_id: prevID,
          to_thread_id: nextID,
        });
      },
    );
    watch(
      () => props.composer?.state?.text,
      () => {
        nextTick(() => syncComposerInputHeight());
      },
      { immediate: true },
    );

    onUpdated(() => {
      if (pauseAcknowledged.value && hasReadyInput()) {
        pauseAcknowledged.value = false;
        logDebug('ui', 'composerBar.pauseAck.resetByInput', {});
      }
      syncComposerInputHeight();
    });

    onMounted(() => {
      bindNativeFileDrop();
      if (typeof window?.addEventListener === 'function') window.addEventListener('resize', syncComposerInputHeight);
      document.addEventListener('click', onThreadConfigClickOutside, true);
      nextTick(() => syncComposerInputHeight());
    });

    onBeforeUnmount(() => {
      unbindNativeFileDrop();
      if (typeof window?.removeEventListener === 'function') window.removeEventListener('resize', syncComposerInputHeight);
      document.removeEventListener('click', onThreadConfigClickOutside, true);
      resetInterruptState();
      resetDropState();
    });

    return {
      isComposing,
      setComposerInputRef,
      hasReadyInput,
      onPaste,
      onInput,
      onCompositionStart,
      onCompositionEnd,
      onSend,
      onCompact,
      compactResultToneClass,
      onAttach,
      onRemoveAttachment,
      showLegacySkillSelector,
      skillMatchClass,
      skillMatchReason,
      skillMatchKey,
      isSkillSelected,
      onToggleSkill,
      onSelectAllSkills,
      onClearSkills,
      threadConfigInlineNotice,
      threadConfigInlineNoticeColor,
      onPromoteTask,

      ...interrupt,
      ...dragDrop,
      ...threadConfig,
    };
  },
  template: `
    <div
      id="chat-input-bar"      class="chat-input-vue"
      data-testid="composer-bar"
      :class="{ 'drop-active': dropActive }"
      data-file-drop-target=""
      style="position:relative"
      @dragenter="onDragEnter"
      @dragover="onDragOver"
      @dragleave="onDragLeave"
      @drop="onDrop"
    >
      <div v-if="compacting" class="agent-loading-bar"></div>
      <div v-if="dropActive" class="composer-drop-hint" aria-live="polite">松开即可添加附件</div>
      <div v-if="showLegacySkillSelector" class="composer-skill-selector" :class="{ 'is-expanded': skillMatches.length > 8 }" role="status" aria-live="polite" data-testid="composer-skill-selector">
        <div class="composer-skill-selector-head">
          <span class="composer-skill-selector-title" :class="{ 'loading-shimmer': skillMatchesLoading }">
            {{ skillMatchesLoading ? '技能匹配中…' : ('技能选择 ' + selectedSkillNames.length + '/' + skillMatches.length) }}
          </span>
          <button
            class="composer-skill-selector-btn"
            type="button"
            :disabled="skillMatches.length === 0"
            @click="onSelectAllSkills"
          >全选</button>
          <button
            class="composer-skill-selector-btn"
            type="button"
            :disabled="selectedSkillNames.length === 0"
            @click="onClearSkills"
          >清空</button>
        </div>
        <div class="composer-skill-selector-list">
          <button
            v-for="(match, index) in skillMatches"
            :key="skillMatchKey(match, index)"
            class="composer-skill-selector-item"
            :class="[skillMatchClass(match), { selected: isSkillSelected(match) }]"
            type="button"
            :title="skillMatchReason(match)"
            @click="onToggleSkill(match)"
          >
            <span class="composer-skill-selector-item-name">{{ match.name }}</span>
            <span class="composer-skill-selector-item-reason">{{ skillMatchReason(match) }}</span>
          </button>
          <span v-if="!skillMatchesLoading && skillMatches.length === 0" class="composer-skill-selector-empty">输入相关内容后可点选技能</span>
        </div>
      </div>

      <div v-if="composer.state.attachments.length > 0" class="chat-attachment-list composer-attachments">
        <span v-for="(att, idx) in composer.state.attachments" :key="att.path + idx" class="chat-attachment-pill" :class="{ 'chat-attachment-pill--image': att.kind === 'image' && att.previewUrl }">
          <img v-if="att.kind === 'image' && att.previewUrl" class="chat-attachment-pill__thumb" :src="att.previewUrl" :alt="att.name" loading="lazy" />
          <span class="attachment-kind">{{ att.kind === 'image' ? 'IMG' : 'FILE' }}</span>
          <span class="attachment-name">{{ att.name }}</span>
          <button class="attachment-remove" @click="onRemoveAttachment(idx)" aria-label="移除附件">×</button>
        </span>
      </div>

      <div id="input-row" class="chat-input-row-vue" data-testid="composer-input-row">
        <button id="btnAttach" class="btn btn-secondary" data-testid="composer-attach-button" @click="onAttach" :disabled="composer.state.attaching || disabled">
          {{ composer.state.attaching ? '选择中...' : '附件' }}
        </button>
        <textarea
          id="chatInput"
          :ref="setComposerInputRef"
          data-testid="composer-input"
          rows="2"
          v-model="composer.state.text"
          placeholder="输入给 Agent 的内容，Enter 发送，Shift+Enter 换行"
          :disabled="disabled"
          @input="onInput"
          @paste="onPaste"
          @compositionstart="onCompositionStart"
          @compositionend="onCompositionEnd"
          @keydown.enter.exact="onSend"
          @keydown.esc.exact="onEscape"
        ></textarea>
        <div class="composer-top-actions" style="align-items: flex-end;">
          <div style="display: flex; flex-direction: column; gap: 4px; align-items: flex-end; margin-left: auto;">
            <div style="display: flex; gap: 4px; align-items: center;">
              <button
                type="button"
                class="composer-token-chip composer-fork-chip"
                data-testid="composer-fork-button"
                :disabled="disabled || !threadId"
                :title="!threadId ? '选中一个会话后才能继承新建' : '以当前会话为背景新建一个继承对话'"
                @click="$emit('open-fork-draft')"
                style="cursor: pointer; display: inline-flex; justify-content: center; align-items: center; border: 1px solid transparent; background: rgba(255, 255, 255, 0.04); transition: background 0.2s; box-sizing: border-box; width: 22px; height: 22px; padding: 0;"
              >
                <svg viewBox="0 0 24 24" aria-hidden="true" style="width:11px;height:11px;opacity:0.7;">
                  <path d="M6 5v6a3 3 0 0 0 3 3h9M18 14l-3-3m3 3l-3 3" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round" />
                </svg>
              </button>
            <button
              class="composer-token-chip"
              data-testid="composer-compact-button"
              :class="[{ loading: compacting }, compactResultToneClass()]"
              type="button"
              :title="!canCompact ? '当前 agent 不支持上下文压缩' : (compacting ? '正在暂停并压缩上下文，等待压缩返回结果' : (tokenTooltip ? ('压缩上下文\\n\\n' + tokenTooltip) : '压缩上下文'))"
              :aria-label="!canCompact ? '当前 agent 不支持上下文压缩' : (compacting ? '正在暂停并压缩上下文，等待压缩返回结果' : (tokenTooltip ? ('压缩上下文，' + tokenTooltip) : '压缩上下文'))"
              :disabled="disabled || !threadId || compacting || !canCompact"
              @click="onCompact"
              style="cursor: pointer; display: inline-flex; justify-content: center; align-items: center; border: 1px solid transparent; background: rgba(255, 255, 255, 0.04); transition: background 0.2s; box-sizing: border-box;"
            >
              <svg class="composer-compact-icon" viewBox="0 0 24 24" aria-hidden="true" style="width:13px;height:13px;opacity:0.6;margin-right:4px; margin-top:-1px;">
                <path
                  d="M9 5l-4 4 4 4M15 5l4 4-4 4M9 19l-4-4 4-4M15 19l4-4-4-4"
                  fill="none"
                  stroke="currentColor"
                  stroke-width="1.9"
                  stroke-linecap="round"
                  stroke-linejoin="round"
                />
              </svg>
              <span v-if="compactResultText && !compacting" style="color: inherit;">{{ compactResultText }}</span>
              <span v-else-if="compacting" class="loading-shimmer">更新中…</span>
              <span
                v-else-if="tokenInline"
                :class="['composer-token-inline', tokenLevel && tokenLevel !== 'normal' ? ('is-token-' + tokenLevel) : '']"
              >
                {{ tokenInline }}
                <span v-if="compactSuccessCount > 0" style="color: var(--success, #4ade80); margin-left: 4px; font-weight: 600;">{{ compactSuccessCount }}</span>
              </span>
              <span v-else-if="compactSuccessCount > 0" style="color: var(--success, #4ade80); font-weight: 600;">{{ compactSuccessCount }}</span>
            </button>
            </div>

            <div
              v-if="threadConfigVisible && threadConfigEditable"
              class="composer-thread-config-wrap"
              ref="threadConfigWrapRef"
              style="position: relative; display: flex;"
            >
              <button
                class="composer-token-chip composer-thread-config-btn"
                type="button"
                ref="threadConfigTriggerRef"
                :class="{ active: threadConfigOpen }"
                @click="toggleThreadConfig"
                :disabled="threadConfigLoading"
                aria-label="线程执行配置"
                :title="threadConfigLoading ? '加载中...' : '线程执行配置'"
                style="cursor: pointer; display: inline-flex; align-items: center; justify-content: center; border: 1px solid transparent; background: rgba(255, 255, 255, 0.04); transition: background 0.2s; box-sizing: border-box; height: 22px;"
              >
                <svg class="composer-compact-icon" viewBox="0 0 24 24" aria-hidden="true" style="width:13px;height:13px;opacity:0.6;margin-right:2px; margin-top:-1px;">
                  <path d="M12 8v4l3 3m6-3a9 9 0 11-18 0 9 9 0 0118 0z" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"/>
                </svg>
                <span>{{ threadConfigLoading ? '加载中...' : threadConfigSummaryLabel }}</span>
                <svg class="project-selector-chevron" viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true" style="margin-left:4px; opacity:0.5; width:10px; height:10px; margin-top:-1px;">
                  <path d="M4 6l4 4 4-4"></path>
                </svg>
              </button>
            <div
              v-if="threadConfigOpen"
              class="project-dropdown composer-thread-config-dropdown"
              :style="threadConfigDropdownStyle"
              @click.stop
            >
              <div style="padding: 10px 14px; display: flex; flex-direction: column; gap: 10px;">
                <label class="thread-config-field" style="display: flex; flex-direction: column; gap: 4px;">
                  <span style="font-size: 11px; color: var(--text-muted); font-weight: 500;">Model</span>
                  <select
                    class="settings-stall-input"
                    data-testid="thread-config-model-select"
                    style="width: 100%; height: 28px; background: var(--card); border: 1px solid var(--border); border-radius: 4px; color: var(--text); font-size: 12px; padding: 0 6px;"
                    :value="threadConfigDraftModel"
                    :disabled="threadConfigSaving"
                    @change="onModelSelectChange($event.target.value)"
                  >
                    <option value="">{{ threadConfigInheritModelLabel }}</option>
                    <option v-for="m in threadConfigModelOptions" :key="m.value" :value="m.value">{{ m.label }}</option>
                  </select>
                </label>
                <label class="thread-config-field" style="display: flex; flex-direction: column; gap: 4px;">
                  <span style="font-size: 11px; color: var(--text-muted); font-weight: 500;">Effort</span>
                  <select
                    class="settings-stall-input"
                    data-testid="thread-config-effort-select"
                    style="width: 100%; height: 28px; background: var(--card); border: 1px solid var(--border); border-radius: 4px; color: var(--text); font-size: 12px; padding: 0 6px;"
                    :value="threadConfigDraftEffort"
                    :disabled="threadConfigSaving"
                    @change="onEffortSelectChange($event.target.value)"
                  >
                    <option value="">{{ threadConfigInheritEffortLabel }}</option>
                    <option v-for="m in threadConfigEffortOptions" :key="m.value" :value="m.value">{{ m.label }}</option>
                  </select>
                </label>
              </div>
              <div
                v-if="threadIsTask || promoteTaskError"
                class="project-dropdown-divider"
                style="margin: 4px 0px;"
              ></div>
              <div
                v-if="threadIsTask || promoteTaskError"
                class="composer-thread-promote-section"
                style="padding: 8px 14px; display: flex; flex-direction: column; gap: 6px;"
              >
                <span style="font-size: 11px; color: var(--text-muted); font-weight: 500;">自动化任务</span>
                <div v-if="threadIsTask" data-testid="thread-config-promote-already" style="font-size: 11px; color: var(--text-muted); line-height: 1.45;">
                  已是自动化任务<span v-if="threadTaskId" style="opacity:0.7; margin-left:4px;">（{{ threadTaskId }}）</span>
                  <div style="opacity: 0.65; margin-top: 2px;">token 满 / 状态出错时会自动续接，不再需要手动点继续。</div>
                </div>
                <span
                  v-else-if="promoteTaskError"
                  data-testid="thread-config-promote-error"
                  :title="promoteTaskError"
                  style="font-size: 10px; color: var(--error, #f87171); line-height: 1.35;"
                >升级失败：{{ promoteTaskError }}</span>
              </div>
              <div v-if="!threadConfigInherited" style="padding: 6px 14px 10px; display: flex; justify-content: center;">
                <button
                  class="btn btn-ghost btn-xs"
                  data-testid="thread-config-restore-button"
                  :disabled="threadConfigSaving"
                  @click="restoreThreadConfig"
                  style="font-size: 11px; opacity: 0.7;"
                >
                  替换：继承全局
                </button>
              </div>
            </div>
            <button
              v-if="!isCmd && threadId && !threadIsTask"
              class="composer-token-chip composer-promote-chip"
              type="button"
              data-testid="composer-promote-chip"
              :disabled="promotingTask"
              :title="promotingTask ? '升级中…' : '把当前对话升级为自动化任务（token 满 / 出错自动续接，单向）'"
              @click="onPromoteTask"
            >
              <svg viewBox="0 0 24 24" aria-hidden="true" class="composer-promote-chip-icon">
                <path d="M12 4v16m-7-9l7-7 7 7" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"/>
              </svg>
              <span>{{ promotingTask ? '升级中…' : '转为自动任务' }}</span>
            </button>
            <div
              v-if="threadConfigInlineNotice"
              class="composer-thread-config-notice"
              :title="threadConfigInlineNotice"
              :style="{ fontSize: '11px', lineHeight: '1.35', minHeight: '16px', textAlign: 'right', color: threadConfigInlineNoticeColor }"
            >
              {{ threadConfigInlineNotice }}
            </div>
          </div>
        </div>
        </div>
        <div class="composer-action-stack">
          <button
            id="btnSend"
            class="btn btn-primary"
            data-testid="composer-send-button"
            :class="{ 'btn-stop': isPauseMode() }"
            :disabled="disabled || (isPauseMode() && interruptPending) || (!isPauseMode() && !hasReadyInput())"
            :aria-label="isPauseMode() ? '中断' : '发送'"
            @click="onPrimaryAction"
          >
            <span v-if="isPauseMode()" class="btn-stop-icon" aria-hidden="true"></span>
            <svg v-else class="btn-send-icon" viewBox="0 0 24 24" aria-hidden="true">
              <path
                d="M12 17V7M7.5 11.5L12 7l4.5 4.5"
                fill="none"
                stroke="currentColor"
                stroke-width="2.2"
                stroke-linecap="round"
                stroke-linejoin="round"
              />
            </svg>
          </button>
        </div>
      </div>
    </div>
  `,
};
