import { useQuery, useQueryClient } from '@tanstack/react-query';
import React, { useCallback, useEffect, useMemo, useState } from 'react';
import { CheckCircle2, File, FileText, Plus, X } from 'lucide-react';
import {
  commitPromptIntent,
  deletePrompt,
  discardPromptIntent,
  draftPromptIntent,
  getPreference,
  listPromptAssets,
  setPreference,
  writePrompt,
} from '../../shared/api/backendApi.js';
import { FocusTrapDialog } from '../../shared/ui/FocusTrapDialog.jsx';

const ACTIVE_PROMPT_PREF_KEY = 'settings.activePromptKey';
const PROMPTS_REQUEST_TIMEOUT_MS = 8000;

const PROMPT_TABS = Object.freeze([
  { key: 'all', label: '全部' },
  { key: 'expert', label: '专家能力' },
  { key: 'recall', label: '参考资料' },
  { key: 'default_rule', label: '默认规则' },
  { key: 'pending', label: '待确认' },
]);

const PROMPT_SCOPE_FILTERS = Object.freeze([
  { key: 'all', label: '全部范围' },
  { key: 'project', label: '这个项目' },
  { key: 'global', label: '全局可用' },
]);

const PROMPT_STATUS_FILTERS = Object.freeze([
  { key: 'all', label: '全部状态' },
  { key: 'enabled', label: '启用中' },
  { key: 'disabled', label: '已停用' },
]);

const PROMPT_KIND_OPTIONS = Object.freeze([
  { key: 'expert', label: '专家能力' },
  { key: 'recall', label: '参考资料' },
  { key: 'default_rule', label: '默认规则' },
]);

const PROMPT_DRAFT_NOT_READY_MESSAGE = '这条内容还需要完善后才能保存，请调整描述后重新生成。';
const PROMPT_DRAFT_REVIEW_MESSAGE = '保存前请先确认提示里的风险。';
const PROMPT_ISSUE_COPY = Object.freeze({
  missing_title: '需要补充一个清晰名称。',
  missing_summary: '需要补充一句简短说明。',
  missing_when_to_use: '需要说明 AI 什么时候会使用它。',
  missing_when_not_to_use: '需要说明哪些问题不适合使用它。',
  missing_workflow: '需要补充 AI 执行时的具体步骤。',
  missing_output: '需要写清楚输出会包含哪些栏目或结构。',
  vague_when_to_use: '适用场景还太泛，需要具体到任务或问题类型。',
  vague_output: '需要写清楚输出会包含哪些栏目或结构。',
  missing_save_boundary: '需要说明保存边界：没有明确保存工具或用户确认时，只能输出建议保存的条目，不能声称已经保存。',
  missing_recall_topic: '资料需要有一个可检索主题。',
  missing_recall_body: '资料正文不能为空。',
  missing_default_rule_body: '默认规则正文不能为空。',
  missing_hit_examples: '需要补充适合使用的例子。',
  missing_miss_examples: '需要补充不适合使用的例子。',
  missing_source_facts: '需要先从原文提取关键要点，再整理成可用内容。',
  missing_source_fact_coverage: '原文里的关键要点没有覆盖完整，建议按缺口重新整理。',
  source_fact_not_applied: '原文里的关键要点没有写入保存内容。',
  external_system_prompt: '这是外部模型或产品的系统提示词，不能直接作为默认规则启用。',
  external_system_prompt_source: '这是外部模型或产品的系统提示词，保存前需要确认来源和用途。',
  identity_pollution: '内容里包含模型或供应商身份声明，不能写入专家能力或默认规则。',
  tool_protocol_pollution: '内容里包含外部工具协议，不能写入专家能力或默认规则。',
  overbroad_scope: '适用范围太宽，建议收窄到具体任务或问题。',
  default_rule_conflict: '和已有默认规则可能重复或冲突，保存前需要确认。',
  project_prompt_duplicate: '当前项目已有相似提示词，建议先确认是否更新已有内容。',
  builtin_prompt_duplicate: '系统已内置相似能力，不需要再保存一份。',
  duplicate_recall_topic: '当前项目已有同名资料主题，请更新已有资料或换一个更具体的主题。',
});

function textValue(value) {
  return value === null || value === undefined ? '' : value.toString().trim();
}

function optionalPromptCwd(value) {
  const cwd = textValue(value);
  return cwd && cwd !== '.' && cwd !== '未选择项目' ? cwd : '';
}

function firstText(...values) {
  for (const value of values) {
    const text = textValue(value);
    if (text) return text;
  }
  return '';
}

function parseTags(value) {
  if (Array.isArray(value)) return value.map(textValue).filter(Boolean);
  if (typeof value !== 'string') return [];
  const text = value.trim();
  if (!text) return [];
  try {
    const parsed = JSON.parse(text);
    return Array.isArray(parsed) ? parsed.map(textValue).filter(Boolean) : [];
  }
  catch {
    return text.split(/[，,;；\n]/).map(textValue).filter(Boolean);
  }
}

function cleanPromptTags(tags) {
  return tags.filter((tag) => (
    !tag.startsWith('intent:')
    && tag !== 'scope.global'
    && !tag.startsWith('scope.cwd:')
    && !tag.startsWith('scope://')
  ));
}

function promptAdvancedDebugEnabled() {
  if (typeof window === 'undefined') return false;
  if (window.__SUPER_DOLPHIN_PROMPT_DEBUG__ === true) return true;
  try {
    return window.localStorage?.getItem('super-dolphin.promptDebug') === '1';
  }
  catch {
    return false;
  }
}

function promptAssetType(raw, tags) {
  const explicit = firstText(raw?.assetType, raw?.asset_type, raw?.kind, raw?.prompt_kind);
  if (explicit === 'recall' || explicit === 'default_rule' || explicit === 'expert') return explicit;
  if (tags.includes('intent:recall')) return 'recall';
  if (tags.includes('intent:default_rule')) return 'default_rule';
  if (firstText(raw?.agent_key, raw?.agentType) === 'default_rule') return 'default_rule';
  return 'expert';
}

function promptAssetScope(raw, tags) {
  const explicit = firstText(raw?.scope, raw?.Scope).toLowerCase();
  if (explicit === 'global' || tags.includes('scope.global')) return 'global';
  return 'project';
}

