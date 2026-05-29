import { computed, onMounted, ref, watch } from '../../lib/vue.esm-browser.prod.js';
import { useSkillEditor } from '../composables/useSkillEditor.js';
import { useSkillFileNavigation } from '../composables/useSkillFileNavigation.js';
import {
  importSummaryDraftPanelHint,
  importSummaryDraftPanelTitle,
} from '../composables/useSkillImportSummaryDrafts.js';
import { useSkillResolutions } from '../composables/useSkillResolutions.js';
import { useSkillSaveFeedback } from '../composables/useSkillSaveFeedback.js';
import { isInternalSkillReferenceWord, isSkillMainFilePath, normalizePathKey } from '../utils/skill-parser.js';

/** @typedef {{ name?: string, display_name?: string, displayName?: string, dir?: string, description?: string, summary?: string, trigger_words?: string[], force_words?: string[] }} SkillListItem */
/** @typedef {{ name: string, displayName: string, displayLabel: string, dir: string, description: string, summary: string, triggerWords: string[], forceWords: string[], displayScenarioWords: string[] }} SkillCard */
/** @typedef {{ skills?: SkillListItem[], cwd?: string, projectStore?: { state?: { active?: string } } | null }} SkillsPageProps */

export { normalizeWordList, listToText, inferSkillNameFromPath,
  summarizeItems, normalizePathKey, fileNameFromPath,
  skillDirFromFilePath, isSkillMainFilePath, parseFrontmatter,
  parseWordsValue, cleanScalar, parseSkillMarkdown,
  quoteYAML, buildSkillMarkdown, isInternalSkillReferenceWord,
} from '../utils/skill-parser.js';

