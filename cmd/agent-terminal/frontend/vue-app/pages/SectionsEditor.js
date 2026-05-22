/**
 * SectionsEditor — prompt_template_sections editor.
 */
// @ts-nocheck
import {
  ref,
  reactive,
  watch,
} from '../../lib/vue.esm-browser.prod.js';

import { callAPI } from '../services/api.js';
import { logWarn } from '../services/log.js';
import {
  normalizeTriggerType,
  sectionDisplayName,
  sectionSummary,
  validRecallTopicName,
} from './SystemPromptPage.helpers.js';

function withCwd(cwd, payload) {
  return cwd ? { ...payload, cwd } : payload;
}

function toErrorMessage(error) {
  return (
    (error && typeof error === 'object' && typeof error.message === 'string' ? error.message : '')
    || String(error || '')
  ).toString().trim();
}

function sectionCardKey(item, idx) {
  return (item?.section_key || `section-${idx}`).toString();
}

function parseEnableWhen(raw) {
  const text = (raw || '').toString().trim();
  if (!text || text === 'null') return { value: undefined, error: '' };
  try {
    return { value: JSON.parse(text), error: '' };
  } catch (error) {
    return { value: undefined, error: `enable_when 不是合法 JSON：${toErrorMessage(error)}` };
  }
}