function promptPreviewText(item) {
  return item.content || item.whenToUse || item.description || '已保存，AI 会在相关场景中使用';
}

function promptTextList(value) {
  return Array.isArray(value) ? value.map(textValue).filter(Boolean) : [];
}

function promptIssueMessage(issue) {
  const code = textValue(issue?.code);
  return PROMPT_ISSUE_COPY[code] || textValue(issue?.message) || code;
}

function normalizePromptIssues(raw) {
  if (!Array.isArray(raw)) return [];
  return raw.map((issue) => ({
    code: textValue(issue?.code),
    severity: textValue(issue?.severity).toLowerCase() === 'block' ? 'block' : 'review',
    message: promptIssueMessage(issue),
  })).filter((issue) => issue.message);
}

function normalizePromptItem(raw = {}, index = 0) {
  const tags = parseTags(raw.tags ?? raw.Tags);
  const id = firstText(raw.id, raw.prompt_key, raw.promptKey, raw.draft_key, raw.draftKey, `prompt-${index}`);
  const assetType = promptAssetType(raw, tags);
  const draftKey = firstText(raw.draft_key, raw.draftKey);
  const draftStatus = firstText(raw.draft_status, raw.draftStatus);
  const state = firstText(raw.state, raw.status);
  const item = {
    id,
    name: firstText(raw.name, raw.title, raw.prompt_key, raw.promptKey, draftKey, '未命名'),
    content: firstText(raw.content, raw.prompt_text, raw.promptText, raw.hint),
    description: firstText(raw.description, raw.summary),
    whenToUse: firstText(raw.when_to_use, raw.whenToUse),
    agentType: firstText(raw.agentType, raw.agent_key, raw.agentKey, 'main'),
    priority: Number.isFinite(Number(raw.priority)) ? Number(raw.priority) : 0,
    enabled: raw.enabled === undefined ? true : Boolean(raw.enabled),
    scope: promptAssetScope(raw, tags),
    tags: cleanPromptTags(tags),
    assetType,
    state,
    draftKey,
    draftStatus,
    card: raw.card && typeof raw.card === 'object' ? raw.card : null,
    issues: Array.isArray(raw.issues) ? raw.issues : [],
    isDefault: Boolean(raw.isDefault || raw.is_default),
  };
  item.isPendingDraft = state === 'pending_confirm' || Boolean(draftKey && draftStatus === 'ready_to_save');
  item.preview = promptPreviewText(item);
  return item;
}

function normalizePromptList(response) {
  const items = Array.isArray(response?.prompts) ? response.prompts : [];
  return items.map(normalizePromptItem).filter((item) => item.id || item.name);
}

function promptBucket(item) {
  if (item.isPendingDraft) return 'pending';
  return item.assetType === 'recall' || item.assetType === 'default_rule' ? item.assetType : 'expert';
}

function canForceLaunchPrompt(item) {
  return promptBucket(item) === 'expert' && item.enabled !== false && !item.isPendingDraft;
}

function promptCounts(items) {
  const counts = { all: items.length, expert: 0, recall: 0, default_rule: 0, pending: 0 };
  items.forEach((item) => {
    const bucket = promptBucket(item);
    counts[bucket] = (counts[bucket] || 0) + 1;
  });
  return counts;
}

function filterPrompts(items, tab, scopeFilter, statusFilter) {
  return items.filter((item) => {
    if (tab !== 'all' && promptBucket(item) !== tab) return false;
    if (scopeFilter !== 'all' && item.scope !== scopeFilter) return false;
    if (tab === 'pending') return true;
    if (statusFilter === 'enabled' && item.enabled === false) return false;
    if (statusFilter === 'disabled' && item.enabled !== false) return false;
    return true;
  });
}

function trunc(value, max = 120) {
  const text = textValue(value);
  if (!text) return '暂无内容';
  return text.length > max ? `${text.slice(0, max)}...` : text;
}

function wordListFromText(value) {
  return (
    textValue(value)
    .split(/[，,;；\n]/)
    .map(textValue)
    .filter(Boolean)
    .filter((word, index, list) => list.findIndex((item) => item.toLowerCase() === word.toLowerCase()) === index)
  );
}

function promptFormFromItem(item) {
  return {
    id: item.id || '',
    name: item.name || '',
    description: item.description || '',
    whenToUse: item.whenToUse || '',
    content: item.content || '',
    originalContent: item.content || '',
    agentType: item.agentType || 'main',
    tagsText: (item.tags || []).join(', '),
    scope: item.scope === 'global' ? 'global' : 'project',
    enabled: item.enabled !== false,
    priority: Number.isFinite(Number(item.priority)) ? Number(item.priority) : 0,
  };
}

function emptyPromptForm() {
  return {
    id: '',
    name: '',
    description: '',
    whenToUse: '',
    content: '',
    originalContent: '',
    agentType: 'main',
    tagsText: '',
    scope: 'project',
    enabled: true,
    priority: 0,
  };
}

function normalizeDraftItem(raw = {}, fallbackKind = 'expert', meta = {}) {
  const card = raw.card && typeof raw.card === 'object' ? raw.card : {};
  const workflow = promptTextList(card.workflow);
  const hitExamples = promptTextList(card.hit_examples || card.hitExamples);
  const missExamples = promptTextList(card.miss_examples || card.missExamples);
  return {
    draftKey: firstText(raw.draft_key, raw.draftKey),
    kind: firstText(raw.inferred_kind, raw.inferredKind, raw.kind, card.kind, meta.inferredKind, fallbackKind),
    scope: firstText(raw.scope, card.scope, 'project'),
    status: firstText(raw.status, 'review'),
    title: firstText(card.title, raw.title, '未命名草稿'),
    summary: firstText(card.summary, raw.description),
    whenToUse: firstText(card.when_to_use, card.whenToUse),
    whenNotToUse: firstText(card.when_not_to_use, card.whenNotToUse),
    workflow,
    saveBoundary: firstText(card.save_boundary, card.saveBoundary),
    output: firstText(card.output, card.recall_body, card.recallBody, card.default_rule_body, card.defaultRuleBody, raw.content),
    hitExamples,
    missExamples,
    card,
    issues: normalizePromptIssues(raw.issues),
  };
}

