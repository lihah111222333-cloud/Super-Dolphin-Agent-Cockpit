/**
 * SectionsEditor — prompt_template_sections 的"高级调试"面板
 *
 * 作为 SystemPromptPage 编辑弹窗的"🔧 高级调试" tab 的子组件。
 * 面向 prompt 工程师，**普通用户不会看到**：需要在编辑弹窗里主动切到
 * 高级 tab 才渲染可见。
 *
 * 职责（Step 1/2/3b 的 UI 入口）：
 *   - 列出当前 prompt_template 的所有 sections（区分 static / dynamic）
 *   - 允许新增 / 编辑 / 删除 section
 *   - 暴露 enable_when JSONB 原始 JSON 编辑（初版不做语义化下拉）
 *
 * 拆成独立组件的动机是 size-guard：SystemPromptPage.js 原文件 <800 行，
 * 继续堆代码会踩硬上限；拆出来后两个文件各自清晰。
 */
// @ts-nocheck
import {
  ref,
  reactive,
  watch,
} from '../../lib/vue.esm-browser.prod.js';

import { callAPI } from '../services/api.js';
import { logWarn } from '../services/log.js';

function withCwd(cwd, payload) {
  return cwd ? { ...payload, cwd } : payload;
}

function truncate(text, max = 240) {
  if (!text) return '';
  return text.length > max ? text.slice(0, max) + '…' : text;
}

function toErrorMessage(error) {
  return (
    (error && typeof error === 'object' && typeof error.message === 'string' ? error.message : '')
    || String(error || '')
  ).toString().trim();
}

