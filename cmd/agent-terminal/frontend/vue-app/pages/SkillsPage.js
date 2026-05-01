import { computed, ref } from '../../lib/vue.esm-browser.prod.js';
import { useSkillEditor } from '../composables/useSkillEditor.js';
import { useSkillFileNavigation } from '../composables/useSkillFileNavigation.js';
import { isSkillMainFilePath } from '../utils/skill-parser.js';
import { approveCandidate, rejectCandidate, getCandidate } from '../services/skills-api.js';

/** @typedef {{ name?: string, dir?: string, description?: string, summary?: string, trigger_words?: string[], force_words?: string[] }} SkillListItem */
/** @typedef {{ name: string, dir: string, description: string, summary: string, triggerWords: string[], forceWords: string[] }} SkillCard */
/** @typedef {{ skills?: SkillListItem[], projectStore?: { state?: { active?: string } } | null }} SkillsPageProps */

export { normalizeWordList, listToText, inferSkillNameFromPath,
  summarizeItems, normalizePathKey, fileNameFromPath,
  skillDirFromFilePath, isSkillMainFilePath, parseFrontmatter,
  parseWordsValue, cleanScalar, parseSkillMarkdown,
  quoteYAML, buildSkillMarkdown,
} from '../utils/skill-parser.js';

