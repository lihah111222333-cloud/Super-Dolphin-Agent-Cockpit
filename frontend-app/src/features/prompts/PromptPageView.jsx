import { useQuery, useQueryClient } from '@tanstack/react-query'; import React, { useCallback, useEffect, useMemo, useReducer, useRef, useState } from 'react'; import { Dialog, Modal, ModalOverlay, Radio, RadioGroup } from 'react-aria-components'; import { z } from 'zod';
import { CheckCircle2, File, FileText, Upload } from 'lucide-react'; import {
commitPromptIntent, copyTextToClipboard, deletePrompt, discardPromptIntent, draftPromptIntent, dryRunPromptIntent, getDashboardPrompts, getPersonalizationProfile, getPreference, getPrompt, listPromptAssets, savePersonalizationProfile, setPreference, writePrompt,
} from '../../pages/prompts/services/promptPageService.js'; import { APP_COPY } from '../../shared/i18n/appI18n.js';
import { firstPresentText, parseStrictJsonValue, rawTextValue } from '../../pages/shared/pageShared.js'; import './PromptPageView.css';
import { requiredAppStoragePort } from '../../shared/api/browser/browserStorage.js';
const ACTIVE_PROMPT_PREF_KEY = 'settings.activePromptKey'; const PROMPTS_REQUEST_TIMEOUT_MS = 8000;
const PROMPT_KIND_OPTIONS = Object.freeze([ { key: 'expert', label: '专家能力' }, { key: 'recall', label: '参考资料' }, { key: 'default_rule', label: '默认规则' }, ]);
const PROMPT_DRAFT_NOT_READY_MESSAGE = '这条内容还需要完善后才能保存，请调整描述后重新生成。'; const PROMPT_DRAFT_REVIEW_MESSAGE = '保存前请先确认提示里的风险。'; const PROMPT_ISSUE_COPY = Object.freeze({ missing_title: '需要补充一个清晰名称。', missing_summary: '需要补充一句简短说明。', missing_when_to_use: '需要说明 AI 什么时候会使用它。',
missing_when_not_to_use: '需要说明哪些问题不适合使用它。', missing_workflow: '需要补充 AI 执行时的具体步骤。', missing_output: '需要写清楚输出会包含哪些栏目或结构。', vague_when_to_use: '适用场景还太泛，需要具体到任务或问题类型。', vague_output: '需要写清楚输出会包含哪些栏目或结构。', missing_save_boundary: '需要说明保存边界：没有明确保存工具或用户确认时，只能输出建议保存的条目，不能声称已经保存。',
missing_recall_topic: '资料需要有一个可检索主题。', missing_recall_body: '资料正文不能为空。', missing_default_rule_body: '默认规则正文不能为空。', missing_hit_examples: '需要补充适合使用的例子。', missing_miss_examples: '需要补充不适合使用的例子。', missing_source_facts: '需要先从原文提取关键要点，再整理成可用内容。',
missing_source_fact_coverage: '原文里的关键要点没有覆盖完整，建议按缺口重新整理。', source_fact_not_applied: '原文里的关键要点没有写入保存内容。', external_system_prompt: '这是外部模型或产品的系统提示词，不能直接作为默认规则启用。', external_system_prompt_source: '这是外部模型或产品的系统提示词，保存前需要确认来源和用途。', identity_pollution: '内容里包含模型或供应商身份声明，不能写入专家能力或默认规则。',
tool_protocol_pollution: '内容里包含外部工具协议，不能写入专家能力或默认规则。', overbroad_scope: '适用范围太宽，建议收窄到具体任务或问题。', default_rule_conflict: '和已有默认规则可能重复或冲突，保存前需要确认。', project_prompt_duplicate: '当前项目已有相似提示词，建议先确认是否更新已有内容。', builtin_prompt_duplicate: '系统已内置相似能力，不需要再保存一份。',
duplicate_recall_topic: '当前项目已有同名资料主题，请更新已有资料或换一个更具体的主题。', });
const promptListItemSchema = z.object({
 id: z.string().trim().min(1),
 name: z.string().trim().min(1),
 content: z.string(),
 description: z.string(),
 agentType: z.string().trim().min(1),
 when_to_use: z.string(),
 createdAt: z.string().trim().min(1),
 updatedAt: z.string().trim().min(1),
 match_when: z.unknown().optional(),
 priority: z.number().finite().int().optional(),
 enabled: z.boolean(),
 scope: z.enum(['project', 'global']),
 tags: z.array(z.string()).superRefine((tags, ctx) => {
 const kinds = tags.filter((tag) => tag.startsWith('intent:')); if (kinds.length !== 1 || !['intent:expert', 'intent:recall', 'intent:default_rule'].includes(kinds[0])) {
 ctx.addIssue({ code: 'custom', message: 'prompt tags must contain exactly one supported intent kind' }); }
 }),
 state: z.literal('pending_confirm').optional(),
 draft_key: z.string().trim().min(1).optional(),
 draft_status: z.literal('ready_to_save').optional(),
 source_type: z.string().optional(),
 card: z.unknown().optional(),
 issues: z.array(z.unknown()).optional(),
 }).strict();
 const promptListResponseSchema = z.object({ prompts: z.array(promptListItemSchema) }).strict();
const dashboardJsonValueSchema = z.lazy(() => z.union([
 z.string(), z.number(), z.boolean(), z.null(), z.array(dashboardJsonValueSchema), z.record(z.string(), dashboardJsonValueSchema),
]));
const dashboardPromptItemSchema = z.object({
 id: z.number().finite().int(),
 prompt_key: z.string().trim().min(1),
 title: z.string(),
 agent_key: z.string(),
 tool_name: z.string(),
 prompt_text: z.string(),
 when_to_use: z.string(),
 variables: dashboardJsonValueSchema,
 tags: z.array(z.string()),
 enabled: z.boolean(),
 manually_edited: z.boolean(),
 match_when: dashboardJsonValueSchema.optional(),
 priority: z.number().finite().int(),
 created_by: z.string(),
 updated_by: z.string(),
 created_at: z.string().trim().min(1),
 updated_at: z.string().trim().min(1),
 description: z.string(),
}).strict();
const dashboardPromptListResponseSchema = z.object({ prompts: z.array(dashboardPromptItemSchema) }).strict();
function textValue(value) { return value === null || value === undefined ? '' : value.toString().trim(); }
function textValues(values) { if (!Array.isArray(values)) return []; const result = []; for (const value of values) { const text = textValue(value); if (text) result.push(text); } return result; }
function optionalPromptCwd(value) { const cwd = textValue(value); return cwd && cwd !== '.' && cwd !== '未选择项目' ? cwd : ''; }
function firstText(...values) { for (const value of values) { const text = textValue(value); if (text) return text; } return ''; }
function cleanPromptTags(tags) { return tags.filter((tag) => ( !tag.startsWith('intent:') && tag !== 'scope.global' && !tag.startsWith('scope.cwd:') && !tag.startsWith('scope://') )); }
function optionalPromptDebugStorageEnabled() { if (typeof window === 'undefined') return false; if (window.__SUPER_DOLPHIN_PROMPT_DEBUG__ === true) return true; try {
const storage = requiredAppStoragePort('prompt debug storage'); return storage.get('super-dolphin.promptDebug') === '1'; } catch { return false; } }
function isReadonlyFallbackListError(error) { const message = firstPresentText(error?.message, error).toLowerCase(); return error?.code === -32601
|| message.includes('method not found') || message.includes('not registered') || message.includes('unknown method') || message.includes('not implemented') || message.includes('unimplemented'); }
function serializeJsonForEditor(value) { if (value === undefined || value === null || value === '') return ''; if (typeof value === 'string') return value; try { return JSON.stringify(value, null, 2); } catch { return ''; } }
function parseJsonObjectForEditor(value, label) { const text = textValue(value); if (!text) return { value: null, error: '' }; try { const parsed = parseStrictJsonValue(text, label); if (parsed === null) return { value: null, error: '' };
if (typeof parsed !== 'object' || Array.isArray(parsed)) { return { value: undefined, error: `${label}必须是 JSON 对象` }; } return { value: parsed, error: '' }; } catch (err) { return { value: undefined, error: `${label}不是合法 JSON：${errorMessage(err)}` }; } }
 function promptAssetType(tags) { if (tags.includes('intent:recall')) return 'recall'; if (tags.includes('intent:default_rule')) return 'default_rule'; return 'expert'; }
