// @ts-nocheck

export const SECTION_FRIENDLY_NAMES = Object.freeze({
  identity: '身份设定',
  principles: '执行原则',
  workflow: '工作流程',
  tool_prefs: '工具偏好',
  output_style: '输出风格',
  safety: '安全边界',
  memory: '记忆规则',
  recall_catalog: '知识目录',
  available_experts: '可用专家',
});

const TRIGGER_TYPES = new Set(['always', 'keyword', 'recall']);
const RECALL_TOPIC_PATTERN = /^[a-z0-9]+(?:-[a-z0-9]+)*$/;

export function resolveProjectCwd(projectStore, windowCwd = '') {
  const active = (projectStore?.state?.active || '').toString().trim();
  if (active && active !== '.') return active;
  const fallback = (windowCwd || '').toString().trim();
  return fallback === '.' ? '' : fallback;
}

export function resolveReadonlyFallbackCwd(props) {
  const projectCwd = resolveProjectCwd(props?.projectStore, props?.windowCwd);
  if (projectCwd) return projectCwd;
  const storeCwd = (props?.projectStore?.state?.cwd || '').toString().trim();
  if (storeCwd && storeCwd !== '.') return storeCwd;
  const threadCwd = (props?.threadStore?.state?.cwd || '').toString().trim();
  if (threadCwd && threadCwd !== '.') return threadCwd;
  const windowCwd = (props?.windowCwd || '').toString().trim();
  return windowCwd === '.' ? '' : windowCwd;
}

export function withCwd(cwd, payload) {
  return cwd ? { ...payload, cwd } : payload;
}

export function promptAdvancedDebugEnabled() {
  if (typeof window === 'undefined') return false;
  if (window.__SUPER_DOLPHIN_PROMPT_DEBUG__ === true) return true;
  try {
    return window.localStorage?.getItem('super-dolphin.promptDebug') === '1';
  } catch {
    return false;
  }
}

export const PROMPT_ASSET_TABS = [
  { key: 'all', label: '全部', icon: '📂' },
  { key: 'expert', label: '专家能力', icon: '🧠' },
  { key: 'recall', label: '参考资料', icon: '📚' },
  { key: 'default_rule', label: '默认规则', icon: '📌' },
  { key: 'pending', label: '待确认', icon: '⏳' },
];

export const PROMPT_SCOPE_FILTERS = [
  { key: 'all', label: '全部范围' },
  { key: 'project', label: '这个项目' },
  { key: 'global', label: '全局可用' },
];

export const PROMPT_STATUS_FILTERS = [
  { key: 'all', label: '全部状态' },
  { key: 'enabled', label: '启用中' },
  { key: 'disabled', label: '已停用' },
];

export function promptAssetBucket(card) {
  if (card?.isPendingDraft) return 'pending';
  const kind = (card?.assetType || '').toString();
  if (kind === 'recall' || kind === 'default_rule') return kind;
  return 'expert';
}

export function canForceLaunchPrompt(card) {
  return promptAssetBucket(card) === 'expert' && card?.enabled !== false && !card?.isPendingDraft;
}

export function promptAssetCounts(cards) {
  const counts = { all: cards.length, expert: 0, recall: 0, default_rule: 0, pending: 0 };
  for (const card of cards) {
    const bucket = promptAssetBucket(card);
    counts[bucket] = (counts[bucket] || 0) + 1;
  }
  return counts;
}

function scopeFilterMatches(card, scopeFilter) {
  if (scopeFilter === 'all') return true;
  const scope = card?.scope === 'global' ? 'global' : 'project';
  return scope === scopeFilter;
}

function statusFilterMatches(card, statusFilter) {
  if (statusFilter === 'all') return true;
  if (card?.isPendingDraft) return false;
  const enabled = card?.enabled !== false;
  return statusFilter === 'enabled' ? enabled : !enabled;
}

export function filterPromptCards(cards, activeTab, scopeFilter, statusFilter) {
  return cards.filter(card => {
    if (activeTab !== 'all' && promptAssetBucket(card) !== activeTab) return false;
    if (!scopeFilterMatches(card, scopeFilter)) return false;
    if (activeTab === 'pending') return true;
    return statusFilterMatches(card, statusFilter);
  });
}

export function emptyStateCopy(isFallback, tab) {
  if (isFallback) return '当前为只读降级；待后端恢复后会自动恢复。';
  if (tab === 'pending') return '暂无待确认内容。拖入文件或描述需求后，AI 整理的草稿会出现在这里。';
  if (tab === 'expert') return '点击“添加给 AI 的内容”，教 AI 一种可复用能力。';
  if (tab === 'recall') return '点击“添加给 AI 的内容”，给 AI 一份需要时可查阅的资料。';
  if (tab === 'default_rule') return '点击“添加给 AI 的内容”，设定一条默认遵守的规则。';
  return '点击“添加给 AI 的内容”开始创建。';
}

export function editorTitleCopy(isFallback, mode) {
  if (isFallback) return '查看提示词';
  if (mode === 'create') return '新建提示词';
  return '编辑提示词';
}

export function saveButtonCopy(isFallback, isSaving) {
  if (isFallback) return '只读模式';
  if (isSaving) return '保存中...';
  return '保存';
}

