import { RPC_METHODS } from './backendRpcMethods.js';
import {
  DEFAULT_PROMPT_INTENT_KIND,
  DEFAULT_PROMPT_SOURCE_TYPE,
  cleanObject,
  hasOwn,
  normalizeOptionalString,
  normalizeString,
  requireCwd,
  requireKey,
} from './backendApiCommon.js';
import { optionalInteger } from './backendApiPayloadWorkflowDag.js';

/** @typedef {Record<string, unknown>} WorkflowPayload */

/** @param {unknown} params */
function promptWritePayload(params) {
  const method = RPC_METHODS.PROMPTS_WRITE;
  const payload = requireKey(method, requireCwd(method, params), 'name');
  const promptID = normalizeString(payload.id) || normalizeString(payload.key);
  if (!promptID) throw new Error(`${method}: id or key is required`);
  return cleanObject({
    cwd: payload.cwd,
    id: promptID,
    name: payload.name,
    description: normalizeString(payload.description),
    agentType: normalizeString(payload.agentType || payload.agent_key || payload.agentKey) || 'main',
    priority: optionalInteger(payload.priority),
    when_to_use: normalizeString(payload.when_to_use ?? payload.whenToUse),
    content: hasOwn(payload, 'content') ? normalizeOptionalString(payload.content) : undefined,
    tags: Array.isArray(payload.tags) ? payload.tags : [],
    enabled: hasOwn(payload, 'enabled') ? Boolean(payload.enabled) : undefined,
    scope: normalizeString(payload.scope) || 'project',
    match_when: promptMatchWhen(payload),
  });
}

/** @param {WorkflowPayload} payload */
function promptMatchWhen(payload) {
  if (hasOwn(payload, 'match_when')) return payload.match_when;
  if (hasOwn(payload, 'matchWhen')) return payload.matchWhen;
  return undefined;
}

/** @param {unknown} params */
function promptDeletePayload(params) {
  const method = RPC_METHODS.PROMPTS_DELETE;
  const payload = requireKey(method, requireCwd(method, params), 'id');
  return cleanObject({
    cwd: payload.cwd,
    id: payload.id,
    scope: normalizeString(payload.scope) || 'project',
  });
}

/** @param {WorkflowPayload} payload */
function promptIntentRawInput(payload) {
  const rawInput = normalizeString(payload.raw_input ?? payload.rawInput);
  if (!rawInput) throw new Error(`${RPC_METHODS.PROMPT_INTENTS_DRAFT}: raw_input is required`);
  return rawInput;
}

/** @param {WorkflowPayload} payload */
function promptIntentEnableGlobal(payload) {
  const scope = normalizeString(payload.scope);
  return payload.enable_global ?? payload.enableGlobal ?? (scope === 'global' ? true : undefined);
}

/** @param {WorkflowPayload} payload */
function promptIntentSourceFields(payload) {
  return {
    source_type: normalizeString(payload.source_type ?? payload.sourceType) || DEFAULT_PROMPT_SOURCE_TYPE,
    source_url: normalizeString(payload.source_url ?? payload.sourceUrl),
    license_hint: normalizeString(payload.license_hint ?? payload.licenseHint),
  };
}

/** @param {WorkflowPayload} payload */
function promptProviderFields(payload) {
  return {
    provider: normalizeString(payload.provider ?? payload.modelProvider),
    model: normalizeString(payload.model),
    model_provider: normalizeString(payload.model_provider ?? payload.codexModelProvider),
  };
}

/** @param {unknown} params */
function promptIntentDraftPayload(params) {
  const payload = /** @type {WorkflowPayload & { cwd: string }} */ (
    requireCwd(RPC_METHODS.PROMPT_INTENTS_DRAFT, params)
  );
  return cleanObject({
    cwd: payload.cwd,
    kind: normalizeString(payload.kind) || DEFAULT_PROMPT_INTENT_KIND,
    raw_input: promptIntentRawInput(payload),
    ...promptIntentSourceFields(payload),
    enable_global: promptIntentEnableGlobal(payload),
    ...promptProviderFields(payload),
  });
}

/** @param {string} method @param {unknown} params */
function memoryConsolidationPayload(method, params) {
  const payload = /** @type {WorkflowPayload & { cwd: string }} */ (requireCwd(method, params));
  return cleanObject({
    cwd: payload.cwd,
    ...promptProviderFields(payload),
  });
}

/** @param {string} method @param {unknown} params @returns {WorkflowPayload & { cwd: string, draft_key: string }} */
function promptDraftKeyPayload(method, params) {
  const payload = /** @type {WorkflowPayload & { cwd: string }} */ (requireCwd(method, params));
  const draftKey = normalizeString(payload.draft_key ?? payload.draftKey);
  if (!draftKey) throw new Error(`${method}: draft_key is required`);
  return { ...payload, draft_key: draftKey };
}

/** @param {unknown} params */
function promptIntentCommitPayload(params) {
  const payload = promptDraftKeyPayload(RPC_METHODS.PROMPT_INTENTS_COMMIT, params);
  const scope = normalizeString(payload.scope);
  return cleanObject({
    cwd: payload.cwd,
    draft_key: payload.draft_key,
    confirm_risk: payload.confirm_risk ?? payload.confirmRisk,
    enable_global: payload.enable_global ?? payload.enableGlobal ?? (scope === 'global' ? true : undefined),
    confirm_global: payload.confirm_global ?? payload.confirmGlobal,
  });
}

/** @param {unknown} params */
function promptIntentDiscardPayload(params) {
  const payload = promptDraftKeyPayload(RPC_METHODS.PROMPT_INTENTS_DISCARD, params);
  return { cwd: payload.cwd, draft_key: payload.draft_key };
}

/** @param {unknown} params */
function promptIntentDryRunPayload(params) {
  const payload = promptDraftKeyPayload(RPC_METHODS.PROMPT_INTENTS_DRY_RUN, params);
  const question = normalizeString(payload.question);
  if (!question) throw new Error(`${RPC_METHODS.PROMPT_INTENTS_DRY_RUN}: question is required`);
  return cleanObject({
    cwd: payload.cwd,
    draft_key: payload.draft_key,
    kind: normalizeString(payload.kind),
    card: payload.card,
    question,
  });
}

/** @param {string} method @param {unknown} params */
function personalizationProfilePayload(method, params) {
  const payload = /** @type {WorkflowPayload & { cwd: string }} */ (requireCwd(method, params));
  if (method === RPC_METHODS.PERSONALIZATION_PROFILE_GET) return { cwd: payload.cwd };
  if (!payload.profile || typeof payload.profile !== 'object' || Array.isArray(payload.profile)) {
    throw new Error(`${method}: profile must be an object`);
  }
  return { cwd: payload.cwd, profile: payload.profile };
}

/** @param {string} method @param {unknown} params */
function promptSectionPayload(method, params) {
  return requireKey(method, requireCwd(method, params), 'prompt_id');
}

export {
  promptWritePayload,
  promptMatchWhen,
  promptDeletePayload,
  promptIntentDraftPayload,
  promptIntentRawInput,
  promptIntentEnableGlobal,
  promptIntentSourceFields,
  promptProviderFields,
  memoryConsolidationPayload,
  promptDraftKeyPayload,
  promptIntentCommitPayload,
  promptIntentDiscardPayload,
  promptIntentDryRunPayload,
  personalizationProfilePayload,
  promptSectionPayload,
};