function normalizeDraft(raw = {}, fallbackKind = 'expert') {
  const meta = {
    inferredKind: firstText(raw.inferred_kind, raw.inferredKind),
  };
  if (Array.isArray(raw.drafts) && raw.drafts.length > 0) {
    const draftOptions = raw.drafts.map((item) => normalizeDraftItem(item, fallbackKind, meta));
    return { ...draftOptions[0], draftOptions };
  }
  return normalizeDraftItem(raw, fallbackKind, meta);
}

function pendingDraftFromItem(item) {
  return normalizeDraft({
    draft_key: item.draftKey || item.id,
    kind: item.assetType || 'expert',
    scope: item.scope || 'project',
    status: item.draftStatus || 'ready_to_save',
    card: item.card || {
      kind: item.assetType || 'expert',
      title: item.name,
      summary: item.description,
      output: item.content,
      hit_examples: [],
      miss_examples: [],
    },
    issues: item.issues || [],
  }, item.assetType || 'expert');
}

function noticeText(error, prefix) {
  const message = error?.message || String(error || '');
  const friendly = promptFriendlyErrorText(message);
  if (friendly) return friendly;
  return `${prefix}：${message}`;
}

function promptFriendlyErrorText(message) {
  const lower = textValue(message).toLowerCase();
  if (lower.includes('prompt intent draft is not ready to save')) {
    return PROMPT_DRAFT_NOT_READY_MESSAGE;
  }
  if (lower.includes('prompt intent draft requires risk confirmation')) {
    return PROMPT_DRAFT_REVIEW_MESSAGE;
  }
  return '';
}

function promptDraftNeedsRevision(draft) {
  const status = textValue(draft?.status).toLowerCase();
  const hasBlockIssue = Array.isArray(draft?.issues) && draft.issues.some((issue) => textValue(issue?.severity).toLowerCase() === 'block');
  return status === 'draft' || status === 'draft_blocked' || status === 'blocked' || hasBlockIssue;
}

function errorMessage(error) {
  if (!error) return '';
  return error?.message || String(error);
}

function withTimeout(promise, timeoutMs, message) {
  let timeoutID;
  const timeout = new Promise((_, reject) => {
    timeoutID = globalThis.setTimeout(() => reject(new Error(message)), timeoutMs);
  });
  return Promise.race([promise, timeout]).finally(() => {
    if (timeoutID) globalThis.clearTimeout(timeoutID);
  });
}

function promptAssetsQueryKey(cwd) {
  return ['dashboard', 'project', cwd, 'prompts'];
}

function activePromptQueryKey(cwd) {
  return ['dashboard', 'project', cwd, 'active-prompt'];
}

async function fetchPromptAssetsSurface(cwd) {
  const response = await withTimeout(
    listPromptAssets({ cwd }),
    PROMPTS_REQUEST_TIMEOUT_MS,
    '提示词列表加载超时，请检查提示词目录或后端状态。',
  );
  return { items: normalizePromptList(response) };
}

async function fetchActivePromptId(cwd) {
  const value = await withTimeout(
    getPreference({ cwd, key: ACTIVE_PROMPT_PREF_KEY }),
    PROMPTS_REQUEST_TIMEOUT_MS,
    '强制提示词状态加载超时，请检查后端状态。',
  );
  return typeof value === 'string' ? value.trim() : '';
}

function promptQueryState(cwd, promptAssetsQuery, activePromptQuery) {
  const items = Array.isArray(promptAssetsQuery.data?.items) ? promptAssetsQuery.data.items : [];
  const hasPromptSnapshot = Array.isArray(promptAssetsQuery.data?.items);
  const promptSyncError = promptAssetsQuery.error ? errorMessage(promptAssetsQuery.error) : '';
  const activePromptSyncError = activePromptQuery.error ? errorMessage(activePromptQuery.error) : '';
  const syncErrorMessage = [promptSyncError, activePromptSyncError].filter(Boolean).join('；');
  return {
    items,
    fallbackMode: Boolean(promptAssetsQuery.data?.fallbackMode),
    activePromptId: textValue(activePromptQuery.data),
    loading: Boolean(cwd) && promptAssetsQuery.isPending && !hasPromptSnapshot,
    syncError: syncErrorMessage && hasPromptSnapshot ? `同步失败，显示的是上次成功的数据：${syncErrorMessage}` : '',
    error: promptSyncError && !hasPromptSnapshot ? noticeText(promptAssetsQuery.error, '加载提示词失败') : '',
  };
}

function usePromptQueries(cwd) {
  const promptAssetsQuery = useQuery({
    queryKey: promptAssetsQueryKey(cwd),
    queryFn: () => fetchPromptAssetsSurface(cwd),
    enabled: Boolean(cwd),
  });
  const activePromptQuery = useQuery({
    queryKey: activePromptQueryKey(cwd),
    queryFn: () => fetchActivePromptId(cwd),
    enabled: Boolean(cwd),
  });
  const state = promptQueryState(cwd, promptAssetsQuery, activePromptQuery);
  return {
    ...state,
    refetchPromptAssets: promptAssetsQuery.refetch,
    refetchActivePrompt: activePromptQuery.refetch,
  };
}

function usePromptRefreshSurface(cwd, queryClient, refetchPromptAssets, refetchActivePrompt) {
  return useCallback(async (options = {}) => {
    const isCancelled = typeof options.isCancelled === 'function' ? options.isCancelled : () => false;
    if (!cwd) return [];
    const [assetResult, activeResult] = await Promise.all([
      refetchPromptAssets(),
      refetchActivePrompt(),
    ]);
    const nextItems = Array.isArray(assetResult.data?.items) ? assetResult.data.items : [];
    if (isCancelled()) return nextItems;
    const nextActiveId = textValue(activeResult.data);
    if (nextActiveId && !nextItems.some((item) => item.id === nextActiveId && canForceLaunchPrompt(item))) {
      queryClient.setQueryData(activePromptQueryKey(cwd), '');
    }
    return nextItems;
  }, [cwd, queryClient, refetchActivePrompt, refetchPromptAssets]);
}

