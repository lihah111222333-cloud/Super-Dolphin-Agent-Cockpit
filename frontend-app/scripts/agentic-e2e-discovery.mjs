export const BLOCKED_ACTION_KEYWORDS = Object.freeze([
  '发送', '中断', '保存', '应用', '删除', '重置', '移除', '上传', '导入', '导出', '安装',
  'send', 'interrupt', 'save', 'apply', 'delete', 'reset', 'remove', 'upload', 'import', 'export', 'install',
]);

const ALLOWED_ACTION_KEYWORDS = Object.freeze([
  '查询', '搜索', '筛选', '展开', '收起', '打开', '详情',
  'query', 'search', 'filter', 'expand', 'collapse', 'open', 'detail',
]);

const KNOWN_NAVIGATION_ENTRY_LABELS = Object.freeze([
  '链路追踪', 'Settings', '设置', '新对话', '记忆中心', '插件', '插件与技能', '自动化', '定制角色', '共享文件',
]);

export function discoverBusinessFlows(facts = {}) {
  const route = routeFromURL(facts.url);
  const entries = businessEntriesFromDOMSummary(facts.domSummary || [], route);
  const actions = businessActionsFromDOMSummary(facts.domSummary || []);
  return entries.map((entry) => ({
    id: flowID(entry),
    entry,
    page: {
      route,
      title: normalizeString(facts.title),
      heading: firstHeadingText(facts.domSummary || []),
      testIds: unique((facts.domSummary || []).map((item) => normalizeString(item.testId)).filter(Boolean)),
    },
    actions,
    result: { status: 'candidate', summary: 'Discovered from visible page structure' },
  }));
}

export function safetyForAction(action = {}) {
  const label = normalizeString(action.label || action.name || action.text);
  const lowerLabel = label.toLowerCase();
  const blocked = BLOCKED_ACTION_KEYWORDS.find((keyword) => lowerLabel.includes(keyword.toLowerCase()));
  if (blocked) {
    return { safety: 'blocked', reason: `mutating or provider action keyword: ${blocked}` };
  }
  const allowed = ALLOWED_ACTION_KEYWORDS.find((keyword) => lowerLabel.includes(keyword.toLowerCase()));
  if (allowed) return { safety: 'allowed', reason: `read-oriented action keyword: ${allowed}` };
  if (action.source === 'navigation') return { safety: 'allowed', reason: 'navigation entry' };
  return { safety: 'blocked', reason: 'action is not recognized as read-only' };
}

export function businessEntriesFromDOMSummary(summary = [], route = '/') {
  return summary
    .filter((item) => isButtonLike(item) && (hasSidebarSource(item) || isKnownNavigationEntry(item)))
    .map((item) => ({
      route,
      label: visibleName(item),
      source: hasSidebarSource(item) ? normalizeString(item.sourceTestId || item.parentTestId || item.testId) : 'visible-navigation',
    }))
    .filter((entry) => entry.label);
}

export function businessActionsFromDOMSummary(summary = []) {
  return summary
    .filter((item) => isButtonLike(item) && !item.disabled)
    .map((item) => {
      const label = visibleName(item);
      const classified = safetyForAction({ type: 'click', label });
      return {
        type: 'click',
        label,
        target: item.testId ? { type: 'testId', value: item.testId } : { type: 'role', role: 'button', name: label },
        safety: classified.safety,
        reason: classified.reason,
      };
    })
    .filter((action) => action.label);
}

function isButtonLike(item = {}) {
  return item.tag === 'button' || item.role === 'button';
}

function hasSidebarSource(item = {}) {
  return normalizeString(item.sourceTestId || item.parentTestId || item.testId).includes('sidebar');
}

function isKnownNavigationEntry(item = {}) {
  const labels = [item.ariaLabel, item.text, item.testId].map((value) => normalizeString(value).toLowerCase()).filter(Boolean);
  return KNOWN_NAVIGATION_ENTRY_LABELS.some((entryLabel) => labels.includes(entryLabel.toLowerCase()));
}

function visibleName(item = {}) {
  return normalizeString(item.ariaLabel || item.text || item.testId);
}

function firstHeadingText(summary = []) {
  const heading = summary.find((item) => item.role === 'heading' || /^h[1-6]$/i.test(item.tag));
  return heading ? visibleName(heading) : '';
}

function flowID(entry) {
  return `visible-${slug(entry.source)}-${slug(entry.label)}`;
}

function routeFromURL(value) {
  try {
    return new URL(value).pathname || '/';
  }
  catch {
    return '/';
  }
}

function unique(values) {
  return Array.from(new Set(values));
}

function slug(value) {
  return normalizeString(value).toLowerCase().replace(/[^a-z0-9\u4e00-\u9fff]+/gi, '-').replace(/^-+|-+$/g, '') || 'unknown';
}

function normalizeString(value) {
  return String(value ?? '').trim();
}