export const SkillsPage = {
  name: 'SkillsPage',
  props: {
    skills: { type: Array, default: () => [] },
    projectStore: { type: Object, default: null },
    pendingCandidates: { type: Array, default: () => [] },
  },
  emits: ['refresh-skills', 'refresh-candidates'],
  setup(props, { emit }) {
    const searchQuery = ref('');
    const scopeFilter = ref('all');

    const scopeForTrust = (trust) => {
      const value = (trust || '').toString().trim().toLowerCase();
      if (value === 'user' || value === 'system') return 'system';
      if (value === 'signed') return 'signed';
      return 'project';
    };

    const skillCards = computed(() => {
      const list = Array.isArray(props.skills) ? props.skills : [];
      return list.map((item) => ({
        name: (item?.name || '').toString(),
        dir: (item?.dir || '').toString(),
        description: (item?.description || '').toString(),
        summary: (item?.summary || item?.description || '').toString(),
        trust: (item?.trust || '').toString(),
        scope: scopeForTrust(item?.trust),
        triggerWords: Array.isArray(item?.trigger_words) ? item.trigger_words : [],
        forceWords: Array.isArray(item?.force_words) ? item.force_words : [],
      }));
    });

    const scopeCounts = computed(() => {
      const counts = { all: 0, project: 0, system: 0, signed: 0 };
      skillCards.value.forEach((item) => {
        counts.all += 1;
        if (item.scope === 'system') counts.system += 1;
        else if (item.scope === 'signed') counts.signed += 1;
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
          item.description,
          item.summary,
          item.dir,
          ...(Array.isArray(item.triggerWords) ? item.triggerWords : []),
          ...(Array.isArray(item.forceWords) ? item.forceWords : []),
        ]
          .join(' ')
          .toLowerCase();
        return haystack.includes(keyword);
      });
    });

    const sourcePath = ref('');
    const activeSkillFilePath = ref('');
    const activeCwdSource = computed(() => {
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
    const candidateActioning = ref(null);
    const candidatePreview = ref(null);

    function formatCandidateTime(ts) {
      if (!ts) return '';
      try { return new Date(ts).toLocaleString('zh-CN'); } catch { return ts; }
    }

    async function onApproveCandidate(item) {
      candidateActioning.value = item.id;
      try {
        const cwd = (props.projectStore?.state?.active || '').toString().trim();
        await approveCandidate(item.id, 'ui-user', '', cwd);
        emit('refresh-candidates');
        emit('refresh-skills');
      } catch (e) {
        console.warn('approve candidate failed', e);
      } finally {
        candidateActioning.value = null;
      }
    }

    async function onRejectCandidate(item) {
      candidateActioning.value = item.id;
      try {
        const cwd = (props.projectStore?.state?.active || '').toString().trim();
        await rejectCandidate(item.id, '', cwd);
        emit('refresh-candidates');
      } catch (e) {
        console.warn('reject candidate failed', e);
      } finally {
        candidateActioning.value = null;
      }
    }

    async function onPreviewCandidate(item) {
      try {
        candidatePreview.value = await getCandidate(item.id);
      } catch (e) {
        console.warn('get candidate detail failed', e);
        candidatePreview.value = null;
      }
    }

    function closeCandidatePreview() {
      candidatePreview.value = null;
    }

    return {
      searchQuery,
      scopeFilter,
      scopeCounts,
      filteredSkillCards,
      skillCards,
      candidateActioning,
      candidatePreview,
      formatCandidateTime,
      onApproveCandidate,
      onRejectCandidate,
      onPreviewCandidate,
      closeCandidatePreview,
      ...editor,
      ...fileNavigation,
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
          <div class="section-header">SKILL 列表</div>
          <div class="panel-body skills-list-panel" data-testid="skills-list-panel">
            <div class="skills-toolbar" data-testid="skills-toolbar">
              <button class="btn btn-secondary" data-testid="skills-import-button" :disabled="uploading" @click="onUploadSkill">
                {{ uploading ? '导入中...' : '批量导入技能目录' }}
              </button>
              <button class="btn btn-ghost" data-testid="skills-create-button" @click="onCreateSkill">
                新建 Skill
              </button>
              <div class="skills-search-wrap">
                <input
                  v-model="searchQuery"
                  class="modal-input skills-search-input"
                  data-testid="skills-search-input"
                  placeholder="搜索技能名称、摘要、触发词..."
                />
              </div>
            </div>
            <div class="skills-subtoolbar" data-testid="skills-subtoolbar">
              <div class="skills-segmented skills-scope-filter" data-testid="skills-scope-filter" role="tablist">
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
                <button
                  type="button"
                  class="skills-segmented-item"
                  :class="{ active: scopeFilter === 'project' }"
                  data-testid="skills-scope-filter-project"
                  role="tab"
                  @click="scopeFilter = 'project'"
                >
                  <span class="skills-scope-dot skills-scope-dot-project" aria-hidden="true"></span>
                  <span>project</span>
                  <span class="skills-segmented-count">{{ scopeCounts.project }}</span>
                </button>
                <button
                  type="button"
                  class="skills-segmented-item"
                  :class="{ active: scopeFilter === 'system' }"
                  data-testid="skills-scope-filter-system"
                  role="tab"
                  @click="scopeFilter = 'system'"
                >
                  <span class="skills-scope-dot skills-scope-dot-system" aria-hidden="true"></span>
                  <span>system</span>
                  <span class="skills-segmented-count">{{ scopeCounts.system }}</span>
                </button>
                <button
                  type="button"
                  class="skills-segmented-item"
                  :class="{ active: scopeFilter === 'pending' }"
                  data-testid="skills-scope-filter-pending"
                  role="tab"
                  @click="scopeFilter = 'pending'"
                >
                  <span class="skills-pending-dot" aria-hidden="true"></span>
                  <span>待审批</span>
                  <span class="skills-segmented-count">{{ pendingCandidates.length }}</span>
                </button>
              </div>
              <div class="skills-segmented skills-import-scope" data-testid="skills-import-scope-group" role="group" aria-label="导入位置">
                <span class="skills-segmented-label">导入位置</span>
                <label class="skills-segmented-item" :class="{ active: importScope === 'project' }">
                  <input v-model="importScope" data-testid="skills-import-scope-project" type="radio" value="project" />
                  <span class="skills-scope-dot skills-scope-dot-project" aria-hidden="true"></span>
                  <span>project</span>
                </label>
                <label class="skills-segmented-item" :class="{ active: importScope === 'system' }">
                  <input v-model="importScope" data-testid="skills-import-scope-system" type="radio" value="system" />
                  <span class="skills-scope-dot skills-scope-dot-system" aria-hidden="true"></span>
                  <span>system</span>
                </label>
              </div>
            </div>
            <div v-if="scopeFilter === 'pending'" class="panel-body skills-list-panel" data-testid="candidates-panel">
              <div v-if="pendingCandidates.length === 0" class="empty-state">
                <h3>暂无待审批技能</h3>
                <p>系统从你的反馈中提炼的技能候选会出现在这里。</p>
              </div>
              <div v-else class="skills-card-grid" data-testid="candidates-list">
                <article
                  v-for="item in pendingCandidates"
                  :key="item.id"
                  class="data-card-vue skill-card skill-candidate-card"
                  :data-testid="'candidate-card-' + item.id"
                >
                  <div class="skill-card-header">
                    <div class="skill-card-heading">
                      <div class="skill-card-title">{{ item.slug }}</div>
                      <div class="skill-card-path">{{ formatCandidateTime(item.created_at) }}</div>
                    </div>
                    <span class="skill-card-scope-tag skill-card-scope-pending">待审批</span>
                  </div>
                  <div class="skill-card-description">{{ item.scope }} scope</div>
                  <div class="skill-candidate-actions">
                    <button
                      class="btn btn-primary btn-xs"
                      :disabled="candidateActioning === item.id"
                      :data-testid="'candidate-approve-' + item.id"
                      @click="onApproveCandidate(item)"
                    >
                      {{ candidateActioning === item.id ? '处理中...' : '批准' }}
                    </button>
                    <button
                      class="btn btn-ghost btn-xs btn-warning"
                      :disabled="candidateActioning === item.id"
                      :data-testid="'candidate-reject-' + item.id"
                      @click="onRejectCandidate(item)"
                    >
                      拒绝
                    </button>
                    <button
                      class="btn btn-ghost btn-xs"
                      :data-testid="'candidate-preview-' + item.id"
                      @click="onPreviewCandidate(item)"
                    >
                      预览
                    </button>
                  </div>
                </article>
              </div>
              <div v-if="candidatePreview" class="skill-candidate-preview" data-testid="candidate-preview">
                <div class="skill-candidate-preview-head">
                  <strong>预览：{{ candidatePreview.slug }}</strong>
                  <button class="btn btn-ghost btn-xs" @click="closeCandidatePreview">关闭</button>
                </div>
                <pre class="skill-candidate-preview-body">{{ candidatePreview.redacted_sample || '无内容' }}</pre>
              </div>
            </div>
            <div v-else-if="skillCards.length === 0" class="empty-state" data-testid="skills-empty-state">
              <div class="es-icon skills-empty-icon">
                <svg viewBox="0 0 24 24" width="32" height="32" aria-hidden="true">
                  <path fill="currentColor" d="M12 2 3 7v6c0 5 3.8 8.7 9 9 5.2-.3 9-4 9-9V7l-9-5zm0 2.2 7 3.9v4.9c0 4-2.9 6.9-7 7.2-4.1-.3-7-3.2-7-7.2V8.1l7-3.9zM11 8v4H7v2h4v4h2v-4h4v-2h-4V8h-2z"/>
                </svg>
              </div>
              <h3>暂无 Skill</h3>
              <p>支持一次导入多个目录（每个目录需包含 SKILL.md）</p>
            </div>
            <div v-else-if="filteredSkillCards.length === 0" class="empty-state" data-testid="skills-search-empty-state">
              <div class="es-icon skills-empty-icon">
                <svg viewBox="0 0 24 24" width="32" height="32" aria-hidden="true">
                  <path fill="currentColor" d="M10 2a8 8 0 1 0 5 14.3l5 5 1.4-1.4-5-5A8 8 0 0 0 10 2zm0 2a6 6 0 1 1 0 12 6 6 0 0 1 0-12z"/>
                </svg>
              </div>
              <h3>没有匹配技能</h3>
              <p>尝试更换关键词或切换 scope，支持按名称、描述、摘要、触发词搜索</p>
            </div>
            <div v-else class="skills-card-grid" data-testid="skills-list">
              <article
                v-for="(item, idx) in filteredSkillCards"
                :key="item.name"
                class="data-card-vue skill-card skill-card-compact"
                :class="{ active: selectedSkillName.toLowerCase() === item.name.toLowerCase() }"
                :data-testid="'skills-card-' + idx"
              >
                <div class="skill-card-header">
                  <div class="skill-card-heading">
                    <div class="skill-card-title">{{ item.name }}</div>
                    <div class="skill-card-path" :title="item.dir">{{ item.dir || '-' }}</div>
                  </div>
                  <div class="skill-card-tags">
                    <span
                      class="skill-card-scope-tag"
                      :class="'skill-card-scope-' + (item.scope || 'project')"
                      :title="'scope: ' + (item.scope || 'project')"
                      :data-testid="'skills-card-scope-' + idx"
                    >{{ item.scope || 'project' }}</span>
                    <span v-if="selectedSkillName.toLowerCase() === item.name.toLowerCase()" class="skill-card-badge">编辑中</span>
                  </div>
                </div>
                <div class="skill-card-description">{{ item.description || '暂无描述' }}</div>
                <div class="skill-card-summary-preview">{{ item.summary || '暂无摘要，点击编辑补充。' }}</div>
                <div class="skill-word-groups">
                  <div v-if="(item.triggerWords || []).length > 0" class="skill-word-line">
                    <strong>触发词</strong>
                    <div class="skill-chip-row">
                      <span
                        v-for="(word, wordIdx) in item.triggerWords.slice(0, 4)"
                        :key="'trigger-' + idx + '-' + wordIdx"
                        class="skill-word-chip"
                      >
                        {{ word }}
                      </span>
                      <span v-if="item.triggerWords.length > 4" class="skill-word-chip muted">+{{ item.triggerWords.length - 4 }}</span>
                    </div>
                  </div>
                  <div v-if="(item.forceWords || []).length > 0" class="skill-word-line">
                    <strong>强制词</strong>
                    <div class="skill-chip-row">
                      <span
                        v-for="(word, wordIdx) in item.forceWords.slice(0, 3)"
                        :key="'force-' + idx + '-' + wordIdx"
                        class="skill-word-chip skill-word-chip-force"
                      >
                        {{ word }}
                      </span>
                      <span v-if="item.forceWords.length > 3" class="skill-word-chip muted">+{{ item.forceWords.length - 3 }}</span>
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
            <div v-if="skillCards.length > 0" class="skills-inline-tip">
              显示 {{ filteredSkillCards.length }} / {{ skillCards.length }} 个技能
            </div>
            <div v-if="notice.message" class="skills-notice" data-testid="skills-notice" :class="'is-' + notice.level">
              {{ notice.message }}
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
              <div class="skills-inline-tip">系统会先生成一版摘要；你可以直接修改并保存到 SKILL.md 的 frontmatter。</div>
            </div>
            <button class="btn btn-ghost" data-testid="skills-editor-close-button" @click="closeEditor">关闭</button>
          </div>
          <div class="skills-editor-panel">
            <div class="skills-field">
              <label>技能名称</label>
              <input v-model="form.name" :disabled="!isEditingMainSkillFile" class="modal-input" data-testid="skills-editor-name-input" placeholder="例如：backend" />
            </div>
            <div class="skills-field">
              <label>描述（可选）</label>
              <input v-model="form.description" :disabled="!isEditingMainSkillFile" class="modal-input" data-testid="skills-editor-description-input" placeholder="一句话描述" />
            </div>
            <div class="skills-field">
              <label>摘要（注入内容）</label>
              <textarea v-model="form.summary" :disabled="!isEditingMainSkillFile" class="modal-input" data-testid="skills-editor-summary-input" rows="3" placeholder="用于运行时注入的摘要，建议 1-3 句"></textarea>
              <div class="skills-inline-tip">摘要来源：{{ summarySourceLabel }}</div>
            </div>
            <div class="skills-field">
              <label>保存范围</label>
              <div class="skills-segmented skills-editor-scope" data-testid="skills-editor-scope-group">
                <label class="skills-segmented-item" :class="{ active: form.scope === 'project', disabled: !isEditingMainSkillFile }">
                  <input v-model="form.scope" data-testid="skills-editor-scope-project" type="radio" value="project" :disabled="!isEditingMainSkillFile" />
                  <span class="skills-scope-dot skills-scope-dot-project" aria-hidden="true"></span>
                  <span>project（当前 cwd）</span>
                </label>
                <label class="skills-segmented-item" :class="{ active: form.scope === 'system', disabled: !isEditingMainSkillFile }">
                  <input v-model="form.scope" data-testid="skills-editor-scope-system" type="radio" value="system" :disabled="!isEditingMainSkillFile" />
                  <span class="skills-scope-dot skills-scope-dot-system" aria-hidden="true"></span>
                  <span>system（全局共享）</span>
                </label>
              </div>
              <div class="skills-inline-tip">project → &lt;cwd&gt;/.agent/skills；system → ~/.multi-agent/skills。</div>
            </div>
            <div class="skills-field two-col">
              <div>
                <label>触发词（逗号分隔）</label>
                <input v-model="form.triggerWordsText" :disabled="!isEditingMainSkillFile" class="modal-input" data-testid="skills-editor-trigger-input" placeholder="api, golang, backend" />
              </div>
              <div>
                <label>强制词（逗号分隔）</label>
                <input v-model="form.forceWordsText" :disabled="!isEditingMainSkillFile" class="modal-input" data-testid="skills-editor-force-input" placeholder="紧急, 必须, 强制" />
              </div>
            </div>
            <div v-if="skillFiles.length > 0" class="skills-field">
              <label>子文件列表（点击切换编辑）</label>
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
                  <span v-if="file.isMain" class="skills-subfile-main-tag">主文件</span>
                </button>
              </div>
              <div class="skills-inline-tip">保存时会写回当前选中的文件。</div>
            </div>
            <div class="skills-field skills-field-body">
              <div class="skills-body-head">
                <label>{{ isEditingMainSkillFile ? 'SKILL 内容（默认自动解析 MD，可手动编辑）' : '子文件内容（原样编辑）' }}</label>
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
                placeholder="输入技能正文 Markdown"
                @focus="onBodyFocus"
                @blur="onBodyBlur"
              ></textarea>
              <div class="skills-inline-tip">点击“编辑正文”进入放大编辑区；切回“预览正文”查看 Markdown 渲染效果。</div>
              <div v-if="!isEditingMainSkillFile" class="skills-inline-tip">当前为子文件编辑模式：名称、摘要、触发词只在 SKILL.md 保存。</div>
              <div v-if="sourcePath" class="skills-inline-tip">来源文件：{{ sourcePath }}</div>
            </div>
            <div class="skills-actions-row" data-testid="skills-editor-actions">
              <button class="btn btn-ghost" data-testid="skills-editor-cancel-button" @click="closeEditor">取消</button>
              <button class="btn btn-primary skills-save-btn" data-testid="skills-save-button" :disabled="saving" @click="onSaveSkill">
                {{ saving ? '保存中...' : (isEditingMainSkillFile ? '保存 Skill' : '保存子文件') }}
              </button>
            </div>
          </div>
        </div>
      </div>
    </section>
  `,
};