function usePromptRefreshEffects(cwd, activePromptId, items, queryClient, promptRefreshKey, refreshPromptSurface, setNotice) {
  useEffect(() => {
    setNotice('');
  }, [cwd, setNotice]);

  useEffect(() => {
    if (!cwd || !activePromptId || items.length === 0) return;
    if (!items.some((item) => item.id === activePromptId && canForceLaunchPrompt(item))) {
      queryClient.setQueryData(activePromptQueryKey(cwd), '');
    }
  }, [activePromptId, cwd, items, queryClient]);

  useEffect(() => {
    if (promptRefreshKey <= 0) return undefined;
    let cancelled = false;
    void refreshPromptSurface({ isCancelled: () => cancelled });
    return () => {
      cancelled = true;
    };
  }, [promptRefreshKey, refreshPromptSurface]);

  useEffect(() => {
    const runAutoRefresh = () => {
      if (typeof document !== 'undefined' && document.visibilityState === 'hidden') return;
      void refreshPromptSurface();
    };
    const handleVisibilityChange = () => {
      if (typeof document === 'undefined' || document.visibilityState === 'visible') runAutoRefresh();
    };
    window.addEventListener('focus', runAutoRefresh);
    document.addEventListener('visibilitychange', handleVisibilityChange);
    return () => {
      window.removeEventListener('focus', runAutoRefresh);
      document.removeEventListener('visibilitychange', handleVisibilityChange);
    };
  }, [refreshPromptSurface]);
}

function promptWritePayload(cwd, form, name) {
  return {
    cwd,
    id: form.id,
    name,
    description: form.description,
    agentType: form.agentType || 'main',
    priority: Number.isFinite(Number(form.priority)) ? Number(form.priority) : 0,
    when_to_use: form.whenToUse,
    content: form.content,
    tags: wordListFromText(form.tagsText),
    enabled: form.enabled,
    scope: form.scope === 'global' ? 'global' : 'project',
  };
}

async function savePromptForm({ cwd, form, refreshPromptSurface, setEditorOpen, setNotice, setSaving }) {
  const name = textValue(form.name);
  if (!name) {
    setNotice('请填写提示词名称');
    return;
  }
  setSaving(true);
  try {
    await writePrompt(promptWritePayload(cwd, form, name));
    await refreshPromptSurface({ force: true });
    setEditorOpen(false);
    setNotice(`提示词已保存：${name}`);
  }
  catch (err) {
    setNotice(noticeText(err, '保存失败'));
  }
  finally {
    setSaving(false);
  }
}

async function removePromptItem({ cwd, item, refreshPromptSurface, setActioning, setNotice }) {
  setActioning(`delete:${item.id}`);
  try {
    await deletePrompt({ cwd, id: item.id, scope: item.scope === 'global' ? 'global' : 'project' });
    await refreshPromptSurface({ force: true });
    setNotice(`已删除：${item.name}`);
  }
  catch (err) {
    setNotice(noticeText(err, '删除失败'));
  }
  finally {
    setActioning('');
  }
}

async function setLaunchPreference({ cwd, item, queryClient, setActioning, setNotice }) {
  setActioning(`launch:${item.id}`);
  try {
    await setPreference({ cwd, key: ACTIVE_PROMPT_PREF_KEY, value: item.id });
    queryClient.setQueryData(activePromptQueryKey(cwd), item.id);
    setNotice(`已设为强制使用：${item.name}`);
  }
  catch (err) {
    setNotice(noticeText(err, '设置强制使用失败'));
  }
  finally {
    setActioning('');
  }
}

async function clearLaunchPreference({ cwd, queryClient, setActioning, setNotice }) {
  setActioning('launch:clear');
  try {
    await setPreference({ cwd, key: ACTIVE_PROMPT_PREF_KEY, value: '' });
    queryClient.setQueryData(activePromptQueryKey(cwd), '');
    setNotice('已取消强制使用，新对话将使用默认路由');
  }
  catch (err) {
    setNotice(noticeText(err, '取消强制使用失败'));
  }
  finally {
    setActioning('');
  }
}

async function discardPromptDraftItem({ cwd, item, refreshPromptSurface, setActioning, setNotice }) {
  const draftKey = item.draftKey || item.id;
  setActioning(`discard:${draftKey}`);
  try {
    await discardPromptIntent({ cwd, draftKey });
    await refreshPromptSurface({ force: true });
    setNotice(`已丢弃：${item.name}`);
  }
  catch (err) {
    setNotice(noticeText(err, '丢弃失败'));
  }
  finally {
    setActioning('');
  }
}

function usePromptEditorActions(params) {
  const { cwd, fallbackMode, actioning, form, queryClient, refreshPromptSurface, setters } = params;
  const { setActioning, setEditorOpen, setForm, setNotice, setSaving, setWizardDraft, setWizardOpen } = setters;
  return {
    switchTab: (key) => {
      setters.setActiveTab(key);
      setNotice('');
    },
    retryPromptSync: () => {
      void refreshPromptSurface({ force: true });
    },
    openCreate: () => {
      if (fallbackMode) {
        setNotice('当前为只读降级，暂不支持新建');
        return;
      }
      setWizardDraft(null);
      setWizardOpen(true);
      setNotice('');
    },
    openEdit: (item) => {
      setForm(promptFormFromItem(item));
      setEditorOpen(true);
      setNotice('');
    },
    savePrompt: () => savePromptForm({ cwd, form, refreshPromptSurface, setEditorOpen, setNotice, setSaving }),
    removePrompt: (item) => {
      if (fallbackMode) {
        setNotice('当前为只读降级，暂不支持删除');
        return;
      }
      if (!item.id || actioning) return;
      void removePromptItem({ cwd, item, refreshPromptSurface, setActioning, setNotice });
    },
    setLaunchPrompt: (item) => {
      if (!canForceLaunchPrompt(item) || actioning) return;
      void setLaunchPreference({ cwd, item, queryClient, setActioning, setNotice });
    },
    clearLaunchPrompt: () => {
      if (actioning) return;
      void clearLaunchPreference({ cwd, queryClient, setActioning, setNotice });
    },
  };
}