export const SectionsEditor = {
  name: 'SectionsEditor',
  props: {
    promptId: { type: String, default: '' },
    cwd: { type: String, default: '' },
    fallbackMode: { type: Boolean, default: false },
    // Parent 控制本组件是否"可见"：切回基础 tab 时 parent 会设 false，
    // 本组件在 watch 里负责关掉任何打开的子模态避免"隐形弹窗"。
    visible: { type: Boolean, default: false },
  },
  setup(props) {
    const sectionsLoading = ref(false);
    const sectionsList = ref([]);
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
    });

    function setNotice(level, message) {
      notice.level = level || 'info';
      notice.message = (message || '').toString().trim();
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
      sectionForm.originalKey = '';
      sectionForm.sectionKey = '';
      sectionForm.region = 'dynamic';
      sectionForm.ordinal = 0;
      sectionForm.body = '';
      sectionForm.enableWhen = '';
      sectionForm.enabled = true;
    }

    function openCreate() {
      if (!props.promptId) {
        setNotice('error', '请先保存提示词再添加分段');
        return;
      }
      resetSectionForm();
      sectionEditorMode.value = 'create';
      sectionEditorOpen.value = true;
    }

    function openEdit(item) {
      sectionForm.originalKey = item?.section_key || '';
      sectionForm.sectionKey = item?.section_key || '';
      sectionForm.region = item?.region === 'static' ? 'static' : 'dynamic';
      sectionForm.ordinal = Number(item?.ordinal || 0);
      sectionForm.body = item?.body || '';
      const ew = item?.enable_when;
      if (ew == null) {
        sectionForm.enableWhen = '';
      } else if (typeof ew === 'string') {
        sectionForm.enableWhen = ew;
      } else {
        try { sectionForm.enableWhen = JSON.stringify(ew); } catch { sectionForm.enableWhen = ''; }
      }
      sectionForm.enabled = item?.enabled !== false;
      sectionEditorMode.value = 'edit';
      sectionEditorOpen.value = true;
    }

    function closeSectionEditor() {
      sectionEditorOpen.value = false;
    }

    async function saveSection() {
      if (sectionSaving.value) return;
      const sk = (sectionForm.sectionKey || '').trim();
      if (!sk) {
        setNotice('error', '请填写段名（section_key）');
        return;
      }
      if (sectionForm.region !== 'static' && sectionForm.region !== 'dynamic') {
        setNotice('error', 'region 必须是 static 或 dynamic');
        return;
      }
      let parsedEnableWhen;
      const ewText = (sectionForm.enableWhen || '').trim();
      if (ewText && ewText !== 'null') {
        try {
          parsedEnableWhen = JSON.parse(ewText);
        } catch (e) {
          setNotice('error', `enable_when 不是合法 JSON：${toErrorMessage(e)}`);
          return;
        }
      }
      sectionSaving.value = true;
      try {
        if (sectionEditorMode.value === 'edit'
          && sectionForm.originalKey
          && sectionForm.originalKey !== sk) {
          await callAPI('prompt-sections/delete', withCwd(props.cwd, {
            prompt_id: props.promptId,
            section_key: sectionForm.originalKey,
          }));
        }
        const payload = {
          prompt_id: props.promptId,
          section_key: sk,
          region: sectionForm.region,
          ordinal: Number(sectionForm.ordinal) || 0,
          body: sectionForm.body || '',
          enabled: !!sectionForm.enabled,
        };
        if (parsedEnableWhen !== undefined) {
          payload.enable_when = parsedEnableWhen;
        }
        await callAPI('prompt-sections/write', withCwd(props.cwd, payload));
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
      const sk = item?.section_key;
      if (!sk || sectionDeletingKey.value) return;
      sectionDeletingKey.value = sk;
      try {
        await callAPI('prompt-sections/delete', withCwd(props.cwd, {
          prompt_id: props.promptId,
          section_key: sk,
        }));
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
        if (nextVisible && nextId) {
          loadSections();
        }
        if (!nextVisible) {
          sectionEditorOpen.value = false;
        }
      },
      { immediate: true },
    );

    return {
      sectionsLoading, sectionsList, sectionEditorOpen, sectionEditorMode,
      sectionSaving, sectionDeletingKey, sectionForm, notice,
      loadSections, openCreate, openEdit, closeSectionEditor,
      saveSection, deleteSection, truncate,
    };
  },
  template: `
    <div class="sp-sections-panel" data-testid="sp-editor-advanced">
      <div class="sp-sections-hint">
        <strong>分段调试（Sections）</strong>
        <p>
          此面板供 prompt 工程师使用。分段按 <code>region</code> 注入：
          <code>static</code> 进可缓存前段（<code>--system-prompt</code>），
          <code>dynamic</code> 进实时段（<code>--append-system-prompt</code>）。
          每段可带 <code>enable_when</code> 条件（JSONB），仅当满足时才注入；
          留空即永远注入。
        </p>
        <p v-if="!promptId" class="sp-sections-warn">
          新建的提示词需要先保存（点「基础」tab 下的「保存」按钮）后，才能添加分段。
        </p>
      </div>

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
        <p>先完成基础信息并保存后，再来添加分段。</p>
      </div>
      <div v-else-if="sectionsList.length === 0" class="empty-state" data-testid="sp-sections-empty">
        <h3>尚未添加分段</h3>
        <p>未添加分段时，上面的「提示词内容」会作为单一动态段注入，行为等同旧版。</p>
      </div>
      <div v-else class="sp-sections-list" data-testid="sp-sections-list">
        <article
          v-for="(item, idx) in sectionsList"
          :key="item.section_key"
          class="data-card-vue sp-section-card"
          :class="{ 'is-disabled': item.enabled === false }"
          :data-testid="'sp-section-card-' + idx"
        >
          <div class="sp-section-head">
            <span class="sp-section-region"
              :class="'is-' + (item.region === 'static' ? 'static' : 'dynamic')"
              :title="item.region === 'static' ? '固定段 / 进可缓存前段' : '条件段 / 进实时段'">{{ item.region === 'static' ? '🔒 固定段' : '🎛 条件段' }}</span>
            <span class="sp-section-key">{{ item.section_key }}</span>
            <span class="sp-section-ord">#{{ item.ordinal }}</span>
            <span v-if="item.enabled === false" class="sp-section-badge is-disabled">已停用</span>
          </div>
          <div v-if="item.enable_when" class="sp-section-when" :title="'enable_when JSONB：满足所有键值时才注入'">
            条件：<code>{{ JSON.stringify(item.enable_when) }}</code>
          </div>
          <div v-else class="sp-section-when is-unconditional">条件：无（永远注入）</div>
          <pre class="sp-section-body">{{ truncate(item.body, 240) }}</pre>
          <div class="sp-section-actions">
            <button class="btn btn-secondary btn-xs"
              :data-testid="'sp-section-edit-btn-' + idx"
              @click="openEdit(item)">编辑</button>
            <button class="btn btn-ghost btn-xs btn-warning"
              :data-testid="'sp-section-delete-btn-' + idx"
              :disabled="sectionDeletingKey === item.section_key"
              @click="deleteSection(item)">{{ sectionDeletingKey === item.section_key ? '删除中...' : '删除' }}</button>
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
              <label>段类型（region）</label>
              <select class="modal-input" v-model="sectionForm.region" :disabled="sectionSaving" data-testid="sp-section-region-select">
                <option value="static">🔒 固定段 · 每次都注入（进缓存前段）</option>
                <option value="dynamic">🎛 条件段 · 满足条件时才注入</option>
              </select>
            </div>
            <div class="sp-field">
              <label>段名（section_key）</label>
              <input class="modal-input" v-model="sectionForm.sectionKey" placeholder="例如 identity / tool_prefs / worktree_reminder" :disabled="sectionSaving" data-testid="sp-section-key-input" />
              <div class="sp-field-meta">同一 prompt 内唯一；改名会先删旧行再新写一行。</div>
            </div>
            <div class="sp-field">
              <label>顺序（ordinal，数字越小越靠前）</label>
              <input type="number" class="modal-input" v-model.number="sectionForm.ordinal" :disabled="sectionSaving" data-testid="sp-section-ordinal-input" />
            </div>
            <div class="sp-field">
              <label>生效条件（enable_when，原始 JSON）</label>
              <textarea
                class="sp-textarea"
                rows="3"
                v-model="sectionForm.enableWhen"
                placeholder='留空=永远注入；示例：{"language":"zh","isWorktree":true}'
                :disabled="sectionSaving"
                data-testid="sp-section-enable-when-input"
              ></textarea>
              <div class="sp-field-meta">支持键：<code>language</code> / <code>provider</code> / <code>model</code> / <code>isWorktree</code> / <code>cwd</code> / <code>gitRoot</code> / <code>sessionFlags.&lt;name&gt;</code>。所有键 AND 匹配。</div>
            </div>
            <div class="sp-field">
              <label>内容（body）</label>
              <textarea
                class="sp-textarea"
                rows="8"
                v-model="sectionForm.body"
                placeholder="本段要注入的文本内容..."
                :disabled="sectionSaving"
                data-testid="sp-section-body-input"
              ></textarea>
            </div>
            <div class="sp-field">
              <label class="sp-toggle-inline">
                <input type="checkbox" v-model="sectionForm.enabled" :disabled="sectionSaving" data-testid="sp-section-enabled-checkbox" />
                <span>启用（enabled=false 时本段不会被加载）</span>
              </label>
            </div>
            <div class="sp-editor-actions">
              <button class="btn btn-ghost" @click="closeSectionEditor">取消</button>
              <button class="btn btn-primary"
                :disabled="sectionSaving"
                data-testid="sp-section-save-btn"
                @click="saveSection">{{ sectionSaving ? '保存中...' : '保存分段' }}</button>
            </div>
          </div>
        </div>
      </div>
    </div>
  `,
};