export const SkillsPage = {
  name: 'SkillsPage',
  props: {
    skills: { type: Array, default: () => [] },
    cwd: { type: String, default: '' },
    projectStore: { type: Object, default: null },
  },
  emits: ['refresh-skills'],
  setup(props, { emit }) {
    const searchQuery = ref('');
    const scopeFilter = ref('all');

    const scopeForTrust = (trust) => {
      const value = (trust || '').toString().trim().toLowerCase();
      if (value === 'project') return 'project';
      if (value === 'user' || value === 'signed' || value === 'system' || value === 'personal') return 'personal';
      return 'project';
    };

    const scopeForItem = (item) => {
      const scope = (item?.scope || '').toString().trim().toLowerCase();
      if (scope === 'project' || scope === 'personal') return scope;
      return scopeForTrust(item?.trust);
    };

    const mergeSkillWords = (skillName, ...groups) => {
      const seen = new Set();
      const words = [];
      groups.flat().forEach((word) => {
        const text = (word || '').toString().trim();
        const key = text.toLowerCase();
        if (!text || isInternalSkillReferenceWord(text, skillName) || seen.has(key)) return;
        seen.add(key);
        words.push(text);
      });
      return words;
    };

    const skillCards = computed(() => {
      const list = Array.isArray(props.skills) ? props.skills : [];
      return list.map((item) => {
        const name = (item?.name || '').toString();
        const displayName = (item?.display_name || item?.displayName || item?.title || '').toString().trim();
        const triggerWords = Array.isArray(item?.trigger_words) ? item.trigger_words : [];
        const forceWords = Array.isArray(item?.force_words) ? item.force_words : [];
        return {
          name,
          displayName,
          displayLabel: displayName || name,
          dir: (item?.dir || '').toString(),
          description: (item?.description || '').toString(),
          summary: (item?.summary || item?.description || '').toString(),
          trust: (item?.trust || '').toString(),
          scope: scopeForItem(item),
          personal_type: (item?.personal_type || item?.personalType || '').toString(),
          triggerWords,
          forceWords,
          displayScenarioWords: mergeSkillWords(name, triggerWords, forceWords),
        };
      });
    });

    const scopeCounts = computed(() => {
      const counts = { all: 0, project: 0, personal: 0 };
      skillCards.value.forEach((item) => {
        counts.all += 1;
        if (item.scope === 'personal') counts.personal += 1;
        else counts.project += 1;
      });
      return counts;
    });

    const scopedSkillCards = computed(() => {
      const scope = (scopeFilter.value || 'all').toString().toLowerCase();
      if (scope === 'all') return skillCards.value;
      return skillCards.value.filter((item) => item.scope === scope);
    });

    const filteredSkillCards = computed(() => {
      const baseList = scopedSkillCards.value;
      const keyword = (searchQuery.value || '').toString().trim().toLowerCase();
      if (!keyword) return baseList;
      return baseList.filter((item) => {
        const haystack = [
          item.name,
          item.displayName,
          item.displayLabel,
          item.description,
          item.summary,
          item.dir,
          ...(Array.isArray(item.displayScenarioWords) ? item.displayScenarioWords : []),
        ]
          .join(' ')
          .toLowerCase();
        return haystack.includes(keyword);
      });
    });
    const showSkillCount = computed(() => skillCards.value.length > 0);
    const skillCountText = computed(() => {
      const total = skillCards.value.length;
      const shown = filteredSkillCards.value.length;
      if (total === 0) return '';
      if (scopeFilter.value === 'all' && !(searchQuery.value || '').toString().trim()) return `共 ${total} 个技能`;
      if (shown === 0) return `当前没有匹配技能，共 ${total} 个`;
      return `显示 ${shown} 个，共 ${total} 个技能`;
    });

    function skillCardKey(item) {
      const scope = (item?.scope || 'project').toString().trim().toLowerCase();
      const personalType = (item?.personal_type || item?.personalType || '').toString().trim().toLowerCase();
      const name = (item?.name || '').toString().trim().toLowerCase();
      const dir = (item?.dir || '').toString().trim().toLowerCase();
      return [scope, personalType, name, dir].join(':');
    }

    function scopeLabel(scope) {
      return (scope || '').toString().trim().toLowerCase() === 'personal' ? '私人使用' : '项目共享';
    }

    const sourcePath = ref('');
    const activeSkillFilePath = ref('');
    const activeCwdSource = computed(() => {
      const explicit = (props.cwd || '').toString().trim();
      if (explicit && explicit !== '.') return explicit;
      const active = (props.projectStore?.state?.active || '').toString().trim();
      return active && active !== '.' ? active : '';
    });
    const isEditingMainSkillFile = computed(() => {
      const candidate = (activeSkillFilePath.value || sourcePath.value || '').toString().trim();
      if (!candidate) return true;
      return isSkillMainFilePath(candidate);
    });
    const editor = useSkillEditor(props, emit, {
      skillCards,
      isEditingMainSkillFile,
      sourcePath,
      activeSkillFilePath,
    });
    const saveButtonLabel = computed(() => {
      if (editor.saving.value) return '保存中...';
      return isEditingMainSkillFile.value ? '保存技能' : '保存文件';
    });
    const fileNavigation = useSkillFileNavigation({
      activeSkillFilePath,
      activeCwdSource,
      form: editor.form,
      onEditSkill: editor.onEditSkill,
      readSkillFile: editor.readSkillFile,
      selectedSkillName: editor.selectedSkillName,
      setNotice: editor.setNotice,
      skillCards,
      skillFiles: editor.skillFiles,
      sourcePath,
    });
    const resolutions = useSkillResolutions({
      activeCwdSource,
      emit,
      setNotice: editor.setNotice,
      onNoConflicts: editor.clearImportConflictDrafts,
    });
    onMounted(() => resolutions.refreshSkillResolutions({ notify: false, notifyOnError: true, collapseOnConflict: false }));
    watch(activeCwdSource, (next, prev) => {
      if (next === prev) return;
      resolutions.resetSkillResolutions();
      resolutions.refreshSkillResolutions({ notify: false, notifyOnError: true, collapseOnConflict: false });
    }, { flush: 'sync' });
    const saveFeedback = useSkillSaveFeedback({ editor, sourcePath, activeSkillFilePath });
    const visibleImportSummaryDrafts = computed(() => editor.importSummaryDrafts.value.filter((draft) => draft?.status !== 'conflict'));
    const importSummaryPanelTitle = computed(() => importSummaryDraftPanelTitle(visibleImportSummaryDrafts.value));
    const importSummaryPanelHint = computed(() => importSummaryDraftPanelHint(visibleImportSummaryDrafts.value));

    async function onSaveSkill() {
      await saveFeedback.onSaveSkill();
      if (editor.notice.level === 'error') return;
      await resolutions.refreshSkillResolutions({ notify: false, notifyOnError: true });
    }

    async function confirmImportScope(scope) {
      const imported = await editor.confirmImportScope(scope);
      if (!imported) return;
      await resolutions.refreshSkillResolutions({ notify: false, notifyOnError: true });
    }

    function isSkillCardActive(item) {
      if (!editor.isEditorOpen.value) return false;
      const selected = (editor.selectedSkillName.value || '').toString().trim().toLowerCase();
      const name = (item?.name || '').toString().trim().toLowerCase();
      if (!selected || selected !== name) return false;
      const sourceKey = normalizePathKey(sourcePath.value || activeSkillFilePath.value || '');
      const dirKey = normalizePathKey(item?.dir || '');
      if (!sourceKey || !dirKey) return true;
      return sourceKey === `${dirKey}/skill.md` || sourceKey.startsWith(`${dirKey}/`);
    }

    return {
      searchQuery,
      scopeFilter,
      scopeCounts,
      filteredSkillCards,
      showSkillCount,
      skillCountText,
      skillCardKey,
      scopeLabel,
      isSkillCardActive,
      skillCards,
      ...resolutions,
      saveButtonLabel,
      ...editor,
      ...fileNavigation,
      ...saveFeedback,
      visibleImportSummaryDrafts,
      importSummaryPanelTitle,
      importSummaryPanelHint,
      onSaveSkill,
      confirmImportScope,
    };
  },
  template: `
    <section id="page-skills" class="page active skills-page" data-testid="skills-page">
      <div class="panel-header">
        <div class="ph-bar"></div>
        <div class="ph-text"><h2>技能管理</h2></div>
      </div>
      <div class="split-duo" data-testid="skills-split">
        <div class="split-left" data-testid="skills-left">
          <div class="section-header">技能列表</div>
          <div class="panel-body skills-list-panel" data-testid="skills-list-panel">
            <div class="skills-toolbar" data-testid="skills-toolbar">
              <button class="btn btn-secondary" data-testid="skills-import-button" :disabled="uploading || importScopePromptOpen" @click="onUploadSkill">
                {{ uploading ? '导入中...' : '批量导入技能目录' }}
              </button>
              <button class="btn btn-ghost" data-testid="skills-create-button" @click="onCreateSkill">
                新建技能
              </button>
              <div class="skills-search-wrap">
                <input
                  v-model="searchQuery"
                  class="modal-input skills-search-input"
                  data-testid="skills-search-input"
                  placeholder="搜索技能名称、简介、关键词..."
                />
              </div>
            </div>
            <div class="skills-subtoolbar" data-testid="skills-subtoolbar">
              <div class="skills-segmented skills-scope-filter" data-testid="skills-scope-filter" role="tablist">
                <button
                  type="button"
                  class="skills-segmented-item"
                  :class="{ active: scopeFilter === 'personal' }"
                  data-testid="skills-scope-filter-personal"
                  role="tab"
                  @click="scopeFilter = 'personal'"
                >
                  <span class="skills-scope-dot skills-scope-dot-personal" aria-hidden="true"></span>
                  <span>私人使用</span>
                  <span class="skills-segmented-count">{{ scopeCounts.personal }}</span>
                </button>
                <button
                  type="button"
                  class="skills-segmented-item"
                  :class="{ active: scopeFilter === 'project' }"
                  data-testid="skills-scope-filter-project"
                  role="tab"
                  @click="scopeFilter = 'project'"
                >
                  <span class="skills-scope-dot skills-scope-dot-project" aria-hidden="true"></span>
                  <span>项目共享</span>
                  <span class="skills-segmented-count">{{ scopeCounts.project }}</span>
                </button>
                <button
                  type="button"
                  class="skills-segmented-item"
                  :class="{ active: scopeFilter === 'all' }"
                  data-testid="skills-scope-filter-all"
                  role="tab"
                  @click="scopeFilter = 'all'"
                >
                  <span>全部</span>
                  <span class="skills-segmented-count">{{ scopeCounts.all }}</span>
                </button>
              </div>
              <button
                v-if="showResolutionCheckButton"
                class="btn btn-ghost btn-xs skills-resolution-check"
                :class="{ 'is-warning': resolutionConflicts.length > 0 }"
                data-testid="skills-resolution-refresh"
                :disabled="resolutionLoading"
                @click="refreshSkillResolutions"
              >
                {{ resolutionCheckButtonText }}
              </button>
              <button
                v-if="resolutionConflicts.length > 0"
                class="btn btn-ghost btn-xs"
                data-testid="skills-resolution-panel-toggle"
                @click="toggleResolutionPanel"
              >
                {{ resolutionPanelToggleText }}
              </button>
            </div>
            <div v-if="resolutionConflictAlertText" class="skills-resolution-alert" data-testid="skills-resolution-alert">
              {{ resolutionConflictAlertText }}
            </div>
            <div v-if="showResolutionPanel" class="skills-resolution-list" data-testid="skills-resolution-list">
              <article
                v-for="(conflict, conflictIdx) in resolutionConflicts"
                :key="conflict.conflict_id || conflictIdx"
                class="skills-resolution-item"
                :data-testid="'skills-resolution-item-' + conflictIdx"
              >
                <div class="skills-resolution-main">
                  <strong>{{ resolutionTitle(conflict) }}</strong>
                  <span>{{ resolutionProviderEntry(conflict).provider ? resolutionProviderEntryLabel(resolutionProviderEntry(conflict)) : scopeLabel(conflict.scope) }}</span>
                </div>
                <p class="skills-resolution-guide">{{ resolutionConflictGuide(conflict) }}</p>
                <div v-if="resolutionActionEntries(conflict).length > 0" class="skills-resolution-actions-title">{{ resolutionActionSectionTitle(conflict) }}</div>
                <div
                  v-for="(entry, sourceIdx) in resolutionProviderEntries(conflict)"
                  :key="entry.source_path_id || entry.provider || sourceIdx"
                  class="skills-resolution-actions"
                >
                  <span v-if="resolutionProviderEntries(conflict).length > 1 || entry.merged_provider_entry" class="skills-resolution-source">{{ resolutionProviderEntryLabel(entry) }}</span>
                  <button
                    v-for="(actionEntry, actionIdx) in resolutionActionEntries(conflict)"
                    :key="resolutionApplyKey(conflict, actionEntry.action, resolutionActionEntryTarget(actionEntry, entry)) + '-' + actionIdx"
                    class="btn btn-ghost btn-xs"
                    :disabled="resolutionActioning === resolutionApplyKey(conflict, actionEntry.action, resolutionActionEntryTarget(actionEntry, entry))"
                    :data-testid="'skills-resolution-action-' + conflictIdx + '-' + sourceIdx + '-' + actionIdx"
                    @click="onApplyResolution(conflict, actionEntry.action, resolutionActionEntryTarget(actionEntry, entry))"
                  >
                    <span v-if="resolutionActioning === resolutionApplyKey(conflict, actionEntry.action, resolutionActionEntryTarget(actionEntry, entry))">处理中...</span>
                    <span v-else>{{ resolutionActionEntryLabel(actionEntry) }}</span>
                  </button>
                  <div
                    v-if="resolutionNamePromptApplies(conflict, entry)"
                    class="skills-resolution-name-field skills-resolution-name-inline"
                    data-testid="skills-resolution-name-prompt"
                  >
                    <label class="skills-resolution-name-input-row">
                      <span>新技能名称</span>
                      <input
                        v-model="resolutionNameInput"
                        class="modal-input"
                        data-testid="skills-resolution-name-input"
                        placeholder="例如：skill-private"
                        @keyup.enter="confirmResolutionNewName"
                      />
                    </label>
                    <div class="skills-resolution-name-actions">
                      <span>{{ resolutionNamePromptHelpText(resolutionNamePrompt) }}</span>
                      <button
                        class="btn btn-primary btn-xs"
                        data-testid="skills-resolution-name-confirm"
                        :disabled="resolutionActioning === resolutionNamePrompt.applyKey"
                        @click="confirmResolutionNewName"
                      >
                        {{ resolutionNamePromptButtonText(resolutionNamePrompt, resolutionActioning) }}
                      </button>
                      <button class="btn btn-ghost btn-xs" data-testid="skills-resolution-name-cancel" @click="clearResolutionNamePrompt">取消</button>
                    </div>
                  </div>
                  <article v-if="resolutionPreviewApplies(conflict, entry)" class="skills-resolution-preview is-inline" data-testid="skills-resolution-preview">
                    <div class="skills-resolution-preview-head">
                      <div>
                        <strong>{{ resolutionActionLabel(resolutionPreview.action) }}</strong>
                        <p>{{ resolutionPreviewIntro(resolutionPreview) }}</p>
                      </div>
                      <button
                        v-if="resolutionPreview.requiresApply"
                        class="btn btn-primary btn-xs"
                        data-testid="skills-resolution-confirm"
                        :disabled="resolutionActioning === 'confirm'"
                        @click="confirmResolutionPreview"
                      >
                        {{ resolutionActioning === 'confirm' ? '应用中...' : '确认应用' }}
                      </button>
                      <button class="btn btn-ghost btn-xs" data-testid="skills-resolution-cancel" @click="clearResolutionPreview">取消</button>
                    </div>
                    <div
                      v-for="(item, previewIdx) in (resolutionPreview.items || [])"
                      :key="item.preview_id || item.source_path_id || previewIdx"
                      class="skills-resolution-preview-item"
                    >
                      <div class="skills-resolution-preview-summary">{{ resolutionPreviewItemSummary(item, resolutionPreview.action) }}</div>
                      <div class="skills-resolution-preview-paths">
                        <div
                          v-for="pathItem in resolutionPreviewItemPaths(item, resolutionPreview.action)"
                          :key="pathItem.label + pathItem.value"
                          class="skills-resolution-preview-path-row"
                        >
                          <span>{{ pathItem.label }}</span>
                          <code>{{ pathItem.value }}</code>
                        </div>
                      </div>
                      <details v-if="item.diff || item.source_hash || item.target_hash" class="skills-resolution-technical">
                        <summary>技术信息</summary>
                        <div v-if="item.source_hash" class="skills-resolution-preview-path">外部版本号：{{ resolutionShortHash(item.source_hash) }}</div>
                        <div v-if="item.target_hash" class="skills-resolution-preview-path">管理版本号：{{ resolutionShortHash(item.target_hash) }}</div>
                        <pre v-if="item.diff" class="skills-resolution-preview-diff">{{ item.diff }}</pre>
                      </details>
                    </div>
                  </article>
                </div>
                <div v-if="resolutionActionFootnote(conflict)" class="skills-resolution-action-help">
                  <span>{{ resolutionActionFootnote(conflict) }}</span>
                </div>
                <div v-else-if="resolutionActionEntries(conflict).length > 0" class="skills-resolution-action-help">
                  <div
                    v-for="(actionEntry, actionHelpIdx) in resolutionActionEntries(conflict)"
                    :key="'help-' + resolutionApplyKey(conflict, actionEntry.action, actionEntry) + '-' + actionHelpIdx"
                  >
                    <strong>{{ resolutionActionEntryLabel(actionEntry) }}</strong>
                    <span>{{ resolutionActionEntryHelp(actionEntry) }}</span>
                  </div>
                </div>
                <div v-if="resolutionManualSteps(conflict).length > 0" class="skills-resolution-manual-steps">
                  <strong>处理方式</strong>
                  <ol>
                    <li v-for="step in resolutionManualSteps(conflict)" :key="step">{{ step }}</li>
                  </ol>
                </div>
              </article>
            </div>
            <div v-if="skillCards.length === 0" class="empty-state" data-testid="skills-empty-state">
              <div class="es-icon skills-empty-icon">
                <svg viewBox="0 0 24 24" width="32" height="32" aria-hidden="true">
                  <path fill="currentColor" d="M12 2 3 7v6c0 5 3.8 8.7 9 9 5.2-.3 9-4 9-9V7l-9-5zm0 2.2 7 3.9v4.9c0 4-2.9 6.9-7 7.2-4.1-.3-7-3.2-7-7.2V8.1l7-3.9zM11 8v4H7v2h4v4h2v-4h4v-2h-4V8h-2z"/>
                </svg>
              </div>
              <h3>暂无技能</h3>
              <p>支持一次导入多个技能文件夹</p>
            </div>
            <div v-else-if="filteredSkillCards.length === 0" class="empty-state" data-testid="skills-search-empty-state">
              <div class="es-icon skills-empty-icon">
                <svg viewBox="0 0 24 24" width="32" height="32" aria-hidden="true">
                  <path fill="currentColor" d="M10 2a8 8 0 1 0 5 14.3l5 5 1.4-1.4-5-5A8 8 0 0 0 10 2zm0 2a6 6 0 1 1 0 12 6 6 0 0 1 0-12z"/>
                </svg>
              </div>
              <h3>没有匹配技能</h3>
              <p>尝试更换关键词或切换使用范围，支持按名称、简介、关键词搜索</p>
            </div>
            <div v-else class="skills-card-grid" data-testid="skills-list">
              <article
                v-for="(item, idx) in filteredSkillCards"
                :key="skillCardKey(item)"
                class="data-card-vue skill-card skill-card-compact"
                :class="{ active: isSkillCardActive(item) }"
                :data-testid="'skills-card-' + idx"
              >
                <div class="skill-card-header">
                  <div class="skill-card-heading">
                    <div class="skill-card-title">{{ item.displayLabel }}</div>
                    <div v-if="item.displayName" class="skill-card-path" :title="item.name">{{ item.name }}</div>
                    <div class="skill-card-path" :title="item.dir">{{ item.dir || '-' }}</div>
                  </div>
                  <div class="skill-card-tags">
                    <span
                      class="skill-card-scope-tag"
                      :class="'skill-card-scope-' + (item.scope || 'project')"
                      :title="scopeLabel(item.scope)"
                      :data-testid="'skills-card-scope-' + idx"
                    >{{ scopeLabel(item.scope) }}</span>
                    <span v-if="isSkillCardActive(item)" class="skill-card-badge">编辑中</span>
                    <span v-if="isSkillCardRecentlySaved(item)" class="skill-card-saved-badge" :data-testid="'skills-card-saved-' + idx">已保存</span>
                  </div>
                </div>
                <div class="skill-card-description">{{ item.description || '暂无简介' }}</div>
                <div class="skill-card-summary-preview">{{ item.summary || '暂无简介，点击编辑补充。' }}</div>
                <div class="skill-word-groups">
                  <div v-if="(item.displayScenarioWords || []).length > 0" class="skill-word-line">
                    <strong>关键词</strong>
                    <div class="skill-chip-row">
                      <span
                        v-for="(word, wordIdx) in item.displayScenarioWords.slice(0, 4)"
                        :key="'trigger-' + idx + '-' + wordIdx"
                        class="skill-word-chip"
                      >
                        {{ word }}
                      </span>
                      <span v-if="item.displayScenarioWords.length > 4" class="skill-word-chip muted">+{{ item.displayScenarioWords.length - 4 }}</span>
                    </div>
                  </div>
                </div>
                <div class="data-actions-vue skill-actions">
                  <button class="btn btn-secondary btn-xs" :data-testid="'skills-edit-button-' + idx" @click="onEditSkill(item)">编辑详情</button>
                  <button class="btn btn-ghost btn-xs btn-warning" :data-testid="'skills-delete-button-' + idx" :disabled="Boolean(deletingSkillName)" @click="onDeleteSkill(item)">
                    {{ isDeletingSkill(item.name) ? '删除中...' : '删除' }}
                  </button>
                </div>
              </article>
            </div>
            <div v-if="showSkillCount" class="skills-inline-tip">
              {{ skillCountText }}
            </div>
            <div v-if="notice.message && !isEditorOpen" class="skills-notice" data-testid="skills-notice" :class="'is-' + notice.level">
              {{ notice.message }}
            </div>
            <div v-if="visibleImportSummaryDrafts.length > 0" class="skills-import-summary-panel" data-testid="skills-import-summary-panel">
              <div class="skills-import-summary-head">
                <div>
                  <strong>{{ importSummaryPanelTitle }}</strong>
                  <span>{{ importSummaryPanelHint }}</span>
                </div>
                <button class="btn btn-ghost btn-xs" data-testid="skills-import-summary-clear" @click="clearImportSummaryDrafts">收起</button>
              </div>
              <article
                v-for="(draft, draftIdx) in visibleImportSummaryDrafts"
                :key="draft.id || draft.skillFile || draftIdx"
                class="skills-import-summary-item"
                :class="'is-' + draft.status"
                :data-testid="'skills-import-summary-item-' + draftIdx"
              >
                <div class="skills-import-summary-main">
                  <strong>{{ draft.name || '未命名技能' }}</strong>
                  <span>{{ scopeLabel(draft.scope) }}</span>
                </div>
                <p v-if="draft.status === 'ready' || draft.status === 'applied'" class="skills-import-summary-text">{{ draft.suggestion }}</p>
                <p v-else-if="draft.status === 'conflict'" class="skills-import-summary-text">{{ draft.error }}</p>
                <p v-else class="skills-import-summary-text">{{ draft.error || '技能已正常导入。可以稍后手动补充简介。' }}</p>
                <div class="skills-import-summary-actions">
                  <button
                    v-if="draft.status === 'ready'"
                    class="btn btn-secondary btn-xs"
                    :data-testid="'skills-import-summary-apply-' + draftIdx"
                    @click="applyImportSummaryDraft(draft)"
                  >
                    采用并编辑
                  </button>
                  <span v-else-if="draft.status === 'applied'" class="skills-inline-tip">已采用，保存后生效</span>
                  <button
                    v-else-if="draft.status === 'error'"
                    class="btn btn-secondary btn-xs"
                    :data-testid="'skills-import-summary-edit-' + draftIdx"
                    @click="openImportSummaryDraft(draft)"
                  >
                    编辑简介
                  </button>
                  <button class="btn btn-ghost btn-xs" :data-testid="'skills-import-summary-dismiss-' + draftIdx" @click="dismissImportSummaryDraft(draft)">跳过</button>
                </div>
              </article>
            </div>
            <ul v-if="importFailures.length > 0" class="skills-failure-list" data-testid="skills-failure-list">
              <li v-for="item in importFailures.slice(0, 5)" :key="item">{{ item }}</li>
            </ul>
            <div v-if="importFailures.length > 5" class="skills-inline-tip">
              还有 {{ importFailures.length - 5 }} 条失败项
            </div>
          </div>
        </div>
      </div>
      <div
        v-if="isEditorOpen"
        class="modal-overlay skills-editor-overlay"
        data-testid="skills-editor-modal-overlay"
        tabindex="0"
        @click.self="closeEditor"
        @keydown.esc.prevent="closeEditor"
      >
        <div class="modal-box skills-editor-modal" :class="{ 'is-body-expanded': isBodyEditing || bodyEditorFocused }" role="dialog" aria-modal="true" data-testid="skills-editor-panel">
          <div class="skills-editor-modal-head">
            <div>
              <div class="modal-title">编辑技能</div>
              <div class="skills-inline-tip">你可以修改简介和技能内容。</div>
            </div>
            <button class="btn btn-ghost" data-testid="skills-editor-close-button" @click="closeEditor">关闭</button>
          </div>
          <div class="skills-editor-panel">
            <div class="skills-field">
              <label>技能名称</label>
              <input v-model="form.name" :disabled="!isEditingMainSkillFile" class="modal-input" data-testid="skills-editor-name-input" placeholder="例如：backend" />
            </div>
            <div class="skills-field">
              <label>显示名称</label>
              <input v-model="form.displayName" :disabled="!isEditingMainSkillFile" class="modal-input" data-testid="skills-editor-display-name-input" placeholder="例如：后端开发" />
            </div>
            <div class="skills-field">
              <div class="skills-editor-label-row">
                <label>技能简介</label>
                <button class="btn btn-ghost btn-sm" data-testid="skills-summary-suggest-button" :disabled="!isEditingMainSkillFile || summarySuggesting || (!form.name.trim() && !form.body.trim())" @click="onSuggestSkillSummary">{{ summarySuggesting ? '生成中' : '帮我生成' }}</button>
              </div>
              <input v-model="form.description" :disabled="!isEditingMainSkillFile" class="modal-input" data-testid="skills-editor-summary-input" placeholder="一句话说明你会在什么情况下使用这个技能" />
              <div v-if="summarySuggestion" class="skills-inline-tip" data-testid="skills-summary-suggestion">
                建议：{{ summarySuggestion }}
                <button class="btn btn-ghost btn-sm" data-testid="skills-summary-apply-button" @click="applySummarySuggestion">采用</button>
                <button class="btn btn-ghost btn-sm" data-testid="skills-summary-regenerate-button" @click="onSuggestSkillSummary">重新生成</button>
              </div>
              <div v-else-if="generatedSummaryPreview" class="skills-inline-tip">根据正文预览：{{ generatedSummaryPreview }}</div>
              <div class="skills-inline-tip">建议写成“当你需要……时使用”。</div>
            </div>
            <div class="skills-field">
              <label>使用范围</label>
              <div class="skills-segmented skills-editor-scope" data-testid="skills-editor-scope-group">
                <label class="skills-segmented-item" :class="{ active: form.scope === 'project', disabled: !isEditingMainSkillFile }">
                  <input v-model="form.scope" data-testid="skills-editor-scope-project" type="radio" value="project" :disabled="!isEditingMainSkillFile" />
                  <span class="skills-scope-dot skills-scope-dot-project" aria-hidden="true"></span>
                  <span>项目共享</span>
                </label>
                <label class="skills-segmented-item" :class="{ active: form.scope === 'personal', disabled: !isEditingMainSkillFile }">
                  <input v-model="form.scope" data-testid="skills-editor-scope-personal" type="radio" value="personal" :disabled="!isEditingMainSkillFile" />
                  <span class="skills-scope-dot skills-scope-dot-personal" aria-hidden="true"></span>
                  <span>私人使用</span>
                </label>
              </div>
            </div>
            <div class="skills-field">
              <label>关键词</label>
              <input v-model="scenarioKeywordsText" :disabled="!isEditingMainSkillFile" class="modal-input" data-testid="skills-editor-trigger-input" placeholder="例如：bug、调试、部署、后端" />
              <div class="skills-inline-tip">可选填入，用于辅助匹配使用技能</div>
            </div>
            <div v-if="showRelatedSkillFiles" class="skills-field">
              <label>附加内容</label>
              <div class="skills-subfile-list" data-testid="skills-subfiles-list">
                <button
                  v-for="(file, fileIdx) in skillFiles"
                  :key="file.path || (file.name + '-' + fileIdx)"
                  class="skills-subfile-item"
                  :class="{ active: activeSkillFilePath === file.path }"
                  :data-testid="'skills-subfile-item-' + fileIdx"
                  @click="onOpenSkillSubfile(file)"
                >
                  <span class="skills-subfile-name">{{ file.name }}</span>
                  <span v-if="file.isMain" class="skills-subfile-main-tag">主要文件</span>
                </button>
              </div>
              <div class="skills-inline-tip">这里是这个技能附带的示例、模板或脚本。</div>
            </div>
            <div class="skills-field skills-field-body">
              <div class="skills-body-head">
                <label>{{ isEditingMainSkillFile ? '技能内容' : '关联文件内容' }}</label>
                <div class="skills-body-head-actions">
                  <button
                    v-if="!isBodyEditing"
                    class="btn btn-secondary btn-xs skills-body-toggle"
                    data-testid="skills-editor-body-edit-button"
                    @click="startBodyEdit"
                  >
                    编辑正文
                  </button>
                  <button
                    v-else
                    class="btn btn-ghost btn-xs skills-body-toggle"
                    data-testid="skills-editor-body-preview-button"
                    @click="finishBodyEdit"
                  >
                    预览正文
                  </button>
                </div>
              </div>
              <div
                v-if="!isBodyEditing"
                class="skills-body-preview chat-item-markdown agent-markdown-root"
                data-testid="skills-editor-body-preview"
                v-html="skillBodyMarkdownHtml"
                @click="onSkillPreviewClick"
              ></div>
              <textarea
                v-else
                ref="bodyInputRef"
                v-model="form.body"
                class="modal-input skills-body-input"
                :class="{ 'is-expanded': isBodyEditing || bodyEditorFocused }"
                data-testid="skills-editor-body-input"
                placeholder="输入技能内容"
                @focus="onBodyFocus"
                @blur="onBodyBlur"
              ></textarea>
              <div class="skills-inline-tip">点击“编辑正文”展开编辑；切回“预览正文”查看效果。</div>
              <div v-if="!isEditingMainSkillFile" class="skills-inline-tip">当前正在编辑关联文件。</div>
            </div>
            <div class="skills-actions-row" data-testid="skills-editor-actions">
              <div v-if="notice.message" class="skills-notice skills-editor-notice" data-testid="skills-editor-notice" :class="'is-' + notice.level">
                {{ notice.message }}
              </div>
              <button class="btn btn-ghost" data-testid="skills-editor-cancel-button" @click="closeEditor">取消</button>
              <button class="btn btn-primary skills-save-btn" data-testid="skills-save-button" :disabled="saving" @click="onSaveSkill">
                {{ saveButtonLabel }}
              </button>
            </div>
          </div>
        </div>
      </div>
      <div v-if="confirmDeleteTarget" class="modal-overlay" data-testid="skills-delete-overlay" @click.self="cancelSkillDelete">
        <div class="modal-box memory-modal" role="dialog" aria-modal="true" data-testid="skills-delete-modal">
          <div class="memory-modal-head">
            <div>
              <div class="modal-title">删除技能</div>
              <div class="memory-modal-tip">{{ confirmDeleteTarget.name }} · {{ scopeLabel(confirmDeleteTarget.scope) }}</div>
            </div>
            <button class="btn btn-ghost" data-testid="skills-delete-close" :disabled="Boolean(deletingSkillName)" @click="cancelSkillDelete">关闭</button>
          </div>
          <div class="memory-form-helper">保存位置：{{ confirmDeleteTarget.dir || '-' }}</div>
          <div class="memory-form-helper">确定删除技能 “{{ confirmDeleteTarget.name }}” 吗？该操作会删除技能目录及其资源文件，无法恢复。</div>
          <div class="memory-editor-actions">
            <button class="btn btn-ghost" data-testid="skills-delete-cancel" :disabled="Boolean(deletingSkillName)" @click="cancelSkillDelete">取消</button>
            <button class="btn btn-danger" data-testid="skills-delete-confirm" :disabled="Boolean(deletingSkillName)" @click="confirmSkillDelete">{{ isDeletingSkill(confirmDeleteTarget.name) ? '删除中...' : '确认删除' }}</button>
          </div>
        </div>
      </div>
      <div v-if="importScopePromptOpen" class="modal-overlay" data-testid="skills-import-scope-modal" @click.self="cancelImportScopePrompt">
        <div class="modal-box memory-modal" role="dialog" aria-modal="true">
          <div class="memory-modal-head">
            <div>
              <div class="modal-title">导入技能</div>
              <div class="memory-modal-tip">选择导入后的使用范围</div>
            </div>
            <button class="btn btn-ghost" data-testid="skills-import-scope-close" :disabled="uploading" @click="cancelImportScopePrompt">关闭</button>
          </div>
          <div class="memory-form-helper">这些技能导入后给谁使用？</div>
          <div class="memory-editor-actions">
            <button class="btn btn-ghost" data-testid="skills-import-scope-cancel" :disabled="uploading" @click="cancelImportScopePrompt">取消</button>
            <button class="btn btn-secondary" data-testid="skills-import-scope-personal" :disabled="uploading" @click="confirmImportScope('personal')">私人使用</button>
            <button class="btn btn-primary" data-testid="skills-import-scope-project" :disabled="uploading" @click="confirmImportScope('project')">项目共享</button>
          </div>
        </div>
      </div>
    </section>
  `,
};