function usePromptDraftActions({ cwd, actioning, refreshPromptSurface, setters }) {
  const { setActioning, setNotice, setWizardDraft, setWizardOpen } = setters;
  return {
    continuePendingDraft: (item) => {
      setWizardDraft(pendingDraftFromItem(item));
      setWizardOpen(true);
      setNotice('');
    },
    discardDraft: (item) => {
      const draftKey = item.draftKey || item.id;
      if (!draftKey || actioning) return;
      void discardPromptDraftItem({ cwd, item, refreshPromptSurface, setActioning, setNotice });
    },
    handleWizardSaved: async () => {
      setWizardOpen(false);
      setWizardDraft(null);
      await refreshPromptSurface({ force: true });
      setNotice('已保存，可在新对话中被 AI 发现和使用');
    },
  };
}

function PromptFilterControls({ scopeFilter, statusFilter, fallbackMode, onScopeChange, onStatusChange }) {
  return (
    <div className="prompt-filter-row">
      <PromptSegment title="范围" items={PROMPT_SCOPE_FILTERS} value={scopeFilter} disabled={fallbackMode} onChange={onScopeChange} />
      <PromptSegment title="状态" items={PROMPT_STATUS_FILTERS} value={statusFilter} disabled={fallbackMode} onChange={onStatusChange} />
    </div>
  );
}

function PromptToolbar({ cwd, fallbackMode, onCreate }) {
  return (
    <div className="prompt-toolbar">
      <button type="button" onClick={onCreate} disabled={fallbackMode || !cwd}>
        <Plus size={15} /> + 添加给 AI 的内容
      </button>
    </div>
  );
}

function PromptStatusMessages({ isProjectPending, fallbackMode, syncError, error, loading, onRetry }) {
  return (
    <>
      {isProjectPending ? <div className="prompt-notice">正在连接本地项目...</div> : null}
      {fallbackMode ? <div className="prompt-notice warn">prompt-assets/list 暂不可用，页面已切换为只读模式。</div> : null}
      {syncError ? <PromptRetryNotice message={syncError} onRetry={onRetry} /> : null}
      {error ? <PromptRetryNotice message={error} onRetry={onRetry} /> : null}
      {loading ? <output className="prompt-loading" aria-live="polite">正在加载提示词...</output> : null}
    </>
  );
}

function PromptRetryNotice({ message, onRetry }) {
  return (
    <div className="prompt-notice error" role="alert">
      <span>{message}</span>
      <button type="button" className="ghost" onClick={onRetry}>重试同步</button>
    </div>
  );
}

function PromptEmptyState({ activeTab }) {
  return (
    <div className="empty-state prompt-empty">
      <File size={30} />
      <h3>暂无内容</h3>
      <p>{activeTab === 'pending' ? '暂无待确认内容。' : '点击“添加给 AI 的内容”开始创建。'}</p>
    </div>
  );
}

function PromptCardsGrid({ visibleItems, activePromptId, actioning, fallbackMode, editorActions, draftActions }) {
  return (
    <div className="prompt-card-grid">
      {visibleItems.map((item, index) => (
        <PromptCard
          key={item.id || index}
          item={item}
          active={activePromptId === item.id && canForceLaunchPrompt(item)}
          actioning={actioning}
          fallbackMode={fallbackMode}
          onEdit={editorActions.openEdit}
          onDelete={editorActions.removePrompt}
          onSetLaunch={editorActions.setLaunchPrompt}
          onClearLaunch={editorActions.clearLaunchPrompt}
          onContinueDraft={draftActions.continuePendingDraft}
          onDiscardDraft={draftActions.discardDraft}
        />
      ))}
    </div>
  );
}

function PromptPageLayout(props) {
  const showEmpty = !props.isProjectPending && !props.loading && !props.showBlockingError && props.visibleItems.length === 0;
  const showCards = !props.isProjectPending && !props.loading && props.visibleItems.length > 0;
  return (
    <section className="console-page prompt-page">
      <PageHeader title="AI 能力与资料" projectPath={props.cwd || props.projectPath} />
      <PromptTabs tabs={PROMPT_TABS} activeTab={props.activeTab} counts={props.counts} disabled={props.fallbackMode} onSwitch={props.editorActions.switchTab} />
      <PromptFilterControls {...props.filters} fallbackMode={props.fallbackMode} />
      <PromptToolbar cwd={props.cwd} fallbackMode={props.fallbackMode} onCreate={props.editorActions.openCreate} />
      <PromptStatusMessages {...props} onRetry={props.editorActions.retryPromptSync} />
      {showEmpty ? <PromptEmptyState activeTab={props.activeTab} /> : null}
      {showCards ? <PromptCardsGrid {...props} /> : null}
      {props.notice && !props.modals.editorOpen && !props.modals.wizardOpen ? <div className="prompt-notice">{props.notice}</div> : null}
      <PromptEditorHost {...props} />
      <PromptWizardHost {...props} />
    </section>
  );
}

function PromptEditorHost({ modals, notice, saving, setters, editorActions }) {
  if (!modals.editorOpen) return null;
  return (
    <PromptEditorModal
      form={modals.form}
      notice={notice}
      saving={saving}
      onChange={setters.setForm}
      onClose={() => {
        setters.setEditorOpen(false);
        setters.setNotice('');
      }}
      onSave={editorActions.savePrompt}
    />
  );
}

function PromptWizardHost({ modals, cwd, resolveLaunchPreferences, setters, draftActions }) {
  if (!modals.wizardOpen) return null;
  return (
    <PromptIntentWizardModal
      cwd={cwd}
      initialDraft={modals.wizardDraft}
      resolveLaunchPreferences={resolveLaunchPreferences}
      onClose={() => {
        setters.setWizardOpen(false);
        setters.setWizardDraft(null);
      }}
      onSaved={draftActions.handleWizardSaved}
    />
  );
}

