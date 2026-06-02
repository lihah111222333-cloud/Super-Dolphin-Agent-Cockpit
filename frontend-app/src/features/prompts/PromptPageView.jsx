import React, { useCallback, useEffect, useMemo, useState } from 'react';
import { CheckCircle2, Copy, File, FileText, Plus, RefreshCw, X } from 'lucide-react';
import {
  commitPromptIntent,
  deletePrompt,
  discardPromptIntent,
  draftPromptIntent,
  getDashboardPrompts,
  getPreference,
  getPrompt,
  listPromptAssets,
  setPreference,
  writePrompt,
} from '../../shared/api/backendApi.js';

const ACTIVE_PROMPT_PREF_KEY = 'settings.activePromptKey';

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

function textValue(value) {
  return value === null || value === undefined ? '' : value.toString().trim();
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
  } catch {
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
  } catch {
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

function readonlyFallbackError(error) {
  const status = Number(error?.status ?? error?.statusCode);
  if (status === 404) return true;
  if (error?.code === -32601 || Number(error?.code) === -32601) return true;
  const name = textValue(error?.name).toLowerCase();
  const code = textValue(error?.code).toLowerCase();
  return name === 'notfounderror' || name === 'method_not_found' || code === 'method_not_found';
}

function wordListFromText(value) {
  return textValue(value)
    .split(/[，,;；\n]/)
    .map(textValue)
    .filter(Boolean)
    .filter((word, index, list) => list.findIndex((item) => item.toLowerCase() === word.toLowerCase()) === index);
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
  return {
    draftKey: firstText(raw.draft_key, raw.draftKey),
    kind: firstText(raw.inferred_kind, raw.inferredKind, raw.kind, card.kind, meta.inferredKind, fallbackKind),
    scope: firstText(raw.scope, card.scope, 'project'),
    status: firstText(raw.status, 'review'),
    title: firstText(card.title, raw.title, '未命名草稿'),
    summary: firstText(card.summary, raw.description),
    output: firstText(card.output, card.recall_body, card.default_rule_body, raw.content),
    hitExamples: Array.isArray(card.hit_examples) ? card.hit_examples : [],
    missExamples: Array.isArray(card.miss_examples) ? card.miss_examples : [],
    card,
    issues: Array.isArray(raw.issues) ? raw.issues : [],
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
  return `${prefix}：${message}`;
}

export function PromptPageView({ projectPath }) {
  const cwd = textValue(projectPath);
  const [items, setItems] = useState([]);
  const [activeTab, setActiveTab] = useState('all');
  const [scopeFilter, setScopeFilter] = useState('all');
  const [statusFilter, setStatusFilter] = useState('all');
  const [activePromptId, setActivePromptId] = useState('');
  const [loading, setLoading] = useState(false);
  const [fallbackMode, setFallbackMode] = useState(false);
  const [notice, setNotice] = useState('');
  const [error, setError] = useState('');
  const [actioning, setActioning] = useState('');
  const [editorOpen, setEditorOpen] = useState(false);
  const [saving, setSaving] = useState(false);
  const [form, setForm] = useState(emptyPromptForm);
  const [wizardOpen, setWizardOpen] = useState(false);
  const [wizardDraft, setWizardDraft] = useState(null);

  const refreshPrompts = useCallback(async () => {
    if (!cwd || cwd === '.' || cwd === '未选择项目') {
      setItems([]);
      setFallbackMode(true);
      setError('当前作用域未确定，无法加载提示词');
      return [];
    }
    setLoading(true);
    setError('');
    try {
      const response = await listPromptAssets({ cwd });
      const next = normalizePromptList(response);
      setItems(next);
      setFallbackMode(false);
      return next;
    } catch (err) {
      if (readonlyFallbackError(err)) {
        setFallbackMode(true);
        const fallback = await getDashboardPrompts({ cwd });
        const next = normalizePromptList(fallback);
        setItems(next);
        return next;
      }
      setItems([]);
      setError(noticeText(err, '加载提示词失败'));
      return [];
    } finally {
      setLoading(false);
    }
  }, [cwd]);

  const refreshActivePrompt = useCallback(async () => {
    if (!cwd || cwd === '.' || cwd === '未选择项目') {
      setActivePromptId('');
      return '';
    }
    try {
      const value = await getPreference({ cwd, key: ACTIVE_PROMPT_PREF_KEY });
      const next = typeof value === 'string' ? value.trim() : '';
      setActivePromptId(next);
      return next;
    } catch {
      setActivePromptId('');
      return '';
    }
  }, [cwd]);

  useEffect(() => {
    let cancelled = false;
    refreshPrompts().then((nextItems) => {
      if (cancelled) return;
      refreshActivePrompt().then((activeId) => {
        if (cancelled || !activeId || nextItems.length === 0) return;
        const valid = nextItems.some((item) => item.id === activeId && canForceLaunchPrompt(item));
        if (!valid) setActivePromptId('');
      });
    });
    return () => {
      cancelled = true;
    };
  }, [refreshActivePrompt, refreshPrompts]);

  const counts = useMemo(() => promptCounts(items), [items]);
  const visibleItems = useMemo(
    () => filterPrompts(items, activeTab, scopeFilter, statusFilter),
    [activeTab, items, scopeFilter, statusFilter],
  );

  const switchTab = (key) => {
    setActiveTab(key);
    setNotice('');
  };

  const openCreate = () => {
    if (fallbackMode) {
      setNotice('当前为只读降级，暂不支持新建');
      return;
    }
    setWizardDraft(null);
    setWizardOpen(true);
    setNotice('');
  };

  const openEdit = (item) => {
    setForm(promptFormFromItem(item));
    setEditorOpen(true);
    setNotice('');
  };

  const copyPrompt = async (item) => {
    if (item.isPendingDraft) {
      setNotice('这条草稿还在待确认，确认保存后才能复制内容');
      return;
    }
    try {
      const response = await getPrompt({ cwd, id: item.id });
      const text = firstText(response?.prompt?.content, response?.prompt?.prompt_text, item.content).trim();
      if (!text) {
        setNotice('暂无可复制内容');
        return;
      }
      if (!navigator?.clipboard?.writeText) {
        setNotice('复制失败：当前环境不支持剪贴板');
        return;
      }
      await navigator.clipboard.writeText(text);
      setNotice('已复制提示词内容');
    } catch (err) {
      setNotice(noticeText(err, '复制失败'));
    }
  };

  const savePrompt = async () => {
    const name = textValue(form.name);
    if (!name) {
      setNotice('请填写提示词名称');
      return;
    }
    setSaving(true);
    try {
      await writePrompt({
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
      });
      await refreshPrompts();
      setEditorOpen(false);
      setNotice(`提示词已保存：${name}`);
    } catch (err) {
      setNotice(noticeText(err, '保存失败'));
    } finally {
      setSaving(false);
    }
  };

  const removePrompt = async (item) => {
    if (fallbackMode) {
      setNotice('当前为只读降级，暂不支持删除');
      return;
    }
    if (!item.id || actioning) return;
    setActioning(`delete:${item.id}`);
    try {
      await deletePrompt({ cwd, id: item.id, scope: item.scope === 'global' ? 'global' : 'project' });
      await refreshPrompts();
      setNotice(`已删除：${item.name}`);
    } catch (err) {
      setNotice(noticeText(err, '删除失败'));
    } finally {
      setActioning('');
    }
  };

  const setLaunchPrompt = async (item) => {
    if (!canForceLaunchPrompt(item) || actioning) return;
    setActioning(`launch:${item.id}`);
    try {
      await setPreference({ cwd, key: ACTIVE_PROMPT_PREF_KEY, value: item.id });
      setActivePromptId(item.id);
      setNotice(`已设为强制使用：${item.name}`);
    } catch (err) {
      setNotice(noticeText(err, '设置强制使用失败'));
    } finally {
      setActioning('');
    }
  };

  const clearLaunchPrompt = async () => {
    if (actioning) return;
    setActioning('launch:clear');
    try {
      await setPreference({ cwd, key: ACTIVE_PROMPT_PREF_KEY, value: '' });
      setActivePromptId('');
      setNotice('已取消强制使用，新对话将使用默认路由');
    } catch (err) {
      setNotice(noticeText(err, '取消强制使用失败'));
    } finally {
      setActioning('');
    }
  };

  const continuePendingDraft = (item) => {
    setWizardDraft(pendingDraftFromItem(item));
    setWizardOpen(true);
    setNotice('');
  };

  const discardDraft = async (item) => {
    const draftKey = item.draftKey || item.id;
    if (!draftKey || actioning) return;
    setActioning(`discard:${draftKey}`);
    try {
      await discardPromptIntent({ cwd, draftKey });
      await refreshPrompts();
      setNotice(`已丢弃：${item.name}`);
    } catch (err) {
      setNotice(noticeText(err, '丢弃失败'));
    } finally {
      setActioning('');
    }
  };

  const handleWizardSaved = async () => {
    setWizardOpen(false);
    setWizardDraft(null);
    await refreshPrompts();
    setNotice('已保存，可在新对话中被 AI 发现和使用');
  };

  return (
    <section className="console-page prompt-page">
      <PageHeader title="AI 能力与资料" projectPath={cwd} onRefresh={refreshPrompts} loading={loading} />
      <PromptTabs tabs={PROMPT_TABS} activeTab={activeTab} counts={counts} disabled={fallbackMode} onSwitch={switchTab} />
      <div className="prompt-filter-row">
        <PromptSegment title="范围" items={PROMPT_SCOPE_FILTERS} value={scopeFilter} disabled={fallbackMode} onChange={setScopeFilter} />
        <PromptSegment title="状态" items={PROMPT_STATUS_FILTERS} value={statusFilter} disabled={fallbackMode} onChange={setStatusFilter} />
      </div>
      <div className="prompt-toolbar">
        <button type="button" onClick={openCreate} disabled={fallbackMode || !cwd}>
          <Plus size={15} /> + 添加给 AI 的内容
        </button>
        <button type="button" className="ghost" onClick={refreshPrompts} disabled={loading}>
          <RefreshCw size={15} /> {loading ? '加载中...' : '刷新'}
        </button>
      </div>
      {fallbackMode ? <div className="prompt-notice warn">prompt-assets/list 暂不可用，页面已切换为只读模式。</div> : null}
      {error ? <div className="prompt-notice error">{error}</div> : null}
      {loading ? <div className="prompt-loading">加载中...</div> : null}
      {!loading && visibleItems.length === 0 ? (
        <div className="empty-state prompt-empty">
          <File size={30} />
          <h3>暂无内容</h3>
          <p>{activeTab === 'pending' ? '暂无待确认内容。' : '点击“添加给 AI 的内容”开始创建。'}</p>
        </div>
      ) : null}
      {!loading && visibleItems.length > 0 ? (
        <div className="prompt-card-grid">
          {visibleItems.map((item, index) => (
            <PromptCard
              key={item.id || index}
              item={item}
              active={activePromptId === item.id && canForceLaunchPrompt(item)}
              actioning={actioning}
              fallbackMode={fallbackMode}
              onEdit={openEdit}
              onCopy={copyPrompt}
              onDelete={removePrompt}
              onSetLaunch={setLaunchPrompt}
              onClearLaunch={clearLaunchPrompt}
              onContinueDraft={continuePendingDraft}
              onDiscardDraft={discardDraft}
            />
          ))}
        </div>
      ) : null}
      {notice && !editorOpen && !wizardOpen ? <div className="prompt-notice">{notice}</div> : null}
      {editorOpen ? (
        <PromptEditorModal
          form={form}
          notice={notice}
          saving={saving}
          onChange={setForm}
          onClose={() => {
            setEditorOpen(false);
            setNotice('');
          }}
          onSave={savePrompt}
        />
      ) : null}
      {wizardOpen ? (
        <PromptIntentWizardModal
          cwd={cwd}
          initialDraft={wizardDraft}
          onClose={() => {
            setWizardOpen(false);
            setWizardDraft(null);
          }}
          onSaved={handleWizardSaved}
        />
      ) : null}
    </section>
  );
}

function PageHeader({ title, projectPath, onRefresh, loading }) {
  return (
    <header className="prompt-header">
      <div>
        <h1><FileText size={25} /> {title}</h1>
        <p title={projectPath}>当前项目：{projectPath || '未知'}</p>
      </div>
      <button type="button" onClick={onRefresh} disabled={loading} aria-label="刷新提示词">
        <RefreshCw size={16} />
      </button>
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

function PromptCard(props) {
  const {
    item, active, actioning, fallbackMode, onEdit, onCopy, onDelete, onSetLaunch, onClearLaunch, onContinueDraft, onDiscardDraft,
  } = props;
  const bucket = promptBucket(item);
  return (
    <article className={`prompt-card ${item.enabled === false && !item.isPendingDraft ? 'disabled' : ''} ${item.isPendingDraft ? 'pending' : ''}`}>
      <div className="prompt-card-head">
        <h3>{item.name || '未命名'}</h3>
        <div className="prompt-badges">
          <span>{bucket === 'expert' ? '专家能力' : bucket === 'recall' ? '参考资料' : bucket === 'default_rule' ? '默认规则' : '待确认'}</span>
          {item.scope === 'global' ? <span>全局可用</span> : null}
          {item.isPendingDraft ? <span>待确认</span> : null}
          {item.enabled === false && !item.isPendingDraft ? <span>已停用</span> : null}
          {active ? <span className="active">强制中</span> : null}
        </div>
      </div>
      {item.description ? <p className="prompt-card-desc">{item.description}</p> : null}
      {item.tags.length > 0 ? (
        <div className="prompt-tag-row">
          {item.tags.map((tag) => <span key={tag}>{tag}</span>)}
        </div>
      ) : null}
      <p className="prompt-card-preview">{trunc(item.preview)}</p>
      <div className="prompt-card-actions">
        {item.isPendingDraft ? (
          <>
            <button type="button" onClick={() => onContinueDraft(item)}>继续确认</button>
            <button type="button" className="ghost danger" disabled={actioning === `discard:${item.draftKey || item.id}`} onClick={() => onDiscardDraft(item)}>
              {actioning === `discard:${item.draftKey || item.id}` ? '丢弃中...' : '丢弃'}
            </button>
          </>
        ) : (
          <>
            <button type="button" onClick={() => onEdit(item)}>{fallbackMode ? '查看' : '编辑'}</button>
            <button type="button" className="ghost" onClick={() => onCopy(item)}><Copy size={14} /> 复制</button>
            {active ? (
              <button type="button" className="ghost" disabled={actioning === 'launch:clear'} onClick={onClearLaunch}>取消强制</button>
            ) : canForceLaunchPrompt(item) ? (
              <button type="button" className="ghost" disabled={Boolean(actioning)} onClick={() => onSetLaunch(item)}>强制使用</button>
            ) : null}
            <button type="button" className="ghost danger" disabled={fallbackMode || actioning === `delete:${item.id}`} onClick={() => onDelete(item)}>
              {actioning === `delete:${item.id}` ? '删除中...' : '删除'}
            </button>
          </>
        )}
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
    <div className="modal-overlay prompt-modal-overlay" onClick={onClose}>
      <section className="modal-box prompt-editor-modal" role="dialog" aria-modal="true" aria-label="编辑提示词" onClick={(event) => event.stopPropagation()}>
        <header>
          <div>
            <h2>编辑提示词</h2>
            <p>{scopeLabel}</p>
          </div>
          <button type="button" onClick={onClose} aria-label="关闭编辑器"><X size={16} /></button>
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
          <button type="button" className="ghost" onClick={onClose}>取消</button>
          <button type="button" onClick={onSave} disabled={saving}>{saving ? '保存中...' : '保存'}</button>
        </footer>
      </section>
    </div>
  );
}

function PromptIntentWizardModal({ cwd, initialDraft, onClose, onSaved }) {
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
    setWorking('draft');
    setNotice('');
    try {
      const response = await draftPromptIntent({
        cwd,
        kind,
        rawInput: text,
        sourceType: 'user_input',
        scope,
      });
      setDraft(normalizeDraft(response, kind));
    } catch (err) {
      setNotice(noticeText(err, '生成失败'));
    } finally {
      setWorking('');
    }
  };

  const commitDraft = async () => {
    if (!draft?.draftKey) return;
    setWorking('commit');
    setNotice('');
    try {
      await commitPromptIntent({ cwd, draftKey: draft.draftKey, scope: draft.scope });
      await onSaved();
    } catch (err) {
      setNotice(noticeText(err, '保存失败'));
    } finally {
      setWorking('');
    }
  };

  return (
    <div className="modal-overlay prompt-modal-overlay" onClick={onClose}>
      <section className="modal-box prompt-wizard-modal" role="dialog" aria-modal="true" aria-label="添加给 AI 的内容" onClick={(event) => event.stopPropagation()}>
        <header>
          <div>
            <h2>添加给 AI 的内容</h2>
            <p>{cwd || '未知'}</p>
          </div>
          <button type="button" onClick={onClose}>关闭</button>
        </header>
        <div className="prompt-kind-tabs" role="tablist" aria-label="内容类型">
          {PROMPT_KIND_OPTIONS.map((option) => (
            <button key={option.key} type="button" role="tab" aria-selected={kind === option.key} className={kind === option.key ? 'active' : ''} onClick={() => setKind(option.key)}>
              {option.label}
            </button>
          ))}
        </div>
        <div className="prompt-scope-choice" role="group" aria-label="草稿范围">
          <button type="button" className={scope !== 'global' ? 'active' : ''} onClick={() => setScope('project')}>这个项目</button>
          <button type="button" className={scope === 'global' ? 'active' : ''} onClick={() => setScope('global')}>全局可用</button>
        </div>
        <label className="prompt-wizard-input">
          写下希望 AI 记住或使用的内容
          <textarea value={rawInput} onChange={(event) => setRawInput(event.target.value)} aria-label="写下希望 AI 记住或使用的内容" />
        </label>
        <button type="button" onClick={runDraft} disabled={working === 'draft'}>{working === 'draft' ? '生成中...' : '帮我生成'}</button>
        {draft ? (
          <article className="prompt-draft-review">
            <div className="prompt-draft-title"><CheckCircle2 size={16} /> {draft.title}</div>
            {draft.summary ? <p>{draft.summary}</p> : null}
            {draft.output ? <pre>{draft.output}</pre> : null}
          </article>
        ) : null}
        {notice ? <div className="prompt-notice error">{notice}</div> : null}
        <footer>
          <button type="button" className="ghost" onClick={onClose}>关闭</button>
          <button type="button" onClick={commitDraft} disabled={!draft?.draftKey || working === 'commit'}>
            {working === 'commit' ? '保存中...' : '确认保存'}
          </button>
        </footer>
      </section>
    </div>
  );
}