export const SectionsEditor = {
  name: 'SectionsEditor',
  props: {
    promptId: { type: String, default: '' },
    cwd: { type: String, default: '' },
    promptScope: { type: String, default: 'project' },
    fallbackMode: { type: Boolean, default: false },
    visible: { type: Boolean, default: false },
  },
  setup(props) {
    const sectionsLoading = ref(false);
    const sectionsList = ref([]);
    const expandedKeys = ref(new Set());
    const sectionEditorOpen = ref(false);
    const sectionEditorMode = ref('create');
    const sectionSaving = ref(false);
    const sectionDeletingKey = ref('');
    const notice = reactive({ level: 'info', message: '' });
    const sectionForm = reactive({
      originalKey: '',
      sectionKey: '',
      region: 'dynamic',
      ordinal: 0,
      body: '',
      enableWhen: '',
      enabled: true,
      triggerType: 'always',
      recallTopic: '',
    });

    function setNotice(level, message) {
      notice.level = level || 'info';
      notice.message = (message || '').toString().trim();
    }

    function withPromptScope(payload) {
      return {
        ...payload,
        scope: props.promptScope === 'global' ? 'global' : 'project',
      };
    }

    async function loadSections() {
      if (!props.promptId) {
        sectionsList.value = [];
        return;
      }
      sectionsLoading.value = true;
      try {
        const res = await callAPI('prompt-sections/list', withCwd(props.cwd, { prompt_id: props.promptId }));
        sectionsList.value = Array.isArray(res?.sections) ? res.sections : [];
        setNotice('info', '');
      } catch (error) {
        sectionsList.value = [];
        logWarn('sections-editor', 'load.failed', { error });
        setNotice('error', `加载分段失败：${toErrorMessage(error)}`);
      } finally {
        sectionsLoading.value = false;
      }
    }

    function resetSectionForm() {
      Object.assign(sectionForm, {
        originalKey: '',
        sectionKey: '',
        region: 'dynamic',
        ordinal: 0,
        body: '',
        enableWhen: '',
        enabled: true,
        triggerType: 'always',
        recallTopic: '',
      });
    }

    function openCreate() {
      if (props.fallbackMode) {
        setNotice('info', '当前为只读模式，暂不支持修改分段');
        return;
      }
      if (!props.promptId) {
        setNotice('error', '请先保存提示词再添加分段');
        return;
      }
      resetSectionForm();
      sectionEditorMode.value = 'create';
      sectionEditorOpen.value = true;
    }

    function openEdit(item) {
      if (props.fallbackMode) {
        setNotice('info', '当前为只读模式，暂不支持修改分段');
        return;
      }
      let enableWhen = '';
      const ew = item?.enable_when;
      if (typeof ew === 'string') {
        enableWhen = ew;
      } else if (ew != null) {
        try { enableWhen = JSON.stringify(ew); } catch { enableWhen = ''; }
      }
      Object.assign(sectionForm, {
        originalKey: item?.section_key || '',
        sectionKey: item?.section_key || '',
        region: item?.region === 'static' ? 'static' : 'dynamic',
        ordinal: Number(item?.ordinal || 0),
        body: item?.body || '',
        enableWhen,
        enabled: item?.enabled !== false,
        triggerType: normalizeTriggerType(item?.trigger_type),
        recallTopic: item?.recall_topic || '',
      });
      sectionEditorMode.value = 'edit';
      sectionEditorOpen.value = true;
    }

    function closeSectionEditor() {
      sectionEditorOpen.value = false;
    }

    function isSectionExpanded(item, idx) {
      return expandedKeys.value.has(sectionCardKey(item, idx));
    }

    function toggleSection(item, idx) {
      const key = sectionCardKey(item, idx);
      const next = new Set(expandedKeys.value);
      if (next.has(key)) next.delete(key);
      else next.add(key);
      expandedKeys.value = next;
    }

    async function saveSection() {
      if (props.fallbackMode) {
        setNotice('info', '当前为只读模式，暂不支持修改分段');
        return;
      }
      if (sectionSaving.value) return;
      const sk = (sectionForm.sectionKey || '').trim();
      if (!sk) { setNotice('error', '请填写段名（section_key）'); return; }
      if (sectionForm.region !== 'static' && sectionForm.region !== 'dynamic') {
        setNotice('error', 'region 必须是 static 或 dynamic');
        return;
      }
      const triggerType = normalizeTriggerType(sectionForm.triggerType);
      const recallTopic = (sectionForm.recallTopic || '').trim();
      if (triggerType === 'recall' && !validRecallTopicName(recallTopic)) {
        setNotice('error', 'recall_topic 必须是小写短横线命名，长度小于 64');
        return;
      }
      const parsedEnableWhen = parseEnableWhen(sectionForm.enableWhen);
      if (parsedEnableWhen.error) { setNotice('error', parsedEnableWhen.error); return; }
      sectionSaving.value = true;
      try {
        if (sectionEditorMode.value === 'edit' && sectionForm.originalKey && sectionForm.originalKey !== sk) {
          await callAPI('prompt-sections/delete', withCwd(props.cwd, withPromptScope({
            prompt_id: props.promptId,
            section_key: sectionForm.originalKey,
          })));
        }
        const payload = {
          prompt_id: props.promptId,
          section_key: sk,
          region: sectionForm.region,
          ordinal: Number(sectionForm.ordinal) || 0,
          body: sectionForm.body || '',
          enabled: !!sectionForm.enabled,
          trigger_type: triggerType,
          recall_topic: triggerType === 'recall' ? recallTopic : '',
        };
        if (parsedEnableWhen.value !== undefined) payload.enable_when = parsedEnableWhen.value;
        await callAPI('prompt-sections/write', withCwd(props.cwd, withPromptScope(payload)));
        sectionEditorOpen.value = false;
        await loadSections();
        setNotice('info', `分段已保存：${sk}`);
      } catch (error) {
        logWarn('sections-editor', 'save.failed', { error });
        setNotice('error', `保存分段失败：${toErrorMessage(error)}`);
      } finally {
        sectionSaving.value = false;
      }
    }

    async function deleteSection(item) {
      if (props.fallbackMode) {
        setNotice('info', '当前为只读模式，暂不支持修改分段');
        return;
      }
      const sk = item?.section_key;
      if (!sk || sectionDeletingKey.value) return;
      sectionDeletingKey.value = sk;
      try {
        await callAPI('prompt-sections/delete', withCwd(props.cwd, withPromptScope({
          prompt_id: props.promptId,
          section_key: sk,
        })));
        await loadSections();
        setNotice('info', `分段已删除：${sk}`);
      } catch (error) {
        logWarn('sections-editor', 'delete.failed', { error });
        setNotice('error', `删除分段失败：${toErrorMessage(error)}`);
      } finally {
        sectionDeletingKey.value = '';
      }
    }

    watch(
      () => [props.visible, props.promptId],
      ([nextVisible, nextId]) => {
        if (nextVisible && nextId) loadSections();
        if (!nextVisible) sectionEditorOpen.value = false;
      },
      { immediate: true },
    );

    return {
      sectionsLoading, sectionsList, expandedKeys, sectionEditorOpen, sectionEditorMode,
      sectionSaving, sectionDeletingKey, sectionForm, notice,
      loadSections, openCreate, openEdit, closeSectionEditor,
      saveSection, deleteSection, sectionCardKey, isSectionExpanded, toggleSection,
      sectionDisplayName, sectionSummary, normalizeTriggerType,
    };
  },
  template: `
    <div class="sp-sections-panel" data-testid="sp-sections-panel">
      <div class="sp-sections-toolbar" data-testid="sp-sections-toolbar">
        <button class="btn btn-secondary btn-xs"
          data-testid="sp-section-create-btn"
          :disabled="!promptId || sectionsLoading || fallbackMode"
          @click="openCreate">+ 新增分段</button>
        <button class="btn btn-ghost btn-xs"
          data-testid="sp-sections-refresh-btn"
          :disabled="!promptId || sectionsLoading"
          @click="loadSections">{{ sectionsLoading ? '加载中...' : '刷新' }}</button>
      </div>

      <div v-if="sectionsLoading" class="sp-loading" data-testid="sp-sections-loading">
        <div class="sp-spinner"></div><span>加载中...</span>
      </div>
      <div v-else-if="!promptId" class="empty-state" data-testid="sp-sections-unsaved">
        <h3>请先保存提示词</h3>
        <p>保存后即可添加分段。</p>
      </div>
      <div v-else-if="sectionsList.length === 0" class="empty-state" data-testid="sp-sections-empty">
        <h3>尚未添加分段</h3>
        <p>点击“新增分段”开始维护注入内容。</p>
      </div>
      <div v-else class="sp-sections-list" data-testid="sp-sections-list">
        <article
          v-for="(item, idx) in sectionsList"
          :key="sectionCardKey(item, idx)"
          class="data-card-vue sp-section-card"
          :class="{ 'is-disabled': item.enabled === false, 'is-open': isSectionExpanded(item, idx), 'is-recall': normalizeTriggerType(item.trigger_type) === 'recall' }"
          :data-testid="'sp-section-card-' + idx"
        >
          <button
            type="button"
            class="sp-section-toggle"
            :data-testid="'sp-section-toggle-' + idx"
            @click="toggleSection(item, idx)"
          >
            <span class="sp-section-caret">{{ isSectionExpanded(item, idx) ? '▾' : '▸' }}</span>
            <span class="sp-section-title-block">
              <span class="sp-section-friendly-name">{{ sectionDisplayName(item) }}</span>
              <span class="sp-section-key">{{ item.section_key }}</span>
            </span>
            <span class="sp-section-summary">{{ sectionSummary(item.body, 150) }}</span>
            <span v-if="normalizeTriggerType(item.trigger_type) === 'recall'" class="sp-section-badge is-recall">🔗 Recall</span>
            <span v-if="item.enabled === false" class="sp-section-badge is-disabled">已停用</span>
          </button>

          <div v-show="isSectionExpanded(item, idx)" class="sp-section-expanded">
            <textarea
              class="sp-textarea sp-textarea-readonly sp-section-body-textarea"
              rows="5"
              :value="item.body || ''"
              readonly
            ></textarea>
            <details class="sp-section-advanced">
              <summary>高级字段</summary>
              <dl class="sp-section-fields">
                <div><dt>region</dt><dd>{{ item.region || 'dynamic' }}</dd></div>
                <div><dt>ordinal</dt><dd>{{ item.ordinal || 0 }}</dd></div>
                <div><dt>trigger_type</dt><dd>{{ normalizeTriggerType(item.trigger_type) }}</dd></div>
                <div v-if="item.recall_topic"><dt>recall_topic</dt><dd>{{ item.recall_topic }}</dd></div>
                <div v-if="item.enable_when"><dt>enable_when</dt><dd><code>{{ JSON.stringify(item.enable_when) }}</code></dd></div>
              </dl>
            </details>
            <div class="sp-section-actions">
              <button class="btn btn-secondary btn-xs"
                :data-testid="'sp-section-edit-btn-' + idx"
                :disabled="fallbackMode"
                @click="openEdit(item)">编辑</button>
              <button class="btn btn-ghost btn-xs btn-warning"
                :data-testid="'sp-section-delete-btn-' + idx"
                :disabled="fallbackMode || sectionDeletingKey === item.section_key"
                @click="deleteSection(item)">{{ sectionDeletingKey === item.section_key ? '删除中...' : '删除' }}</button>
            </div>
          </div>
        </article>
      </div>

      <div v-if="notice.message" class="sp-notice" :class="'is-' + notice.level" data-testid="sp-sections-notice">
        {{ notice.message }}
      </div>

      <div
        v-if="sectionEditorOpen"
        class="modal-overlay sp-section-editor-overlay"
        data-testid="sp-section-editor-overlay"
        @click.self="closeSectionEditor"
        @keydown.esc.prevent="closeSectionEditor"
      >
        <div class="modal-box sp-section-editor-modal" role="dialog" aria-modal="true">
          <div class="sp-editor-head">
            <div class="modal-title">{{ sectionEditorMode === 'create' ? '新增分段' : '编辑分段' }}</div>
            <button class="btn btn-ghost" @click="closeSectionEditor">关闭</button>
          </div>
          <div class="sp-editor-body">
            <div class="sp-field">
              <label>段名（section_key）</label>
              <input class="modal-input" v-model="sectionForm.sectionKey" placeholder="identity / tool_prefs / sqlc-workflow" :disabled="sectionSaving || fallbackMode" data-testid="sp-section-key-input" />
            </div>
            <div class="sp-field">
              <label>内容（body）</label>
              <textarea
                class="sp-textarea"
                rows="8"
                v-model="sectionForm.body"
                placeholder="本段要注入的文本内容..."
                :disabled="sectionSaving || fallbackMode"
                data-testid="sp-section-body-input"
              ></textarea>
            </div>
            <details class="sp-section-advanced" :open="sectionForm.triggerType === 'recall' || !!sectionForm.enableWhen">
              <summary>高级字段</summary>
              <div class="sp-section-advanced-form">
                <div class="sp-field">
                  <label>region</label>
                  <select class="modal-input" v-model="sectionForm.region" :disabled="sectionSaving || fallbackMode" data-testid="sp-section-region-select">
                    <option value="static">static</option>
                    <option value="dynamic">dynamic</option>
                  </select>
                </div>
                <div class="sp-field">
                  <label>ordinal</label>
                  <input type="number" class="modal-input" v-model.number="sectionForm.ordinal" :disabled="sectionSaving || fallbackMode" data-testid="sp-section-ordinal-input" />
                </div>
                <div class="sp-field">
                  <label>trigger_type</label>
                  <select class="modal-input" v-model="sectionForm.triggerType" :disabled="sectionSaving || fallbackMode" data-testid="sp-section-trigger-select">
                    <option value="always">always</option>
                    <option value="keyword">keyword</option>
                    <option value="recall">recall</option>
                  </select>
                </div>
                <div v-if="sectionForm.triggerType === 'recall'" class="sp-field">
                  <label>recall_topic</label>
                  <input class="modal-input" v-model="sectionForm.recallTopic" placeholder="sqlc-workflow" :disabled="sectionSaving || fallbackMode" data-testid="sp-section-recall-topic-input" />
                  <div class="sp-field-meta">lowercase-dash, length &lt; 64</div>
                </div>
                <div class="sp-field">
                  <label>enable_when</label>
                  <textarea
                    class="sp-textarea"
                    rows="3"
                    v-model="sectionForm.enableWhen"
                    placeholder='{"language":"zh","isWorktree":true}'
                    :disabled="sectionSaving || fallbackMode"
                    data-testid="sp-section-enable-when-input"
                  ></textarea>
                </div>
                <div class="sp-field">
                  <label class="sp-toggle-inline">
                    <input type="checkbox" v-model="sectionForm.enabled" :disabled="sectionSaving || fallbackMode" data-testid="sp-section-enabled-checkbox" />
                    <span>enabled</span>
                  </label>
                </div>
              </div>
            </details>
            <div class="sp-editor-actions">
              <button class="btn btn-ghost" @click="closeSectionEditor">取消</button>
              <button class="btn btn-primary"
                :disabled="sectionSaving || fallbackMode"
                data-testid="sp-section-save-btn"
                @click="saveSection">{{ sectionSaving ? '保存中...' : '保存分段' }}</button>
            </div>
          </div>
        </div>
      </div>
    </div>
  `,
};
