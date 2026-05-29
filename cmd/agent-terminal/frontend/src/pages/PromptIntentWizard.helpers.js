// @ts-nocheck

export const PROMPT_INTENT_TYPES = Object.freeze([
  {
    key: 'expert',
    label: '专家能力',
    placeholder: '例如：我希望 AI 在处理 sqlc、migration、schema drift 时先查源 SQL、再生成、最后验证。',
  },
  {
    key: 'recall',
    label: '给 AI 查阅的资料',
    placeholder: '粘贴规则、文档、FAQ、代码规范、业务说明、字段解释等。',
  },
  {
    key: 'default_rule',
    label: '默认规则',
    placeholder: '例如：涉及数据库迁移时，先说明影响范围，再修改 migration 和查询。',
  },
]);

export function intentTypeLabel(kind) {
  return PROMPT_INTENT_TYPES.find(x => x.key === kind)?.label || '未知类型';
}

export function intentTypePlaceholder(kind) {
  return PROMPT_INTENT_TYPES.find(x => x.key === kind)?.placeholder || '';
}

export function toErrorMessage(error) {
  return (
    (error && typeof error === 'object' && typeof error.message === 'string' ? error.message : '')
    || String(error || '')
  ).toString().trim();
}

const ISSUE_COPY = Object.freeze({
  missing_title: '需要补充一个清晰名称。',
  missing_summary: '需要补充一句简短说明。',
  missing_when_to_use: '需要说明 AI 什么时候会使用它。',
  missing_when_not_to_use: '需要说明哪些问题不适合使用它。',
  missing_workflow: '需要补充 AI 执行时的具体步骤。',
  missing_output: '需要写清楚输出会包含哪些栏目或结构。',
  vague_when_to_use: '适用场景还太泛，需要具体到任务或问题类型。',
  vague_output: '需要写清楚输出会包含哪些栏目或结构。',
  missing_save_boundary: '需要说明保存边界：没有明确保存工具或用户确认时，只能输出建议保存的条目，不能声称已经保存。',
  missing_source_facts: '需要先从原文提取关键要点，再整理成可用内容。',
  missing_source_fact_coverage: '原文里的关键要点没有覆盖完整，建议按缺口重新整理。',
  missing_recall_topic: '资料需要有一个可检索主题。',
  missing_recall_body: '资料正文不能为空。',
  missing_default_rule_body: '默认规则正文不能为空。',
  kind_mismatch: 'AI 判断这份内容更适合作为其他类型，请按推荐方向重新整理。',
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

const DETAIL_FIRST_ISSUES = new Set([
  'missing_source_fact_coverage',
  'source_fact_not_applied',
]);

function isSafeIssueDetail(raw) {
  const text = (raw || '').toString().trim();
  if (!text) return false;
  return !/\b(source_facts|source_profile|requested_kind|inferred_kind|draft_key|prompt_key)\b/.test(text);
}

function issueMessage(issue) {
  const code = (issue?.code || '').toString();
  const raw = (issue?.message || '').toString();
  if (DETAIL_FIRST_ISSUES.has(code) && isSafeIssueDetail(raw)) return raw;
  if (ISSUE_COPY[code]) return ISSUE_COPY[code];
  return raw || ISSUE_COPY[code] || code;
}

export function normalizeIntentIssues(raw) {
  return Array.isArray(raw)
    ? raw
      .map(issue => ({
        code: (issue?.code || '').toString(),
        severity: (issue?.severity || '').toString() === 'block' ? 'block' : 'review',
        message: issueMessage(issue),
      }))
      .filter(issue => issue.message)
    : [];
}

export function normalizeIntentCard(raw) {
  if (!raw || typeof raw !== 'object') return {};
  return {
    ...raw,
    hit_examples: Array.isArray(raw.hit_examples) ? raw.hit_examples.filter(Boolean) : [],
    miss_examples: Array.isArray(raw.miss_examples) ? raw.miss_examples.filter(Boolean) : [],
    conflicting_rules: Array.isArray(raw.conflicting_rules) ? raw.conflicting_rules : [],
  };
}

function normalizeOneIntentDraft(src, fallbackKind, responseMeta = {}) {
  const card = normalizeIntentCard(src.card || src.generated_card);
  const scope = (src.scope || responseMeta.scope || '').toString().trim().toLowerCase();
  return {
    draft_key: (src.draft_key || src.draftKey || '').toString(),
    requested_kind: (src.requested_kind || responseMeta.requested_kind || fallbackKind || '').toString(),
    inferred_kind: (src.inferred_kind || src.kind || card.kind || responseMeta.inferred_kind || fallbackKind || '').toString(),
    kind: (src.kind || src.inferred_kind || card.kind || fallbackKind || '').toString(),
    status: (src.status || '').toString(),
    scope: scope === 'global' ? 'global' : 'project',
    card,
    issues: normalizeIntentIssues(src.issues),
    confidence: Number(src.confidence ?? 0),
  };
}

export function normalizeIntentDraftResponse(res, fallbackKind) {
  const responseMeta = {
    requested_kind: (res?.requested_kind || '').toString(),
    inferred_kind: (res?.inferred_kind || '').toString(),
    scope: (res?.scope || '').toString(),
  };
  if (Array.isArray(res?.drafts) && res.drafts.length > 0) {
    const options = res.drafts.map(item => normalizeOneIntentDraft(item || {}, fallbackKind, responseMeta));
    return { ...options[0], draft_options: options };
  }
  const src = res?.draft && typeof res.draft === 'object' ? res.draft : (res || {});
  return normalizeOneIntentDraft(src, fallbackKind, responseMeta);
}

export function hasBlockIssues(issues) {
  return normalizeIntentIssues(issues).some(issue => issue.severity === 'block');
}

export function hasReviewIssues(issues, card) {
  if (normalizeIntentIssues(issues).some(issue => issue.severity === 'review')) return true;
  return Array.isArray(card?.conflicting_rules) && card.conflicting_rules.length > 0;
}

export function draftExamplesReady(card) {
  return Array.isArray(card?.hit_examples) && card.hit_examples.length > 0
    && Array.isArray(card?.miss_examples) && card.miss_examples.length > 0;
}

export function normalizeIntentKind(kind) {
  const value = (kind || '').toString().trim();
  return PROMPT_INTENT_TYPES.some(item => item.key === value) ? value : '';
}

function friendlyAlternativeReason(reason) {
  const text = (reason || '').toString().trim();
  if (!text) return '';
  if (/\b(requested_kind|inferred_kind|default_rule|recall|expert|kind_mismatch)\b/.test(text)) return '';
  return text;
}

export function suggestedAlternativeView(card, requestedKind = '') {
  const explicitKind = normalizeIntentKind(card?.suggested_alternative?.kind);
  const inferredKind = normalizeIntentKind(card?.kind);
  const normalizedRequestedKind = normalizeIntentKind(requestedKind);
  const targetKind = explicitKind || (inferredKind && inferredKind !== normalizedRequestedKind ? inferredKind : '');
  if (!targetKind) return null;
  const targetLabel = intentTypeLabel(targetKind);
  const copy = {
    expert: `改成「${targetLabel}」：让 AI 在相关任务中按步骤执行，不把它变成每次都生效的默认规则。`,
    recall: `改成「${targetLabel}」：把内容作为资料保存，让 AI 需要查阅时再使用。`,
    default_rule: `改成「${targetLabel}」：只保留必须长期约束 AI 行为的规则。`,
  };
  return {
    kind: targetKind,
    label: targetLabel,
    title: `推荐优化为${targetLabel}`,
    body: copy[targetKind] || `按「${targetLabel}」重新整理，避免和现有内容混在一起。`,
    reason: friendlyAlternativeReason(card?.suggested_alternative?.reason),
  };
}