function usePromptPageState() {
  const [activeTab, setActiveTab] = useState('all');
  const [scopeFilter, setScopeFilter] = useState('all');
  const [statusFilter, setStatusFilter] = useState('all');
  const [notice, setNotice] = useState('');
  const [actioning, setActioning] = useState('');
  const [editorOpen, setEditorOpen] = useState(false);
  const [saving, setSaving] = useState(false);
  const [form, setForm] = useState(emptyPromptForm);
  const [wizardOpen, setWizardOpen] = useState(false);
  const [wizardDraft, setWizardDraft] = useState(null);
  return {
    activeTab,
    actioning,
    form,
    modals: { editorOpen, form, wizardDraft, wizardOpen },
    notice,
    saving,
    scopeFilter,
    statusFilter,
    setters: {
      setActiveTab,
      setActioning,
      setEditorOpen,
      setForm,
      setNotice,
      setSaving,
      setScopeFilter,
      setStatusFilter,
      setWizardDraft,
      setWizardOpen,
    },
  };
}

export function PromptPageView({ projectPath, refreshKey = 0, resolveLaunchPreferences }) {
  const cwd = optionalPromptCwd(projectPath);
  const isProjectPending = !cwd;
  const queryClient = useQueryClient();
  const pageState = usePromptPageState();
  const { activeTab, actioning, form, modals, notice, saving, scopeFilter, statusFilter, setters } = pageState;

  const queryState = usePromptQueries(cwd);
  const { items, fallbackMode, activePromptId, loading, syncError, error } = queryState;
  const refreshPromptSurface = usePromptRefreshSurface(
    cwd,
    queryClient,
    queryState.refetchPromptAssets,
    queryState.refetchActivePrompt,
  );
  usePromptRefreshEffects(cwd, activePromptId, items, queryClient, Number(refreshKey || 0), refreshPromptSurface, setters.setNotice);

  const counts = useMemo(() => promptCounts(items), [items]);
  const visibleItems = useMemo(
    () => filterPrompts(items, activeTab, scopeFilter, statusFilter),
    [activeTab, items, scopeFilter, statusFilter],
  );
  const editorActions = usePromptEditorActions({
    cwd,
    fallbackMode,
    actioning,
    form,
    queryClient,
    refreshPromptSurface,
    setters,
  });
  const draftActions = usePromptDraftActions({ cwd, actioning, refreshPromptSurface, setters });
  const layoutProps = {
    activePromptId,
    activeTab,
    actioning,
    counts,
    cwd,
    draftActions,
    editorActions,
    error,
    fallbackMode,
    filters: {
      scopeFilter,
      statusFilter,
      onScopeChange: setters.setScopeFilter,
      onStatusChange: setters.setStatusFilter,
    },
    isProjectPending,
    loading,
    modals,
    notice,
    projectPath,
    resolveLaunchPreferences,
    saving,
    setters,
    showBlockingError: Boolean(error),
    syncError,
    visibleItems,
  };

  return <PromptPageLayout {...layoutProps} />;
}

function PageHeader({ title, projectPath }) {
  return (
    <header className="prompt-header">
      <div>
        <h1><FileText size={25} /> {title}</h1>
        <p title={projectPath}>当前项目：{projectPath || '未知'}</p>
      </div>
    </header>
  );
}

function PromptTabs({ tabs, activeTab, counts, disabled, onSwitch }) {
  return (
    <div className="prompt-tabs" role="tablist" aria-label="提示词分类">
      {tabs.map((tab) => (
        <button
          key={tab.key}
          type="button"
          role="tab"
          aria-selected={activeTab === tab.key}
          className={activeTab === tab.key ? 'active' : ''}
          disabled={disabled}
          onClick={() => onSwitch(tab.key)}
        >
          {tab.label} {counts[tab.key] || 0}
        </button>
      ))}
    </div>
  );
}

function PromptSegment({ title, items, value, disabled, onChange }) {
  return (
    <div className="prompt-segment">
      <span>{title}</span>
      {items.map((item) => (
        <button
          key={item.key}
          type="button"
          className={value === item.key ? 'active' : ''}
          disabled={disabled}
          onClick={() => onChange(item.key)}
        >
          {item.label}
        </button>
      ))}
    </div>
  );
}

function promptBucketLabel(bucket) {
  if (bucket === 'expert') return '专家能力';
  if (bucket === 'recall') return '参考资料';
  if (bucket === 'default_rule') return '默认规则';
  return '待确认';
}

function PromptBadges({ item, active }) {
  const bucket = promptBucket(item);
  return (
    <div className="prompt-badges">
      <span>{promptBucketLabel(bucket)}</span>
      {item.scope === 'global' ? <span>全局可用</span> : null}
      {item.isPendingDraft ? <span>待确认</span> : null}
      {item.enabled === false && !item.isPendingDraft ? <span>已停用</span> : null}
      {active ? <span className="active">强制中</span> : null}
    </div>
  );
}

function PromptTagRow({ tags }) {
  if (!tags.length) return null;
  return (
    <div className="prompt-tag-row">
      {tags.map((tag) => <span key={tag}>{tag}</span>)}
    </div>
  );
}

function PromptForceAction({ item, active, actioning, onSetLaunch, onClearLaunch }) {
  if (active) {
    return (
      <button type="button" className="ghost" disabled={actioning === 'launch:clear'} onClick={onClearLaunch}>取消强制</button>
    );
  }
  if (!canForceLaunchPrompt(item)) return null;
  return (
    <button type="button" className="ghost" disabled={Boolean(actioning)} onClick={() => onSetLaunch(item)}>强制使用</button>
  );
}

function PromptPendingActions({ item, actioning, onContinueDraft, onDiscardDraft }) {
  const discardKey = item.draftKey || item.id;
  return (
    <>
      <button type="button" onClick={() => onContinueDraft(item)}>继续确认</button>
      <button type="button" className="ghost danger" disabled={actioning === `discard:${discardKey}`} onClick={() => onDiscardDraft(item)}>
        {actioning === `discard:${discardKey}` ? '丢弃中...' : '丢弃'}
      </button>
    </>
  );
}

function PromptSavedActions({ item, active, actioning, fallbackMode, onEdit, onDelete, onSetLaunch, onClearLaunch }) {
  return (
    <>
      <button type="button" onClick={() => onEdit(item)}>{fallbackMode ? '查看' : '编辑'}</button>
      <PromptForceAction item={item} active={active} actioning={actioning} onSetLaunch={onSetLaunch} onClearLaunch={onClearLaunch} />
      <button type="button" className="ghost danger" disabled={fallbackMode || actioning === `delete:${item.id}`} onClick={() => onDelete(item)}>
        {actioning === `delete:${item.id}` ? '删除中...' : '删除'}
      </button>
    </>
  );
}

