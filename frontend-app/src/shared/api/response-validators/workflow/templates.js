// @ts-check

import {
  assertBackendResponseObject,
  assertOnlyResponseKeys,
  assertResponseRecord,
  hasOwn,
  normalizeString,
} from '../shared.js';
import {
  requireResponseString,
  requireResponseIdentity,
  requireResponseInteger,
  requireResponseBoolean,
  validateResponseStringArray,
  validateNullableResponseStringArray,
  validateOptionalResponseStrings,
} from './dag.js';

const WORKFLOW_TEMPLATES_LIST_RESPONSE_KEYS = new Set(['templates']);
const WORKFLOW_TEMPLATE_RESPONSE_KEYS = new Set(['template']);
const WORKFLOW_TEMPLATE_DRAFT_RESPONSE_KEYS = new Set(['draft']);
const WORKFLOW_TEMPLATE_SAVE_RESPONSE_KEYS = new Set(['template']);
const WORKFLOW_TEMPLATE_SUMMARY_KEYS = new Set(['id', 'version', 'title', 'description', 'category', 'business_flow', 'output_types', 'tags', 'estimated_nodes', 'requires_review', 'supports_schedule', 'final_node_key', 'trust', 'compatibility', 'available_versions']);
const WORKFLOW_TEMPLATE_KEYS = new Set(['id', 'version', 'title', 'description', 'category', 'business_flow', 'output_types', 'tags', 'estimated_nodes', 'requires_review', 'supports_schedule', 'trust', 'compatibility', 'ui_schema', 'dag_template', 'validation', 'final_output']);
const WORKFLOW_LOCALIZED_TEXT_KEYS = new Set(['zh', 'en']);
const WORKFLOW_TRUST_KEYS = new Set(['level', 'source']);
const WORKFLOW_COMPATIBILITY_KEYS = new Set(['runtime', 'node_types', 'required_capabilities']);
const WORKFLOW_UI_FIELD_KEYS = new Set(['key', 'type', 'required', 'label', 'placeholder', 'help', 'options']);
const WORKFLOW_UI_OPTION_KEYS = new Set(['value', 'label']);
const WORKFLOW_DAG_TEMPLATE_KEYS = new Set(['dag_key_template', 'title_template', 'description_template', 'trigger', 'final_node_key', 'nodes']);
const WORKFLOW_NODE_TEMPLATE_KEYS = new Set(['node_key', 'title', 'node_type', 'assigned_to', 'depends_on', 'config']);
const WORKFLOW_VALIDATION_KEYS = new Set(['sharedfile_prefix', 'sharedfile_prefixes', 'require_review_before_final', 'require_final_node_key']);
const WORKFLOW_FINAL_OUTPUT_KEYS = new Set(['node_key', 'kind', 'path_template']);
const WORKFLOW_DAG_DRAFT_KEYS = new Set(['template_id', 'template_version', 'dag_key', 'title', 'description', 'trigger', 'final_node_key', 'review_node_key', 'nodes', 'final_output', 'metadata']);

/** @param {string} method @param {unknown} response @param {string} label */
function validateWorkflowLocalizedText(method, response, label) {
  const text = assertResponseRecord(method, response, label);
  assertOnlyResponseKeys(method, text, WORKFLOW_LOCALIZED_TEXT_KEYS, label);
  requireResponseString(method, text, label, 'zh');
  validateOptionalResponseStrings(method, text, label, ['en']);
}

/** @param {string} method @param {unknown} response @param {string} label */
function validateWorkflowTrust(method, response, label) {
  const trust = assertResponseRecord(method, response, label);
  assertOnlyResponseKeys(method, trust, WORKFLOW_TRUST_KEYS, label);
  for (const key of ['level', 'source']) requireResponseString(method, trust, label, key);
}

/** @param {string} method @param {unknown} response @param {string} label */
function validateWorkflowCompatibility(method, response, label) {
  const compatibility = assertResponseRecord(method, response, label);
  assertOnlyResponseKeys(method, compatibility, WORKFLOW_COMPATIBILITY_KEYS, label);
  requireResponseString(method, compatibility, label, 'runtime');
  validateResponseStringArray(method, compatibility.node_types, `${label}.node_types`);
  validateResponseStringArray(method, compatibility.required_capabilities, `${label}.required_capabilities`);
}

/** @param {string} method @param {unknown} response @param {string} label */
function validateWorkflowUIOption(method, response, label) {
  const option = assertResponseRecord(method, response, label);
  assertOnlyResponseKeys(method, option, WORKFLOW_UI_OPTION_KEYS, label);
  requireResponseString(method, option, label, 'value');
  validateWorkflowLocalizedText(method, option.label, `${label}.label`);
}