export function promptWritePayloadFromForm(form, activeTabValue, name) {
  const payload = {
    id: form.id || '',
    name,
    description: form.description || '',
    agentType: form.agentKey || 'main',
    priority: Number.isFinite(Number(form.priority)) ? Number(form.priority) : 0,
    when_to_use: form.whenToUse || '',
    tags: Array.isArray(form.tags) ? [...form.tags] : [],
    enabled: form.enabled !== false,
    scope: form.scope === 'global' ? 'global' : 'project',
  };
  const content = (form.content || '').toString();
  const originalContent = (form.originalContent || '').toString();
  if (content !== originalContent) payload.content = content;
  return payload;
}

function generateId() {
  return Date.now().toString(36) + '-' + Math.random().toString(36).slice(2, 7);
}

function normalizeFallbackErrorToken(value) {
  return (value == null ? '' : String(value)).trim().toLowerCase().replace(/[\s-]+/g, '_');
}

export function isReadonlyFallbackListError(error) {
  if (!error || typeof error !== 'object') return false;
  const status = Number(error.status ?? error.statusCode);
  if (status === 404) return true;
  if (error.code === -32601 || Number(error.code) === -32601) return true;
  const normalizedName = normalizeFallbackErrorToken(error.name);
  if (normalizedName === 'method_not_found' || normalizedName === 'notfounderror') return true;
  const normalizedCode = normalizeFallbackErrorToken(error.code);
  return normalizedCode === 'method_not_found';
}

function promptAssetType(raw, tags) {
  if (tags.includes('intent:recall')) return 'recall';
  if (tags.includes('intent:default_rule') || raw?.agent_key === 'default_rule' || raw?.agentType === 'default_rule') return 'default_rule';
  if (tags.includes('intent:expert')) return 'expert';
  return 'expert';
}

function promptAssetScope(raw) {
  return (raw?.scope || raw?.Scope || '').toString().trim().toLowerCase() === 'global'
    ? 'global'
    : 'project';
}

function promptPreviewText(item) {
  return item.content || item.whenToUse || item.description || '已保存，AI 会在相关场景中使用';
}

function normalizePromptItem(raw, agentType) {
  const rawTags = (() => {
    try {
      const t = raw.tags || raw.Tags;
      return Array.isArray(t) ? t : JSON.parse(t || '[]');
    } catch { return []; }
  })();
  const item = {
    id: (raw?.id || raw?.prompt_key || generateId()).toString(),
    name: (raw?.name || raw?.title || raw?.prompt_key || '').toString(),
    content: (raw?.content || raw?.prompt_text || raw?.hint || '').toString(),
    description: (raw?.description || '').toString(),
    whenToUse: (raw?.when_to_use || raw?.whenToUse || '').toString(),
    agentType: (raw?.agentType || raw?.agent_key || agentType || 'main').toString(),
    isDefault: Boolean(raw?.isDefault),
    createdAt: (raw?.createdAt || raw?.created_at || '').toString(),
    match_when: raw?.match_when ?? null,
    priority: Number.isFinite(Number(raw?.priority)) ? Number(raw.priority) : 0,
    enabled: raw?.enabled === undefined ? true : Boolean(raw.enabled),
    scope: promptAssetScope(raw),
    state: (raw?.state || '').toString(),
    draftKey: (raw?.draft_key || raw?.draftKey || '').toString(),
    draftStatus: (raw?.draft_status || raw?.draftStatus || '').toString(),
    card: raw?.card && typeof raw.card === 'object' ? raw.card : null,
    issues: Array.isArray(raw?.issues) ? raw.issues : [],
  };
  item.assetType = promptAssetType(raw, rawTags);
  item.isPendingDraft = item.state === 'pending_confirm' || Boolean(item.draftKey && item.draftStatus === 'ready_to_save');
  item.preview = promptPreviewText(item);
  item.tags = rawTags.filter(t => typeof t === 'string' && t !== 'scope.global' && !t.startsWith('scope.cwd:') && !t.startsWith('scope://') && !t.startsWith('intent:'));
  return item;
}

export function normalizePromptList(items) {
  return Array.isArray(items) ? items.map(item => normalizePromptItem(item)) : [];
}

export function normalizeFallbackPromptList(items) {
  if (!Array.isArray(items)) return null;
  const normalized = items.map(item => normalizePromptItem(item)).filter(item => item.name || item.content);
  if (items.length > 0 && normalized.length === 0) return null;
  return normalized;
}

export function editButtonCopy(item, fallbackMode) {
  if (item?.isPendingDraft) return '待确认';
  return fallbackMode ? '查看' : '编辑';
}

export function normalizeTriggerType(value) {
  const triggerType = (value || '').toString().trim().toLowerCase();
  return TRIGGER_TYPES.has(triggerType) ? triggerType : 'always';
}

export function validRecallTopicName(topic) {
  const text = (topic || '').toString().trim();
  return text.length > 0 && text.length < 64 && RECALL_TOPIC_PATTERN.test(text);
}

export function sectionDisplayName(section) {
  const key = (section?.section_key || '').toString().trim();
  const base = SECTION_FRIENDLY_NAMES[key] || '其他段落';
  return normalizeTriggerType(section?.trigger_type) === 'recall'
    ? `${base} · Recall`
    : base;
}

export function sectionSummary(body, max = 120) {
  const text = (body || '').toString().replace(/\s+/g, ' ').trim();
  if (!text) return '无内容';
  return text.length > max ? text.slice(0, max) + '…' : text;
}