function promptPreviewText(item) { return item.content || item.whenToUse || item.description || '已保存，AI 会在相关场景中使用'; }
function promptTextList(value) { return textValues(value); }
function promptIssueMessage(issue) { const code = textValue(issue?.code); return PROMPT_ISSUE_COPY[code] || textValue(issue?.message) || code; }
function normalizePromptIssues(raw) { if (!Array.isArray(raw)) return []; const issues = []; for (const issue of raw) { const normalizedIssue = { code: textValue(issue?.code), severity: textValue(issue?.severity).toLowerCase() === 'block' ? 'block' : 'review',
message: promptIssueMessage(issue), }; if (normalizedIssue.message) issues.push(normalizedIssue); } return issues; }
 function normalizePromptItem(raw) {
 const tags = raw.tags; const assetType = promptAssetType(tags); const item = { id: raw.id, name: raw.name,
 content: raw.content, description: raw.description, whenToUse: raw.when_to_use, agentType: raw.agentType,
 priority: raw.priority, enabled: raw.enabled, scope: raw.scope, tags: cleanPromptTags(tags), assetType, state: raw.state, draftKey: raw.draft_key, draftStatus: raw.draft_status,
 card: raw.card, issues: raw.issues, matchWhen: raw.match_when,
 }; item.isPendingDraft = item.state === 'pending_confirm' || Boolean(item.draftKey && item.draftStatus === 'ready_to_save'); item.preview = promptPreviewText(item); return item; }
 function normalizePromptList(response) { return promptListResponseSchema.parse(response).prompts.map(normalizePromptItem); }
function normalizeDashboardPromptItem(raw) { const tags = raw.tags; const item = { id: raw.prompt_key, name: raw.title,
 content: raw.prompt_text, description: raw.description, whenToUse: raw.when_to_use, agentType: raw.agent_key,
 priority: raw.priority, enabled: raw.enabled, scope: tags.includes('scope.global') ? 'global' : 'project', tags: cleanPromptTags(tags), assetType: promptAssetType(tags),
 matchWhen: raw.match_when, isPendingDraft: false,
 }; item.preview = promptPreviewText(item); return item; }
function normalizeDashboardPromptList(response) { return dashboardPromptListResponseSchema.parse(response).prompts.map(normalizeDashboardPromptItem); }
function promptBucket(item) { if (item.isPendingDraft) return 'pending'; return item.assetType === 'recall' || item.assetType === 'default_rule' ? item.assetType : 'expert'; }
function canForceLaunchPrompt(item) { return promptBucket(item) === 'expert' && item.enabled !== false && !item.isPendingDraft; }
function promptLifecycleStatus(item, active) { if (item.isPendingDraft) return 'pending'; if (item.enabled === false) return 'disabled'; if (active) return 'started'; return 'created'; }
function promptLifecycleStatusLabel(status) { if (status === 'created') return '已创建'; if (status === 'started') return '已启动'; if (status === 'disabled') return '已停用'; return ''; }
function promptCounts(items) { const counts = { all: items.length, expert: 0, recall: 0, default_rule: 0, pending: 0 }; items.forEach((item) => { const bucket = promptBucket(item); counts[bucket] = (counts[bucket] || 0) + 1; }); return counts; }
function trunc(value, max = 120) { const text = textValue(value); if (!text) return '暂无内容'; return text.length > max ? `${text.slice(0, max)}...` : text; }
function wordListFromText(value) { const result = []; const seen = new Set(); for (const word of textValue(value).split(/[，,;；\n]/)) { const text = textValue(word); const key = text.toLowerCase(); if (!text || seen.has(key)) continue; seen.add(key); result.push(text);
} return result; }
function promptFormFromItem(item) { return { id: textValue(item.id), name: textValue(item.name), description: textValue(item.description), whenToUse: textValue(item.whenToUse), content: rawTextValue(item.content), originalContent: rawTextValue(item.content),
 agentType: item.agentType, tagsText: (Array.isArray(item.tags) ? item.tags : []).join(', '), scope: item.scope === 'global' ? 'global' : 'project', enabled: item.enabled !== false,
 priority: Number.isFinite(Number(item.priority)) ? Number(item.priority) : 0, hasPriority: item.priority !== undefined,
 matchWhenText: serializeJsonForEditor(item.matchWhen), hasMatchWhen: item.matchWhen !== undefined, }; }
 function emptyPromptForm() { return { id: '', name: '', description: '', whenToUse: '', content: '', originalContent: '', agentType: 'main', tagsText: '', scope: 'project', enabled: true, priority: 0, hasPriority: true, matchWhenText: '', hasMatchWhen: false, }; }