/** @param {string} method @param {unknown} response @param {string} label */
function validateWorkflowUIField(method, response, label) {
  const field = assertResponseRecord(method, response, label);
  assertOnlyResponseKeys(method, field, WORKFLOW_UI_FIELD_KEYS, label);
  for (const key of ['key', 'type']) requireResponseString(method, field, label, key);
  requireResponseBoolean(method, field, label, 'required');
  for (const key of ['label', 'placeholder', 'help']) validateWorkflowLocalizedText(method, field[key], `${label}.${key}`);
  if (hasOwn(field, 'options')) {
    if (!Array.isArray(field.options)) throw new TypeError(`${method} response ${label}.options must be an array`);
    /** @type {unknown[]} */ (field.options).forEach((option, index) => validateWorkflowUIOption(method, option, `${label}.options[${index}]`));
  }
}

/** @param {string} method @param {unknown} response @param {string} label */
function validateWorkflowNodeTemplate(method, response, label) {
  const node = assertResponseRecord(method, response, label);
  assertOnlyResponseKeys(method, node, WORKFLOW_NODE_TEMPLATE_KEYS, label);
  for (const key of ['node_key', 'title', 'node_type', 'assigned_to']) requireResponseString(method, node, label, key);
  validateResponseStringArray(method, node.depends_on, `${label}.depends_on`);
  assertResponseRecord(method, node.config, `${label}.config`);
}

/** @param {string} method @param {unknown} response @param {string} label */
function validateWorkflowFinalOutput(method, response, label) {
  const output = assertResponseRecord(method, response, label);
  assertOnlyResponseKeys(method, output, WORKFLOW_FINAL_OUTPUT_KEYS, label);
  for (const key of ['node_key', 'kind', 'path_template']) requireResponseString(method, output, label, key);
}

/** @param {string} method @param {unknown} response @param {string} label */
function validateWorkflowValidationRule(method, response, label) {
  const rule = assertResponseRecord(method, response, label);
  assertOnlyResponseKeys(method, rule, WORKFLOW_VALIDATION_KEYS, label);
  for (const key of ['require_review_before_final', 'require_final_node_key']) requireResponseBoolean(method, rule, label, key);
  validateOptionalResponseStrings(method, rule, label, ['sharedfile_prefix']);
  if (hasOwn(rule, 'sharedfile_prefixes')) validateResponseStringArray(method, rule.sharedfile_prefixes, `${label}.sharedfile_prefixes`);
}

/** @param {string} method @param {unknown} response @param {string} label */
function validateWorkflowDagTemplate(method, response, label) {
  const dag = assertResponseRecord(method, response, label);
  assertOnlyResponseKeys(method, dag, WORKFLOW_DAG_TEMPLATE_KEYS, label);
  for (const key of ['dag_key_template', 'title_template', 'description_template', 'trigger', 'final_node_key']) requireResponseString(method, dag, label, key);
  if (!Array.isArray(dag.nodes)) throw new TypeError(`${method} response ${label}.nodes must be an array`);
  /** @type {unknown[]} */ (dag.nodes).forEach((node, index) => validateWorkflowNodeTemplate(method, node, `${label}.nodes[${index}]`));
}

/** @param {string} method @param {unknown} response @param {string} label */
function validateWorkflowTemplateSummary(method, response, label) {
  const template = assertResponseRecord(method, response, label);
  assertOnlyResponseKeys(method, template, WORKFLOW_TEMPLATE_SUMMARY_KEYS, label);
  requireResponseIdentity(method, template, label, 'id');
  for (const key of ['version', 'estimated_nodes']) requireResponseInteger(method, template, label, key);
  for (const key of ['category', 'business_flow', 'final_node_key']) requireResponseString(method, template, label, key);
  for (const key of ['requires_review', 'supports_schedule']) requireResponseBoolean(method, template, label, key);
  validateWorkflowLocalizedText(method, template.title, `${label}.title`);
  validateWorkflowLocalizedText(method, template.description, `${label}.description`);
  validateResponseStringArray(method, template.output_types, `${label}.output_types`);
  validateNullableResponseStringArray(method, template.tags, `${label}.tags`);
  validateWorkflowTrust(method, template.trust, `${label}.trust`);
  validateWorkflowCompatibility(method, template.compatibility, `${label}.compatibility`);
  if (!Array.isArray(template.available_versions) || /** @type {unknown[]} */ (template.available_versions).some((version) => !Number.isInteger(version))) {
    throw new TypeError(`${method} response ${label}.available_versions must be an array of integers`);
  }
}