function PromptCardActions(props) {
  if (props.item.isPendingDraft) {
    return <PromptPendingActions {...props} />;
  }
  return <PromptSavedActions {...props} />;
}

function PromptCard(props) {
  const { item, active } = props;
  return (
    <article className={`prompt-card ${item.enabled === false && !item.isPendingDraft ? 'disabled' : ''} ${item.isPendingDraft ? 'pending' : ''}`}>
      <div className="prompt-card-head">
        <h3>{item.name || '未命名'}</h3>
        <PromptBadges item={item} active={active} />
      </div>
      {item.description ? <p className="prompt-card-desc">{item.description}</p> : null}
      <PromptTagRow tags={item.tags} />
      <p className="prompt-card-preview">{trunc(item.preview)}</p>
      <div className="prompt-card-actions">
        <PromptCardActions {...props} />
      </div>
    </article>
  );
}

function PromptEditorModal({ form, notice, saving, onChange, onClose, onSave }) {
  const update = (key) => (event) => {
    const { type, checked, value } = event.target;
    onChange({ ...form, [key]: type === 'checkbox' ? checked : value });
  };
  const scopeLabel = form.scope === 'global' ? '全局可用' : '这个项目';
  const scopeHint = form.scope === 'global' ? '说明：其他项目也可以使用；当前项目同名内容优先。' : '说明：只在当前项目的对话中使用。';
  const previewText = form.content || form.whenToUse || form.description || '已保存，AI 会在相关场景中使用';
  const advancedDebugAvailable = promptAdvancedDebugEnabled();
  return (
    <FocusTrapDialog
      ariaLabel="编辑提示词"
      className="modal-box prompt-editor-modal"
      overlayClassName="modal-overlay prompt-modal-overlay"
      closeDisabled={saving}
      closeOnOverlayClick
      onClose={onClose}
    >
        <header>
          <div>
            <h2>编辑提示词</h2>
            <p>{scopeLabel}</p>
          </div>
          <button type="button" onClick={onClose} aria-label="关闭编辑器" disabled={saving}><X size={16} /></button>
        </header>
        <div className="prompt-scope-copy">
          <div>可用范围：{scopeLabel}</div>
          <div className="prompt-scope-choice" role="group" aria-label="可用范围">
            <button type="button" className={form.scope !== 'global' ? 'active' : ''} onClick={() => onChange({ ...form, scope: 'project' })}>这个项目</button>
            <button type="button" className={form.scope === 'global' ? 'active' : ''} onClick={() => onChange({ ...form, scope: 'global' })}>全局可用</button>
          </div>
          <div>{scopeHint}</div>
        </div>
        <div className="prompt-editor-grid">
          <label>名称<input value={form.name} onChange={update('name')} aria-label="名称" /></label>
          <label className="wide">一句话描述<input value={form.description} onChange={update('description')} aria-label="一句话描述" /></label>
          <label className="wide">AI 什么时候会使用它<textarea value={form.whenToUse} onChange={update('whenToUse')} aria-label="AI 什么时候会使用它" /></label>
          <label className="wide">AI 使用时怎么做<textarea value={form.content} onChange={update('content')} aria-label="AI 使用时怎么做" /></label>
          <label className="wide">保存后 AI 会看到什么<textarea className="prompt-preview-readonly" value={previewText} aria-label="保存后 AI 会看到什么" readOnly /></label>
          <label className="prompt-check"><input type="checkbox" checked={form.enabled} onChange={update('enabled')} /> 启用状态</label>
        </div>
        {advancedDebugAvailable ? (
          <details className="prompt-advanced-debug">
            <summary>高级调试</summary>
            <div className="prompt-editor-grid prompt-advanced-grid">
              <label>Agent Key<input value={form.agentType} onChange={update('agentType')} aria-label="Agent Key" /></label>
              <label>场景标签<input value={form.tagsText} onChange={update('tagsText')} aria-label="场景标签" /></label>
              <label>排序权重<input type="number" value={form.priority} onChange={update('priority')} aria-label="排序权重" /></label>
            </div>
          </details>
        ) : null}
        {notice ? <div className="prompt-notice">{notice}</div> : null}
        <footer>
          <button type="button" className="ghost" onClick={onClose} disabled={saving}>取消</button>
          <button type="button" onClick={onSave} disabled={saving}>{saving ? '保存中...' : '保存'}</button>
        </footer>
    </FocusTrapDialog>
  );
}

async function buildPromptDraft({ cwd, kind, rawInput, scope, resolveLaunchPreferences }) {
  const launchPreferences = typeof resolveLaunchPreferences === 'function'
    ? await resolveLaunchPreferences(cwd)
    : null;
  return draftPromptIntent({
    cwd,
    kind,
    rawInput,
    sourceType: 'user_input',
    scope,
    provider: textValue(launchPreferences?.modelProvider || launchPreferences?.provider),
    model: textValue(launchPreferences?.model),
    codexModelProvider: textValue(launchPreferences?.config?.codexModelProvider),
  });
}

function PromptKindTabs({ kind, onChange }) {
  return (
    <div className="prompt-kind-tabs" role="tablist" aria-label="内容类型">
      {PROMPT_KIND_OPTIONS.map((option) => (
        <button key={option.key} type="button" role="tab" aria-selected={kind === option.key} className={kind === option.key ? 'active' : ''} onClick={() => onChange(option.key)}>
          {option.label}
        </button>
      ))}
    </div>
  );
}

function PromptScopeChoice({ scope, onChange, ariaLabel = '草稿范围' }) {
  return (
    <div className="prompt-scope-choice" role="group" aria-label={ariaLabel}>
      <button type="button" className={scope !== 'global' ? 'active' : ''} onClick={() => onChange('project')}>这个项目</button>
      <button type="button" className={scope === 'global' ? 'active' : ''} onClick={() => onChange('global')}>全局可用</button>
    </div>
  );
}