const emptyPersonalizationProfile = Object.freeze({ displayName: '', role: '', background: '', customInstructions: '', });
// 与后端 internal/module/personalization/dto.go 保持一致：短字段 80、长字段 1200 字符（按 code point 计数）。
const PROFILE_FIELD_LIMITS = Object.freeze({ displayName: 80, role: 80, background: 1200, customInstructions: 1200 });
function validatePersonalizationProfile(profile) {
const errors = {}; for (const [field, limit] of Object.entries(PROFILE_FIELD_LIMITS)) { const value = profile?.[field]; const length = typeof value === 'string' ? [...value].length : 0; if (length > limit) errors[field] = `不能超过 ${limit} 个字符（当前 ${length} 个）`; } return errors; }
function normalizeDraftItem(raw = {}, fallbackKind = 'expert', meta = {}) {
const card = raw.card && typeof raw.card === 'object' ? raw.card : {}; const workflow = promptTextList(card.workflow); const hitExamples = promptTextList(card.hit_examples || card.hitExamples); const missExamples = promptTextList(card.miss_examples || card.missExamples); return {
draftKey: firstText(raw.draft_key, raw.draftKey), kind: firstText(raw.inferred_kind, raw.inferredKind, raw.kind, card.kind, meta.inferredKind, fallbackKind), scope: firstText(raw.scope, card.scope, 'project'), status: firstText(raw.status, 'review'),
title: firstText(card.title, raw.title, '未命名草稿'), summary: firstText(card.summary, raw.description), whenToUse: firstText(card.when_to_use, card.whenToUse), whenNotToUse: firstText(card.when_not_to_use, card.whenNotToUse), workflow,
saveBoundary: firstText(card.save_boundary, card.saveBoundary), output: firstText(card.output, card.recall_body, card.recallBody, card.default_rule_body, card.defaultRuleBody, raw.content), hitExamples, missExamples, card, issues: normalizePromptIssues(raw.issues), }; }
function normalizeDraft(raw = {}, fallbackKind = 'expert') { const meta = { inferredKind: firstText(raw.inferred_kind, raw.inferredKind), }; if (Array.isArray(raw.drafts) && raw.drafts.length > 0) {
const draftOptions = raw.drafts.map((item) => normalizeDraftItem(item, fallbackKind, meta)); return { ...draftOptions[0], draftOptions }; } return normalizeDraftItem(raw, fallbackKind, meta); }
function pendingDraftFromItem(item) { return normalizeDraft({ draft_key: item.draftKey || item.id, kind: item.assetType || 'expert', scope: item.scope || 'project', status: item.draftStatus || 'ready_to_save', card: item.card || { kind: item.assetType || 'expert',
title: item.name, summary: item.description, output: item.content, hit_examples: [], miss_examples: [], }, issues: Array.isArray(item.issues) ? item.issues : [], }, item.assetType || 'expert'); }
function noticeText(error, prefix) { const message = firstPresentText(error?.message, error); const friendly = promptFriendlyErrorText(message); if (friendly) return friendly; return `${prefix}：${message}`; }
function promptFriendlyErrorText(message) { const lower = textValue(message).toLowerCase(); if (lower.includes('prompt intent draft is not ready to save')) { return PROMPT_DRAFT_NOT_READY_MESSAGE; } if (lower.includes('prompt intent draft requires risk confirmation')) {
return PROMPT_DRAFT_REVIEW_MESSAGE; } return ''; }
function promptDraftNeedsRevision(draft) { const status = textValue(draft?.status).toLowerCase(); const hasBlockIssue = Array.isArray(draft?.issues) && draft.issues.some((issue) => textValue(issue?.severity).toLowerCase() === 'block');
return status === 'draft' || status === 'draft_blocked' || status === 'blocked' || hasBlockIssue; }
function errorMessage(error) { if (!error) return ''; return error?.message || String(error); }
async function withTimeout(promise, timeoutMs, message) { let timeoutID; const timeout = new Promise((_, reject) => { timeoutID = globalThis.setTimeout(() => reject(new Error(message)), timeoutMs); }); try { return await Promise.race([promise, timeout]); } finally {
if (timeoutID) globalThis.clearTimeout(timeoutID); } }
function promptAssetsQueryKey(cwd) { return ['dashboard', 'project', cwd, 'prompts']; }
function activePromptQueryKey(cwd) { return ['dashboard', 'project', cwd, 'active-prompt']; }
async function fetchPromptAssetsSurface(cwd) { try { const response = await withTimeout( listPromptAssets({ cwd }), PROMPTS_REQUEST_TIMEOUT_MS, '提示词列表加载超时，请检查提示词目录或后端状态。', ); return { items: normalizePromptList(response), fallbackMode: false }; } catch (err) {
if (!isReadonlyFallbackListError(err)) throw err; const response = await withTimeout( getDashboardPrompts({ cwd }), PROMPTS_REQUEST_TIMEOUT_MS, '只读提示词列表加载超时，请检查 dashboard/prompts 后端状态。', ); return { items: normalizeDashboardPromptList(response), fallbackMode: true,
fallbackReason: errorMessage(err), }; } }
async function fetchActivePromptId(cwd) { const value = await withTimeout( getPreference({ cwd, key: ACTIVE_PROMPT_PREF_KEY }), PROMPTS_REQUEST_TIMEOUT_MS, '强制提示词状态加载超时，请检查后端状态。', ); return typeof value === 'string' ? value.trim() : ''; }
function promptQueryState(cwd, promptAssetsQuery, activePromptQuery) {
const items = Array.isArray(promptAssetsQuery.data?.items) ? promptAssetsQuery.data.items : []; const hasPromptSnapshot = Array.isArray(promptAssetsQuery.data?.items); const promptSyncError = promptAssetsQuery.error ? errorMessage(promptAssetsQuery.error) : '';
const activePromptSyncError = activePromptQuery.error ? errorMessage(activePromptQuery.error) : ''; const syncErrorMessage = [promptSyncError, activePromptSyncError].filter(Boolean).join('；');
const activePromptId = promptActiveIdForItems(textValue(activePromptQuery.data), items, hasPromptSnapshot); return { items, fallbackMode: Boolean(promptAssetsQuery.data?.fallbackMode), activePromptId, loading: Boolean(cwd) && promptAssetsQuery.isPending && !hasPromptSnapshot,
syncError: syncErrorMessage && hasPromptSnapshot ? `同步失败，显示的是上次成功的数据：${syncErrorMessage}` : '', error: promptSyncError && !hasPromptSnapshot ? noticeText(promptAssetsQuery.error, '加载提示词失败') : '', }; }
function promptActiveIdForItems(activePromptId, items, hasPromptSnapshot) { if (!activePromptId) return ''; if (!hasPromptSnapshot) return activePromptId; return items.some((item) => item.id === activePromptId && canForceLaunchPrompt(item)) ? activePromptId : ''; }
function usePromptQueries(cwd) { const { data: promptAssetsData, error: promptAssetsError, isPending: promptAssetsPending, refetch: refetchPromptAssets, } = useQuery({ queryKey: promptAssetsQueryKey(cwd), queryFn: () => fetchPromptAssetsSurface(cwd), enabled: Boolean(cwd),
}); const { data: activePromptData, error: activePromptError, refetch: refetchActivePrompt, } = useQuery({ queryKey: activePromptQueryKey(cwd), queryFn: () => fetchActivePromptId(cwd), enabled: Boolean(cwd), }); const promptAssetsQuery = { data: promptAssetsData,
error: promptAssetsError, isPending: promptAssetsPending, }; const activePromptQuery = { data: activePromptData, error: activePromptError, }; const state = promptQueryState(cwd, promptAssetsQuery, activePromptQuery); return { ...state, refetchPromptAssets, refetchActivePrompt, };
}
function usePromptRefreshSurface(cwd, queryClient, refetchPromptAssets, refetchActivePrompt) { return useCallback(async (options = {}) => { const isCancelled = typeof options.isCancelled === 'function' ? options.isCancelled : () => false; if (!cwd) return [];
const [assetResult, activeResult] = await Promise.all([ refetchPromptAssets(), refetchActivePrompt(), ]); const nextItems = Array.isArray(assetResult.data?.items) ? assetResult.data.items : []; if (isCancelled()) return nextItems;
const nextActiveId = textValue(activeResult.data); if (nextActiveId && !nextItems.some((item) => item.id === nextActiveId && canForceLaunchPrompt(item))) { queryClient.setQueryData(activePromptQueryKey(cwd), ''); } return nextItems;
}, [cwd, queryClient, refetchActivePrompt, refetchPromptAssets]); }
function usePromptRefreshEffects(promptRefreshKey, refreshPromptSurface) { useEffect(() => { if (promptRefreshKey <= 0) return undefined; let cancelled = false; void refreshPromptSurface({ isCancelled: () => cancelled }); return () => { cancelled = true; };
}, [promptRefreshKey, refreshPromptSurface]);
useEffect(() => { const runAutoRefresh = () => { if (typeof document !== 'undefined' && document.visibilityState === 'hidden') return; void refreshPromptSurface(); }; const handleVisibilityChange = () => {
if (typeof document === 'undefined' || document.visibilityState === 'visible') runAutoRefresh(); }; window.addEventListener('focus', runAutoRefresh); document.addEventListener('visibilitychange', handleVisibilityChange); return () => {
window.removeEventListener('focus', runAutoRefresh); document.removeEventListener('visibilitychange', handleVisibilityChange); }; }, [refreshPromptSurface]); }
 function promptWritePayload(cwd, form, name, agentType, matchWhen) { const payload = { cwd, id: form.id, name, description: form.description, agentType, when_to_use: form.whenToUse,
 content: form.content, tags: wordListFromText(form.tagsText), enabled: form.enabled, scope: form.scope === 'global' ? 'global' : 'project',
 match_when: form.hasMatchWhen || textValue(form.matchWhenText) ? matchWhen : undefined, }; if (form.hasPriority) payload.priority = Number.isFinite(Number(form.priority)) ? Number(form.priority) : 0; return payload; }
 async function savePromptForm(options) {
 const { cwd, form, isCurrentRequest, refreshPromptSurface, setEditorOpen, setNotice, setSaving } = options;
 const name = textValue(form.name);
 if (!name) { setNotice('请填写提示词名称'); return; }
 const agentType = textValue(form.agentType);
 if (!agentType) { setNotice('请填写 Agent Key'); return; }
 const parsedMatchWhen = parseJsonObjectForEditor(form.matchWhenText, '自动匹配条件');
 if (parsedMatchWhen.error) { setNotice(parsedMatchWhen.error); return; }
 setSaving(true);
 try {
 await writePrompt(promptWritePayload(cwd, form, name, agentType, parsedMatchWhen.value));
 if (!isCurrentRequest()) return;
 await refreshPromptSurface({ force: true });
 if (!isCurrentRequest()) return;
 setEditorOpen(false);
 setNotice(`提示词已保存：${name}`);
 } catch (err) {
 if (isCurrentRequest()) setNotice(noticeText(err, '保存失败'));
 } finally {
 if (isCurrentRequest()) setSaving(false);
 }
 }