/** @param {string} method @param {unknown} response @param {string} label */
function validateWorkflowTemplate(method, response, label) {
  const template = assertResponseRecord(method, response, label);
  assertOnlyResponseKeys(method, template, WORKFLOW_TEMPLATE_KEYS, label);
  requireResponseIdentity(method, template, label, 'id');
  for (const key of ['version', 'estimated_nodes']) requireResponseInteger(method, template, label, key);
  for (const key of ['category', 'business_flow']) requireResponseString(method, template, label, key);
  for (const key of ['requires_review', 'supports_schedule']) requireResponseBoolean(method, template, label, key);
  validateWorkflowLocalizedText(method, template.title, `${label}.title`);
  validateWorkflowLocalizedText(method, template.description, `${label}.description`);
  validateResponseStringArray(method, template.output_types, `${label}.output_types`);
  validateNullableResponseStringArray(method, template.tags, `${label}.tags`);
  validateWorkflowTrust(method, template.trust, `${label}.trust`);
  validateWorkflowCompatibility(method, template.compatibility, `${label}.compatibility`);
  if (!Array.isArray(template.ui_schema)) throw new TypeError(`${method} response ${label}.ui_schema must be an array`);
  /** @type {unknown[]} */ (template.ui_schema).forEach((field, index) => validateWorkflowUIField(method, field, `${label}.ui_schema[${index}]`));
  validateWorkflowDagTemplate(method, template.dag_template, `${label}.dag_template`);
  validateWorkflowValidationRule(method, template.validation, `${label}.validation`);
  validateWorkflowFinalOutput(method, template.final_output, `${label}.final_output`);
}

/** @param {string} method @param {unknown} response @param {string} label */
function validateWorkflowTemplateDraft(method, response, label) {
  const draft = assertResponseRecord(method, response, label);
  assertOnlyResponseKeys(method, draft, WORKFLOW_DAG_DRAFT_KEYS, label);
  for (const key of ['template_id', 'dag_key']) requireResponseIdentity(method, draft, label, key);
  requireResponseInteger(method, draft, label, 'template_version');
  for (const key of ['title', 'description', 'trigger', 'final_node_key', 'review_node_key']) requireResponseString(method, draft, label, key);
  if (!Array.isArray(draft.nodes)) throw new TypeError(`${method} response ${label}.nodes must be an array`);
  /** @type {unknown[]} */ (draft.nodes).forEach((node, index) => validateWorkflowNodeTemplate(method, node, `${label}.nodes[${index}]`));
  validateWorkflowFinalOutput(method, draft.final_output, `${label}.final_output`);
  assertResponseRecord(method, draft.metadata, `${label}.metadata`);
}

/** @param {string} method @param {unknown} response */
function validateWorkflowTemplatesListResponse(method, response) {
  const value = assertBackendResponseObject(method, response);
  assertOnlyResponseKeys(method, value, WORKFLOW_TEMPLATES_LIST_RESPONSE_KEYS, 'body');
  if (!Array.isArray(value.templates)) throw new TypeError(`${method} response body.templates must be an array`);
  /** @type {unknown[]} */ (value.templates).forEach((template, index) => validateWorkflowTemplateSummary(method, template, `body.templates[${index}]`));
  return value;
}

/**
 * @param {string} method
 * @param {unknown} response
 */

function validateWorkflowTemplateResponse(method, response) {
  const value = assertBackendResponseObject(method, response);
  assertOnlyResponseKeys(method, value, WORKFLOW_TEMPLATE_RESPONSE_KEYS, 'body');
  validateWorkflowTemplate(method, value.template, 'body.template');
  return value;
}

/** @param {string} method @param {unknown} response */
function validateWorkflowTemplateDraftResponse(method, response) {
  const value = assertBackendResponseObject(method, response);
  assertOnlyResponseKeys(method, value, WORKFLOW_TEMPLATE_DRAFT_RESPONSE_KEYS, 'body');
  validateWorkflowTemplateDraft(method, value.draft, 'body.draft');
  return value;
}

/** @param {string} method @param {unknown} response */
function validateWorkflowTemplateSaveResponse(method, response) {
  const value = assertBackendResponseObject(method, response);
  assertOnlyResponseKeys(method, value, WORKFLOW_TEMPLATE_SAVE_RESPONSE_KEYS, 'body');
  validateWorkflowTemplateSummary(method, value.template, 'body.template');
  return value;
}

/** @param {string} method @param {unknown} response @param {unknown} request */
function validateWorkflowTemplateRollbackResponse(method, response, request) {
  const value = validateWorkflowTemplateSaveResponse(method, response);
  const payload = assertResponseRecord(method, request, 'request');
  const templateId = normalizeString(payload.templateId);
  const version = payload.version;
  if (!templateId) throw new TypeError(`${method} request templateId is required for response correlation`);
  if (!Number.isInteger(version) || /** @type {number} */ (version) <= 0) {
    throw new TypeError(`${method} request version must be a positive integer for response correlation`);
  }
  const template = assertResponseRecord(method, value.template, 'body.template');
  if (template.id !== templateId) throw new TypeError(`${method} response template.id must match request templateId`);
  if (template.version !== version) throw new TypeError(`${method} response template.version must match request version`);
  return value;
}

export { validateWorkflowTemplateDraftResponse, validateWorkflowTemplateResponse, validateWorkflowTemplateRollbackResponse, validateWorkflowTemplateSaveResponse, validateWorkflowTemplatesListResponse };