function PromptDraftExamples({ draft }) {
  if (!draft.hitExamples.length && !draft.missExamples.length) return null;
  return (
    <div className="prompt-draft-examples">
      {draft.hitExamples.length ? (
        <div>
          <strong>适合的问题</strong>
          <ul>{draft.hitExamples.map((example) => <li key={example}>{example}</li>)}</ul>
        </div>
      ) : null}
      {draft.missExamples.length ? (
        <div>
          <strong>不适合的问题</strong>
          <ul>{draft.missExamples.map((example) => <li key={example}>{example}</li>)}</ul>
        </div>
      ) : null}
    </div>
  );
}

function PromptDraftReview({ draft }) {
  if (!draft) return null;
  return (
    <article className="prompt-draft-review">
      <div className="prompt-draft-title"><CheckCircle2 size={16} /> {draft.title}</div>
      {draft.summary ? <p>{draft.summary}</p> : null}
      {draft.whenToUse ? <p>{draft.whenToUse}</p> : null}
      {draft.whenNotToUse ? <p>{draft.whenNotToUse}</p> : null}
      {draft.workflow?.length ? <ol>{draft.workflow.map((step) => <li key={step}>{step}</li>)}</ol> : null}
      {draft.saveBoundary ? <p><span>保存边界：</span><span>{draft.saveBoundary}</span></p> : null}
      {draft.output ? <pre>{draft.output}</pre> : null}
      <PromptDraftExamples draft={draft} />
      {draft.issues.length ? (
        <div className="prompt-draft-issues">
          {draft.issues.map((issue) => <div key={`${issue.code}:${issue.message}`}>{issue.message}</div>)}
        </div>
      ) : null}
    </article>
  );
}

function PromptWizardNotice({ draftNeedsRevision, notice }) {
  const noticeIsGuidance = notice === PROMPT_DRAFT_NOT_READY_MESSAGE || notice === PROMPT_DRAFT_REVIEW_MESSAGE;
  return (
    <>
      {draftNeedsRevision ? <div className="prompt-notice">{PROMPT_DRAFT_NOT_READY_MESSAGE}</div> : null}
      {notice ? <div className={`prompt-notice${noticeIsGuidance ? '' : ' error'}`}>{notice}</div> : null}
    </>
  );
}

async function runPromptDraftGeneration(params) {
  const { cwd, kind, rawInput, scope, resolveLaunchPreferences, setDraft, setNotice, setWorking } = params;
  setWorking('draft');
  setNotice('');
  try {
    const response = await buildPromptDraft({ cwd, kind, rawInput, scope, resolveLaunchPreferences });
    setDraft(normalizeDraft(response, kind));
  }
  catch (err) {
    setNotice(noticeText(err, '生成失败'));
  }
  finally {
    setWorking('');
  }
}

async function runPromptDraftCommit({ cwd, draft, onSaved, setNotice, setWorking }) {
  setWorking('commit');
  setNotice('');
  try {
    await commitPromptIntent({ cwd, draftKey: draft.draftKey, scope: draft.scope });
    await onSaved();
  }
  catch (err) {
    setNotice(noticeText(err, '保存失败'));
  }
  finally {
    setWorking('');
  }
}

function PromptIntentWizardModal({ cwd, initialDraft, resolveLaunchPreferences, onClose, onSaved }) {
  const [kind, setKind] = useState(initialDraft?.kind || 'expert');
  const [scope, setScope] = useState(initialDraft?.scope || 'project');
  const [rawInput, setRawInput] = useState('');
  const [draft, setDraft] = useState(initialDraft);
  const [notice, setNotice] = useState('');
  const [working, setWorking] = useState('');

  useEffect(() => {
    setKind(initialDraft?.kind || 'expert');
    setScope(initialDraft?.scope || 'project');
    setDraft(initialDraft);
    setRawInput('');
    setNotice('');
  }, [initialDraft]);

  const runDraft = async () => {
    const text = textValue(rawInput);
    if (!text) {
      setNotice('请先写下希望 AI 记住或使用的内容');
      return;
    }
    await runPromptDraftGeneration({ cwd, kind, rawInput: text, scope, resolveLaunchPreferences, setDraft, setNotice, setWorking });
  };

  const commitDraft = async () => {
    if (!draft?.draftKey) return;
    if (promptDraftNeedsRevision(draft)) {
      setNotice(PROMPT_DRAFT_NOT_READY_MESSAGE);
      return;
    }
    await runPromptDraftCommit({ cwd, draft, onSaved, setNotice, setWorking });
  };

  const draftNeedsRevision = promptDraftNeedsRevision(draft);

  return (
    <FocusTrapDialog
      ariaLabel="添加给 AI 的内容"
      className="modal-box prompt-wizard-modal"
      overlayClassName="modal-overlay prompt-modal-overlay"
      closeDisabled={Boolean(working)}
      closeOnOverlayClick
      onClose={onClose}
    >
        <header>
          <div>
            <h2>添加给 AI 的内容</h2>
            <p>{cwd || '未知'}</p>
          </div>
          <button type="button" onClick={onClose} disabled={Boolean(working)}>关闭</button>
        </header>
        <PromptKindTabs kind={kind} onChange={setKind} />
        <PromptScopeChoice scope={scope} onChange={setScope} />
        <label className="prompt-wizard-input">
          写下希望 AI 记住或使用的内容
          <textarea value={rawInput} onChange={(event) => setRawInput(event.target.value)} aria-label="写下希望 AI 记住或使用的内容" />
        </label>
        <button type="button" onClick={runDraft} disabled={working === 'draft'}>{working === 'draft' ? '生成中...' : '帮我生成'}</button>
        <PromptDraftReview draft={draft} />
        <PromptWizardNotice draftNeedsRevision={draftNeedsRevision} notice={notice} />
        <footer>
          <button type="button" className="ghost" onClick={onClose} disabled={Boolean(working)}>关闭</button>
          <button type="button" onClick={commitDraft} disabled={!draft?.draftKey || draftNeedsRevision || working === 'commit'}>
            {working === 'commit' ? '保存中...' : '确认保存'}
          </button>
        </footer>
    </FocusTrapDialog>
  );
}