async function removePromptItem({ cwd, item, refreshPromptSurface, setActioning, setNotice }) { setActioning(`delete:${item.id}`); try {
await deletePrompt({ cwd, id: item.id, scope: item.scope === 'global' ? 'global' : 'project' }); await refreshPromptSurface({ force: true }); setNotice(`已删除：${item.name}`); } catch (err) { setNotice(noticeText(err, '删除失败')); } finally { setActioning(''); } }
async function copyPromptItem({ cwd, item, fallbackMode, setActioning, setNotice }) { if (item.isPendingDraft) { setNotice('这条草稿还在待确认，确认保存后才能复制内容'); return; } setActioning(`copy:${item.id}`); try { let content = rawTextValue(item.content); if (!fallbackMode && item.id) {
const response = await getPrompt({ cwd, id: item.id }); content = firstText(response?.prompt?.content, response?.prompt?.prompt_text, response?.promptText, content); } if (!textValue(content)) { setNotice('暂无可复制内容'); return;
} await copyTextToClipboard(content); setNotice('已复制提示词内容'); } catch (err) { setNotice(noticeText(err, '复制失败')); } finally { setActioning(''); } }
async function setLaunchPreference({ cwd, item, queryClient, setActioning, setNotice }) { setActioning(`launch:${item.id}`); try {
await setPreference({ cwd, key: ACTIVE_PROMPT_PREF_KEY, value: item.id }); queryClient.setQueryData(activePromptQueryKey(cwd), item.id); setNotice(`已设为强制使用：${item.name}`); } catch (err) { setNotice(noticeText(err, '设置强制使用失败')); } finally { setActioning(''); } }
async function clearLaunchPreference({ cwd, queryClient, setActioning, setNotice }) { setActioning('launch:clear'); try {
await setPreference({ cwd, key: ACTIVE_PROMPT_PREF_KEY, value: '' }); queryClient.setQueryData(activePromptQueryKey(cwd), ''); setNotice('已取消强制使用，新对话将使用默认路由'); } catch (err) { setNotice(noticeText(err, '取消强制使用失败')); } finally { setActioning(''); } }
async function discardPromptDraftItem({ cwd, item, refreshPromptSurface, setActioning, setNotice }) { const draftKey = item.draftKey || item.id; setActioning(`discard:${draftKey}`); try {
await discardPromptIntent({ cwd, draftKey }); await refreshPromptSurface({ force: true }); setNotice(`已丢弃：${item.name}`); } catch (err) { setNotice(noticeText(err, '丢弃失败')); } finally { setActioning(''); } }
function usePromptEditorActions(params) {
const { cwd, fallbackMode, actioning, form, queryClient, refreshPromptSurface, setters } = params;
const { setActioning, setEditorOpen, setForm, setNotice, setSaving } = setters;
const currentCwdRef = useRef(cwd);
const saveGenerationRef = useRef(0);
useEffect(() => {
currentCwdRef.current = cwd;
saveGenerationRef.current += 1;
}, [cwd]);
const savePrompt = () => {
const generation = saveGenerationRef.current + 1;
saveGenerationRef.current = generation;
return savePromptForm({
cwd, form, refreshPromptSurface, setEditorOpen, setNotice, setSaving,
isCurrentRequest: () => currentCwdRef.current === cwd && saveGenerationRef.current === generation,
});
};
return { retryPromptSync: () => {
void refreshPromptSurface({ force: true }); }, openEdit: (item) => { saveGenerationRef.current += 1; setForm(promptFormFromItem(item)); setEditorOpen(true); setNotice(''); }, savePrompt, removePrompt: (item) => {
if (fallbackMode) { setNotice('当前为只读降级，暂不支持删除'); return; } if (!item.id || actioning) return; void removePromptItem({ cwd, item, refreshPromptSurface, setActioning, setNotice }); }, copyPrompt: (item) => { if (!item.id || actioning) return;
void copyPromptItem({ cwd, item, fallbackMode, setActioning, setNotice }); }, setLaunchPrompt: (item) => { if (!canForceLaunchPrompt(item) || actioning) return; void setLaunchPreference({ cwd, item, queryClient, setActioning, setNotice }); }, clearLaunchPrompt: () => {
if (actioning) return; void clearLaunchPreference({ cwd, queryClient, setActioning, setNotice }); }, }; }
function usePromptDraftActions({ cwd, actioning, refreshPromptSurface, setters }) { const { setActioning, setNotice, setWizardDraft, setWizardOpen } = setters; return { continuePendingDraft: (item) => {
setWizardDraft(pendingDraftFromItem(item)); setWizardOpen(true); setNotice(''); }, discardDraft: (item) => { const draftKey = item.draftKey || item.id; if (!draftKey || actioning) return; void discardPromptDraftItem({ cwd, item, refreshPromptSurface, setActioning, setNotice });
}, handleWizardSaved: async () => { setWizardOpen(false); setWizardDraft(null); await refreshPromptSurface({ force: true }); setNotice('已保存，可在新对话中被 AI 发现和使用'); }, }; }
function PromptStatusMessages(props) { const { copy, isProjectPending, fallbackMode, syncError, error, loading, onRetry } = props; return (
<> {isProjectPending ? <div className="prompt-notice">{copy.connecting}</div> : null} {fallbackMode ? <div className="prompt-notice warn">{copy.fallbackNotice}</div> : null} {syncError ? <PromptRetryNotice copy={copy} message={syncError} onRetry={onRetry} /> : null}
{error ? <PromptRetryNotice copy={copy} message={error} onRetry={onRetry} /> : null} {loading ? <output className="prompt-loading" aria-live="polite">{copy.loading}</output> : null} </> ); }
function PromptRetryNotice({ copy = APP_COPY.zh.prompts, message, onRetry }) { return ( <div className="prompt-notice error" role="alert"> <span>{message}</span> <button type="button" className="ghost" onClick={onRetry}>{copy.retrySync}</button> </div> ); }
function PromptEmptyState({ copy }) { return ( <div className="empty-state prompt-empty"> <File size={30} /> <h3>{copy.emptyTitle}</h3> <p>{copy.emptyText}</p> </div> ); }
function PromptCardsGrid(props) { const { visibleItems, activePromptId, actioning, fallbackMode, editorActions, draftActions } = props; return ( <div className="prompt-card-grid"> {visibleItems.map((item, index) => (
<PromptCard key={item.id || index} item={item} active={activePromptId === item.id && canForceLaunchPrompt(item)} actioning={actioning} fallbackMode={fallbackMode} onEdit={editorActions.openEdit} onCopy={editorActions.copyPrompt} onDelete={editorActions.removePrompt}
onSetLaunch={editorActions.setLaunchPrompt} onClearLaunch={editorActions.clearLaunchPrompt} onContinueDraft={draftActions.continuePendingDraft} onDiscardDraft={draftActions.discardDraft} /> ))} </div> ); }
function PromptPageLayout(props) { const showEmpty = !props.isProjectPending && !props.loading && !props.showBlockingError && props.visibleItems.length === 0; const showCards = !props.isProjectPending && !props.loading && props.visibleItems.length > 0; return (
<section className="console-page prompt-page"> <PageHeader copy={props.copy} title={props.copy.title} subtitle={props.copy.subtitle} projectPath={props.cwd || props.projectPath} /> <PromptPersonalizationOverview copy={props.copy} counts={props.counts}
fallbackMode={props.fallbackMode} isProjectPending={props.isProjectPending} personalization={props.personalization} /> <PromptStatusMessages {...props} onRetry={props.editorActions.retryPromptSync} /> {showEmpty ? <PromptEmptyState copy={props.copy} /> : null}
{showCards ? <PromptCardsGrid {...props} /> : null} {props.notice && !props.modals.editorOpen && !props.modals.wizardOpen ? <div className="prompt-notice">{props.notice}</div> : null} <PromptEditorHost {...props} /> <PromptWizardHost {...props} /> </section> ); }
function PromptEditorHost(props) { const { cwd, fallbackMode, modals, notice, saving, setters, editorActions } = props; if (!modals.editorOpen) return null; return (
<PromptEditorModal cwd={cwd} fallbackMode={fallbackMode} form={modals.form} notice={notice} saving={saving} onChange={setters.setForm} onClose={() => { setters.setEditorOpen(false); setters.setNotice(''); }} onSave={editorActions.savePrompt} /> ); }
function PromptWizardHost({ modals, cwd, resolveLaunchPreferences, setters, draftActions }) { if (!modals.wizardOpen) return null; return (
<PromptIntentWizardModal key={promptWizardKey(modals.wizardDraft)} cwd={cwd} initialDraft={modals.wizardDraft} resolveLaunchPreferences={resolveLaunchPreferences} onClose={() => { setters.setWizardOpen(false); setters.setWizardDraft(null);
}} onSaved={draftActions.handleWizardSaved} /> ); }
function promptWizardKey(draft) { return textValue(draft?.draftKey) || textValue(draft?.id) || textValue(draft?.name) || 'new'; }
function savedPersonalizationProfile(result) { const profile = result.profile && typeof result.profile === 'object' ? result.profile : {}; return { ...emptyPersonalizationProfile, ...profile }; }
function usePromptPageState(cwd) { const [noticeState, setNoticeState] = useState({ cwd, notice: '' }); if (noticeState.cwd !== cwd) { setNoticeState({ cwd, notice: '' }); } const setNotice = useCallback((value) => {
 setNoticeState((current) => ({ ...current, notice: typeof value === 'function' ? value(current.notice) : value }));
 }, []); const [actioning, setActioning] = useState(''); const [editorOpen, setEditorOpen] = useState(false); const [savingState, setSavingState] = useState({ cwd, value: false });
if (savingState.cwd !== cwd) setSavingState({ cwd, value: false });
const saving = savingState.cwd === cwd ? savingState.value : false;
const setSaving = useCallback((value) => { setSavingState((current) => (current.cwd === cwd ? { cwd, value } : current)); }, [cwd]);
const [form, setForm] = useState(emptyPromptForm);
const [wizardOpen, setWizardOpen] = useState(false);
const [wizardDraft, setWizardDraft] = useState(null);
return {
actioning, form, modals: { editorOpen, form, wizardDraft, wizardOpen },
notice: noticeState.cwd === cwd ? noticeState.notice : '', saving,
setters: { setActioning, setEditorOpen, setForm, setNotice, setSaving, setWizardDraft, setWizardOpen },
};
}
export function PromptPageView({ copy = APP_COPY.zh.prompts, projectPath, refreshKey = 0, resolveLaunchPreferences }) {
const cwd = optionalPromptCwd(projectPath); const isProjectPending = !cwd; const queryClient = useQueryClient(); const pageState = usePromptPageState(cwd); const { actioning, form, modals, notice, saving, setters } = pageState;
const profileCwdRef = useRef(cwd);
const profileSaveGenerationRef = useRef(0);
const profileDraftRevisionRef = useRef(0);
useEffect(() => {
profileCwdRef.current = cwd;
profileSaveGenerationRef.current += 1;
profileDraftRevisionRef.current += 1;
}, [cwd]);
const queryState = usePromptQueries(cwd); const { items, fallbackMode, activePromptId, loading, syncError, error } = queryState; const { data: profileData, error: profileError, isLoading: profileLoading } = useQuery({ queryKey: ['personalizationProfile', cwd],
queryFn: () => withTimeout(getPersonalizationProfile({ cwd }), PROMPTS_REQUEST_TIMEOUT_MS, '个人资料加载超时，请检查后端状态。'), enabled: Boolean(cwd),
}); const loadedProfile = profileData?.profile ? { ...emptyPersonalizationProfile, ...profileData.profile } : emptyPersonalizationProfile; const [profileDraft, setProfileDraft] = useState({ cwd: '', profile: null });
const profileForm = profileDraft.cwd === cwd && profileDraft.profile ? profileDraft.profile : loadedProfile;
const setProfileForm = useCallback((nextProfile) => {
profileDraftRevisionRef.current += 1;
setProfileDraft({ cwd, profile: nextProfile });
}, [cwd]);
const refreshPromptSurface = usePromptRefreshSurface( cwd,
queryClient, queryState.refetchPromptAssets, queryState.refetchActivePrompt, ); usePromptRefreshEffects(Number(refreshKey || 0), refreshPromptSurface);
const counts = useMemo(() => promptCounts(items), [items]); const visibleItems = items; const editorActions = usePromptEditorActions({ cwd, fallbackMode, actioning, form, queryClient, refreshPromptSurface, setters,
}); const draftActions = usePromptDraftActions({ cwd, actioning, refreshPromptSurface, setters }); const handleSaveProfile = async () => {
if (!cwd || saving) return;
const generation = profileSaveGenerationRef.current + 1;
const draftRevision = profileDraftRevisionRef.current;
profileSaveGenerationRef.current = generation;
const isCurrentRequest = () => profileCwdRef.current === cwd && profileSaveGenerationRef.current === generation;
const isCurrentDraft = () => isCurrentRequest() && profileDraftRevisionRef.current === draftRevision;
setters.setSaving(true);
setters.setNotice('');
try {
const result = await savePersonalizationProfile({ cwd, profile: profileForm });
const savedProfile = savedPersonalizationProfile(result);
if (!isCurrentRequest()) return;
queryClient.setQueryData(['personalizationProfile', cwd], { profile: savedProfile });
if (!isCurrentDraft()) return;
setProfileDraft({ cwd, profile: null });
setters.setNotice('个人资料已保存');
} catch (err) {
if (isCurrentDraft()) setters.setNotice(noticeText(err, '个人资料保存失败'));
} finally {
if (isCurrentRequest()) setters.setSaving(false);
}
}; const handleImportMemory = () => { setters.setWizardDraft({ kind: 'recall', rawInput: '', scope: 'project' }); setters.setWizardOpen(true);
}; const profileValidationErrors = useMemo(() => validatePersonalizationProfile(profileForm), [profileForm]); const handleProjectRequired = useCallback(() => { setters.setNotice('请先在聊天页选择项目，再使用个性化设置。'); }, [setters]);
const layoutProps = { activePromptId, actioning, counts, copy, cwd, draftActions, editorActions, error, fallbackMode, isProjectPending, loading, modals, notice, personalization: { error: profileError, loading: profileLoading, onImportMemory: handleImportMemory,
onProfileChange: setProfileForm, onProjectRequired: handleProjectRequired, onSaveProfile: handleSaveProfile, profile: profileForm, saving, validationErrors: profileValidationErrors, },
projectPath, resolveLaunchPreferences, saving, setters, showBlockingError: Boolean(error), syncError, visibleItems, };
return <PromptPageLayout {...layoutProps} />; }
function PageHeader({ copy, title, subtitle, projectPath }) { return (
<header className="prompt-header"> <div> <h1><FileText size={25} /> {title}</h1> {subtitle ? <strong>{subtitle}</strong> : null} <p title={projectPath}>{copy.currentProject}: {projectPath || copy.unknownProject}</p> </div> </header> ); }
function PromptPersonalizationOverview({ copy, counts, isProjectPending, fallbackMode, personalization }) { const metrics = [ [copy.expert, counts.expert || 0], [copy.recall, counts.recall || 0], [copy.defaultRule, counts.default_rule || 0], [copy.pending, counts.pending || 0],
]; const profile = personalization?.profile || emptyPersonalizationProfile; const profileLoading = Boolean(personalization?.loading); const profileSaving = Boolean(personalization?.saving);
const profileErrors = personalization && personalization.validationErrors ? personalization.validationErrors : {}; const profileInvalid = Object.keys(profileErrors).length > 0;
// 无项目：按钮保持可聚焦可点击，点击后由 onProjectRequired 显示明确引导（不再永久禁用且原因不明）。
// 表单无效：禁用保存，但每个字段下方显示具体校验原因；加载/保存中为瞬态禁用。
const saveProfileDisabled = !isProjectPending && (profileLoading || profileSaving || profileInvalid);
const updateProfile = (key) => (event) => { personalization?.onProfileChange?.({ ...profile, [key]: event.target.value });
}; const profileStatus = isProjectPending ? copy.waitingProject : personalization?.error ? copy.loadFailed : profileLoading ? copy.loadingShort : copy.connected; const overviewText = isProjectPending ? copy.overviewConnecting : fallbackMode ? copy.overviewFallback
: copy.overviewReady; const handleSaveClick = () => { if (isProjectPending) { personalization?.onProjectRequired?.(); return; } if (saveProfileDisabled) return; personalization?.onSaveProfile?.(); };
const handleImportClick = () => { if (isProjectPending) { personalization?.onProjectRequired?.(); return; } personalization?.onImportMemory?.(); };
const fieldError = (key) => (profileErrors[key] ? <span className="personalization-field-error" role="alert">{profileErrors[key]}</span> : null); return (
<section className="personalization-overview" aria-label={copy.overviewAria}>
  <div className="personalization-overview-hero fusion-surface">
    <div className="personalization-overview-copy">
      <span>{copy.profile}</span>
      <h2>{copy.overviewTitle}</h2>
      <p>{overviewText}</p>
    </div>
    <dl>
      {metrics.map(([label, value]) => (
        <div key={label} className="fusion-surface-glass">
          <dt>{label}</dt>
          <dd>{value}</dd>
        </div>
      ))}
    </dl>
  </div>
  <div className="personalization-overview-content personalization-profile-grid">
    <section className="personalization-profile-card" aria-label={copy.profile}>
      <header>
        <h3>{copy.profile}</h3>
        <span>{profileStatus}</span>
      </header>
      <div className="personalization-form-grid">
        <label>{copy.displayName}<input aria-label={copy.displayName} type="text" value={profile.displayName} onChange={updateProfile('displayName')} disabled={isProjectPending || profileLoading} />{fieldError('displayName')}</label>
        <label>{copy.role}<input aria-label={copy.role} type="text" value={profile.role} onChange={updateProfile('role')} disabled={isProjectPending || profileLoading} />{fieldError('role')}</label>
        <label>{copy.background}<textarea aria-label={copy.background} rows={3} value={profile.background} onChange={updateProfile('background')} disabled={isProjectPending || profileLoading} />{fieldError('background')}</label>
        <label>{copy.customInstructions}<textarea aria-label={copy.customInstructions} rows={3} value={profile.customInstructions} onChange={updateProfile('customInstructions')} disabled={isProjectPending || profileLoading} />{fieldError('customInstructions')}</label>
      </div>
      <button type="button" aria-disabled={isProjectPending || undefined} disabled={saveProfileDisabled} title={isProjectPending ? '请先选择项目' : undefined} onClick={handleSaveClick}>
        {profileSaving ? copy.saving : copy.saveProfile}
      </button>
    </section>
    <section className="personalization-profile-card suiyuan-import-memory-card" aria-label={copy.importMemoryTitle}>
      <div className="suiyuan-import-memory-content">
        <Upload size={28} className="suiyuan-import-memory-icon" />
        <h3>{copy.importMemoryTitle}</h3>
        <p>{copy.importMemoryText}</p>
        <button type="button" aria-disabled={isProjectPending || undefined} title={isProjectPending ? '请先选择项目' : undefined} onClick={handleImportClick}>
          {copy.importMemory}
        </button>
      </div>
    </section>
  </div>
</section>
);
}
function promptBucketLabel(bucket) { if (bucket === 'expert') return '专家能力'; if (bucket === 'recall') return '参考资料'; if (bucket === 'default_rule') return '默认规则'; return '待确认'; }
function PromptBadges({ item, active }) { const bucket = promptBucket(item); const lifecycleStatus = promptLifecycleStatus(item, active); const lifecycleLabel = promptLifecycleStatusLabel(lifecycleStatus); return (
<div className="prompt-badges"> <span>{promptBucketLabel(bucket)}</span> {item.scope === 'global' ? <span>全局可用</span> : null} {item.isPendingDraft ? <span>待确认</span> : null} {!item.isPendingDraft && lifecycleLabel ? <span>{lifecycleLabel}</span> : null}
{active ? <span className="active">强制使用</span> : null} </div> ); }
function PromptTagRow({ tags }) { if (!tags.length) return null; return ( <div className="prompt-tag-row"> {tags.map((tag) => <span key={tag}>{tag}</span>)} </div> ); }
function PromptForceAction({ item, active, actioning, onSetLaunch, onClearLaunch }) { if (active) { return ( <button type="button" className="ghost" disabled={actioning === 'launch:clear'} onClick={onClearLaunch}>取消强制</button> ); } if (!canForceLaunchPrompt(item)) return null;
return ( <button type="button" className="ghost" disabled={Boolean(actioning)} onClick={() => onSetLaunch(item)}>强制使用</button> ); }
function PromptPendingActions({ item, actioning, onContinueDraft, onDiscardDraft }) { const discardKey = item.draftKey || item.id; return (
<> <button type="button" onClick={() => onContinueDraft(item)}>继续确认</button> <button type="button" className="ghost danger" disabled={actioning === `discard:${discardKey}`} onClick={() => onDiscardDraft(item)}> {actioning === `discard:${discardKey}` ? '丢弃中...' : '丢弃'}
</button> </> ); }
function PromptSavedActions(props) { const { item, active, actioning, fallbackMode, onEdit, onCopy, onSetLaunch, onClearLaunch } = props; return (
<> <button type="button" onClick={() => onEdit(item)}>{fallbackMode ? '查看' : '编辑'}</button> <button type="button" className="ghost" disabled={actioning === `copy:${item.id}`} onClick={() => onCopy(item)}> {actioning === `copy:${item.id}` ? '复制中...' : '复制'} </button>
<PromptForceAction item={item} active={active} actioning={actioning} onSetLaunch={onSetLaunch} onClearLaunch={onClearLaunch} /> </> ); }
function PromptCardActions(props) { if (props.item.isPendingDraft) { return <PromptPendingActions {...props} />; } return <PromptSavedActions {...props} />; }
function PromptCard(props) { const { item, active } = props; return (
<article className={`prompt-card ${item.enabled === false && !item.isPendingDraft ? 'disabled' : ''} ${item.isPendingDraft ? 'pending' : ''}`}> <div className="prompt-card-head"> <h3>{item.name || '未命名'}</h3> <PromptBadges item={item} active={active} /> </div>
{item.description ? <p className="prompt-card-desc">{item.description}</p> : null} <PromptTagRow tags={item.tags} /> <p className="prompt-card-preview">{trunc(item.preview)}</p> <div className="prompt-card-actions"> <PromptCardActions {...props} /> </div> </article> ); }
function PromptAriaModal(props) { const { ariaLabel, children, className = 'modal-box', closeDisabled = false, closeOnOverlayClick = false, onClose, overlayClassName = 'modal-overlay' } = props; const [restoreFocusElement] = useState(() => {
const activeElement = document.activeElement; return activeElement instanceof HTMLElement ? activeElement : null;
}); useEffect(() => () => { if (restoreFocusElement && document.contains(restoreFocusElement)) restoreFocusElement.focus(); }, [restoreFocusElement]); const handleOpenChange = useCallback((isOpen) => {
if (!isOpen && !closeDisabled && typeof onClose === 'function') onClose();
}, [closeDisabled, onClose]); return (
<ModalOverlay className={overlayClassName} isOpen isDismissable={closeOnOverlayClick && !closeDisabled} isKeyboardDismissDisabled={closeDisabled} onOpenChange={handleOpenChange}> <Modal className={className}> <Dialog aria-label={ariaLabel}>{children}</Dialog> </Modal> </ModalOverlay> ); }
 function PromptEditorModal(props) { const { form, notice, saving, onChange, onClose, onSave } = props; const update = (key) => (event) => { const { type, checked, value } = event.target;
 onChange({ ...form, [key]: type === 'checkbox' ? checked : value, ...(key === 'priority' ? { hasPriority: true } : {}) });
}; const scopeLabel = form.scope === 'global' ? '全局可用' : '这个项目'; const scopeHint = form.scope === 'global' ? '说明：其他项目也可以使用；当前项目同名内容优先。' : '说明：只在当前项目的对话中使用。'; const previewText = form.content || form.whenToUse || form.description || '已保存，AI 会在相关场景中使用';
const advancedDebugAvailable = optionalPromptDebugStorageEnabled(); return (
<PromptAriaModal ariaLabel="编辑提示词" className="modal-box prompt-editor-modal" overlayClassName="modal-overlay prompt-modal-overlay" closeDisabled={saving} closeOnOverlayClick onClose={onClose} > <header> <div> <h2>编辑提示词</h2> <p>{scopeLabel}</p> </div> </header>
<div className="prompt-scope-copy"> <div>可用范围：{scopeLabel}</div> <PromptScopeChoice ariaLabel="可用范围" autoFocusProject scope={form.scope} onChange={(value) => onChange({ ...form, scope: value })} /> <div>{scopeHint}</div> </div> <div className="prompt-editor-grid">
<label>名称<input value={form.name} onChange={update('name')} aria-label="名称" /></label> <label className="wide">一句话描述<input value={form.description} onChange={update('description')} aria-label="一句话描述" /></label>
<label className="wide">AI 什么时候会使用它<textarea value={form.whenToUse} onChange={update('whenToUse')} aria-label="AI 什么时候会使用它" /></label> <label className="wide">AI 使用时怎么做<textarea value={form.content} onChange={update('content')} aria-label="AI 使用时怎么做" /></label>
<label className="wide">保存后 AI 会看到什么<textarea className="prompt-preview-readonly" value={previewText} aria-label="保存后 AI 会看到什么" readOnly /></label> <label className="prompt-check"><input type="checkbox" checked={form.enabled} onChange={update('enabled')} /> 启用状态</label>
</div> {advancedDebugAvailable ? ( <details className="prompt-advanced-debug"> <summary>高级调试</summary> <div className="prompt-editor-grid prompt-advanced-grid"> <label>Agent Key<input value={form.agentType} onChange={update('agentType')} aria-label="Agent Key" /></label>
<label>场景标签<input value={form.tagsText} onChange={update('tagsText')} aria-label="场景标签" /></label> <label>排序权重<input type="number" value={form.priority} onChange={update('priority')} aria-label="排序权重" /></label>
<label className="wide">match_when JSON<textarea value={form.matchWhenText} onChange={update('matchWhenText')} aria-label="match_when JSON" /></label> </div> </details>
) : null} {notice ? <div className="prompt-notice">{notice}</div> : null} <footer> <button type="button" className="ghost" onClick={onClose} disabled={saving}>取消</button> <button type="button" onClick={onSave} disabled={saving}>{saving ? '保存中...' : '保存'}</button>
</footer> </PromptAriaModal> ); }
async function buildPromptDraft({ cwd, kind, rawInput, scope, resolveLaunchPreferences }) { const launchPreferences = typeof resolveLaunchPreferences === 'function' ? await resolveLaunchPreferences(cwd) : null; return draftPromptIntent({ cwd, kind, rawInput,
sourceType: 'user_input', scope, provider: firstPresentText(launchPreferences?.modelProvider, launchPreferences?.provider), model: textValue(launchPreferences?.model),
codexModelProvider: firstPresentText(launchPreferences?.codexModelProvider, launchPreferences?.config?.codexModelProvider), }); }
function PromptKindTabs({ kind, onChange, autoFocus = false }) { return ( <div className="prompt-kind-tabs" role="tablist" aria-label="内容类型"> {PROMPT_KIND_OPTIONS.map((option) => (
<button key={option.key} type="button" role="tab" aria-selected={kind === option.key} autoFocus={autoFocus && kind === option.key} className={kind === option.key ? 'active' : ''} onClick={() => onChange(option.key)}> {option.label} </button> ))} </div> ); }
function PromptScopeChoice({ scope, onChange, ariaLabel = '草稿范围', autoFocusProject = false }) { return (
<RadioGroup aria-label={ariaLabel} className="prompt-scope-choice" onChange={onChange} value={scope === 'global' ? 'global' : 'project'} > <Radio autoFocus={autoFocusProject && scope !== 'global'} className={({ isSelected }) => (isSelected ? 'active' : '')} value="project">这个项目</Radio>
<Radio className={({ isSelected }) => (isSelected ? 'active' : '')} value="global">全局可用</Radio> </RadioGroup> ); }
function PromptDraftExamples({ draft }) { if (!draft.hitExamples.length && !draft.missExamples.length) return null; return ( <div className="prompt-draft-examples"> {draft.hitExamples.length ? (
<div> <strong>适合的问题</strong> <ul>{draft.hitExamples.map((example) => <li key={example}>{example}</li>)}</ul> </div> ) : null} {draft.missExamples.length ? ( <div> <strong>不适合的问题</strong> <ul>{draft.missExamples.map((example) => <li key={example}>{example}</li>)}</ul> </div>
) : null} </div> ); }
function PromptDraftReview({ draft }) { if (!draft) return null; return (
<article className="prompt-draft-review"> <div className="prompt-draft-title"><CheckCircle2 size={16} /> {draft.title}</div> {draft.summary ? <p>{draft.summary}</p> : null} {draft.whenToUse ? <p>{draft.whenToUse}</p> : null}
{draft.whenNotToUse ? <p>{draft.whenNotToUse}</p> : null} {draft.workflow?.length ? <ol>{draft.workflow.map((step) => <li key={step}>{step}</li>)}</ol> : null} {draft.saveBoundary ? <p><span>保存边界：</span><span>{draft.saveBoundary}</span></p> : null}
{draft.output ? <pre>{draft.output}</pre> : null} <PromptDraftExamples draft={draft} /> {draft.issues.length ? ( <div className="prompt-draft-issues"> {draft.issues.map((issue) => <div key={`${issue.code}:${issue.message}`}>{issue.message}</div>)} </div> ) : null} </article> ); }
function PromptWizardNotice({ draftNeedsRevision, notice }) { const noticeIsGuidance = notice === PROMPT_DRAFT_NOT_READY_MESSAGE || notice === PROMPT_DRAFT_REVIEW_MESSAGE; return (
<> {draftNeedsRevision ? <div className="prompt-notice">{PROMPT_DRAFT_NOT_READY_MESSAGE}</div> : null} {notice ? <div className={`prompt-notice${noticeIsGuidance ? '' : ' error'}`}>{notice}</div> : null} </> ); }
function dryRunKindLabel(kind) { if (kind === 'recall') return '参考资料'; if (kind === 'default_rule') return '默认规则'; return '专家能力'; }
function promptDryRunSummary(result, draft) { if (!result) return ''; const wouldUse = Boolean(result.would_use ?? result.wouldUse ?? result.matched ?? result.should_use); const kind = textValue(result.kind || result.action || draft?.kind || 'expert'); if (wouldUse) {
return `这条内容会参与${dryRunKindLabel(kind)}匹配。`; } return `这条内容暂不会被当前问题命中。`; }
async function runPromptDraftDryRun(options) { const { cwd, draft, question, setDryRunResult, setNotice, setWorking } = options; const cleanQuestion = textValue(question); if (!cleanQuestion) { setNotice('请先填写试问问题'); return; } if (!draft?.draftKey) {
setNotice('请先生成草稿后再验证'); return; } setWorking('dry-run'); setNotice(''); try { const result = await dryRunPromptIntent({ cwd, draftKey: draft.draftKey, kind: draft.kind, card: draft.card, question: cleanQuestion, }); setDryRunResult(result); } catch (err) {
setNotice(noticeText(err, '验证失败')); } finally { setWorking(''); } }
async function runPromptDraftGeneration(params) { const { cwd, kind, rawInput, scope, resolveLaunchPreferences, setDraft, setNotice, setWorking } = params; setWorking('draft'); setNotice(''); try {
const response = await buildPromptDraft({ cwd, kind, rawInput, scope, resolveLaunchPreferences }); setDraft(normalizeDraft(response, kind)); } catch (err) { setNotice(noticeText(err, '生成失败')); } finally { setWorking(''); } }
async function runPromptDraftCommit(options) { const { confirmGlobal, confirmRisk, cwd, draft, onSaved, setNotice, setWorking } = options; setWorking('commit'); setNotice(''); try {
await commitPromptIntent({ cwd, draftKey: draft.draftKey, scope: draft.scope, confirmGlobal, confirmRisk }); await onSaved(); } catch (err) { setNotice(noticeText(err, '保存失败')); } finally { setWorking(''); } }
function promptWizardInitialState(initialDraft) { const hasDraft = Boolean(initialDraft?.draftKey) || Boolean(initialDraft?.id) || Boolean(initialDraft?.card); return { draft: hasDraft ? initialDraft : null, dryRunQuestion: '', dryRunResult: null,
kind: initialDraft?.kind || 'expert', notice: '', rawInput: textValue(initialDraft?.rawInput), reviewConfirmed: false, scope: initialDraft?.scope || 'project', working: '', }; }
function promptWizardReducer(state, action) { switch (action.type) { case 'draft/generated': return { ...state, draft: action.draft, dryRunQuestion: '', dryRunResult: null, reviewConfirmed: false }; case 'dry-run/result': return { ...state, dryRunResult: action.result };
case 'field/set': return { ...state, [action.key]: action.value }; case 'notice/set': return { ...state, notice: action.notice }; case 'review/confirmed': return { ...state, reviewConfirmed: action.confirmed }; case 'working/set': return { ...state, working: action.working };
default: return state; } }
function PromptIntentWizardModal({ cwd, initialDraft, resolveLaunchPreferences, onClose, onSaved }) {
const [state, dispatch] = useReducer(promptWizardReducer, initialDraft, promptWizardInitialState); const { draft, dryRunQuestion, dryRunResult, kind, notice, rawInput, reviewConfirmed, scope, working } = state; const setDraft = useCallback((nextDraft) => {
dispatch({ type: 'draft/generated', draft: nextDraft }); }, []); const setDryRunResult = useCallback((result) => { dispatch({ type: 'dry-run/result', result }); }, []); const setNotice = useCallback((nextNotice) => { dispatch({ type: 'notice/set', notice: nextNotice });
}, []); const setWorking = useCallback((nextWorking) => { dispatch({ type: 'working/set', working: nextWorking }); }, []); const setReviewConfirmed = useCallback((confirmed) => { dispatch({ type: 'review/confirmed', confirmed });
}, []); const setWizardField = useCallback((key, value) => { dispatch({ type: 'field/set', key, value }); }, []);
const runDraft = async () => { const text = textValue(rawInput); if (!text) { setNotice('请先写下希望 AI 记住或使用的内容'); return; } await runPromptDraftGeneration({ cwd, kind, rawInput: text, scope, resolveLaunchPreferences, setDraft, setNotice, setWorking }); };
const commitDraft = async () => { if (!draft?.draftKey) return; if (promptDraftNeedsRevision(draft)) { setNotice(PROMPT_DRAFT_NOT_READY_MESSAGE); return; } if (draftHasReviewIssues && !reviewConfirmed) { setNotice(PROMPT_DRAFT_REVIEW_MESSAGE); return;
} await runPromptDraftCommit({ confirmGlobal: draft.scope === 'global' ? true : undefined, confirmRisk: draftHasReviewIssues && reviewConfirmed ? true : undefined, cwd, draft, onSaved, setNotice, setWorking, }); };
const draftNeedsRevision = promptDraftNeedsRevision(draft); const draftHasReviewIssues = promptDraftHasReviewIssues(draft); const canCommitDraft = Boolean(draft?.draftKey) && !draftNeedsRevision && (!draftHasReviewIssues || reviewConfirmed); const runDryRun = async () => {
await runPromptDraftDryRun({ cwd, draft, question: dryRunQuestion, setDryRunResult, setNotice, setWorking }); };
return (
<PromptAriaModal ariaLabel="添加给 AI 的内容" className="modal-box prompt-wizard-modal" overlayClassName="modal-overlay prompt-modal-overlay" closeDisabled={working === 'commit'} closeOnOverlayClick onClose={onClose} > <header> <div> <h2>添加给 AI 的内容</h2> <p>{cwd || '未知'}</p> </div>
</header> <PromptKindTabs autoFocus kind={kind} onChange={(value) => setWizardField('kind', value)} /> <PromptScopeChoice scope={scope} onChange={(value) => setWizardField('scope', value)} /> <label className="prompt-wizard-input"> 写下希望 AI 记住或使用的内容
<textarea value={rawInput} onChange={(event) => setWizardField('rawInput', event.target.value)} aria-label="写下希望 AI 记住或使用的内容" /> </label> <button type="button" onClick={runDraft} disabled={working === 'draft'}>{working === 'draft' ? '生成中...' : '帮我生成'}</button>
{working === 'draft' ? <output className="prompt-notice" aria-live="polite">正在整理内容，可能需要一点时间。</output> : null} <PromptDraftReview draft={draft} /> {draft ? (
<details className="prompt-dry-run-panel"> <summary>试问验证</summary> <div className="prompt-dry-run-body"> <label>试问问题 <textarea value={dryRunQuestion} onChange={(event) => setWizardField('dryRunQuestion', event.target.value)} aria-label="试问问题" /> </label>
<button type="button" disabled={working === 'dry-run'} onClick={runDryRun}>{working === 'dry-run' ? '验证中...' : '验证'}</button> {dryRunResult ? <div className="prompt-notice">{promptDryRunSummary(dryRunResult, draft)}</div> : null} </div> </details>
) : null} <PromptWizardNotice draftNeedsRevision={draftNeedsRevision} notice={notice} /> <PromptDraftRiskConfirmation checked={reviewConfirmed} disabled={Boolean(working)} show={draftHasReviewIssues && !draftNeedsRevision} onChange={setReviewConfirmed} /> <footer>
<button type="button" className="ghost" onClick={onClose} disabled={working === 'commit'}>关闭</button> <button type="button" onClick={commitDraft} disabled={!canCommitDraft || working === 'commit'}> {working === 'commit' ? '保存中...' : '确认保存'} </button> </footer> </PromptAriaModal>
); }
function promptDraftHasReviewIssues(draft) { return Array.isArray(draft?.issues) && draft.issues.some((issue) => textValue(issue?.severity).toLowerCase() === 'review'); }
function PromptDraftRiskConfirmation({ checked, disabled, show, onChange }) { if (!show) return null; return (
<label className="prompt-check"> <input type="checkbox" aria-label="我已确认这些风险，仍要保存" checked={checked} disabled={disabled} onChange={(event) => onChange(event.target.checked)} /> 我已确认这些风险，仍要保存 </label> ); }
