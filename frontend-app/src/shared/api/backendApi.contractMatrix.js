import { RPC_METHODS } from './backendApi.js';

export const RPC_RISK_LEVELS = Object.freeze({
  P0: 'P0',
  P1: 'P1',
  P2: 'P2',
});

export const RPC_RESPONSE_BEHAVIORS = Object.freeze({
  MEANINGFUL: 'meaningful',
  STATUS: 'status',
  PASSTHROUGH: 'passthrough',
});

const P0_KEYS = new Set([
  'UI_CODE_SAVE',
  'UI_MEMORY_ENTRY_UPSERT',
  'UI_MEMORY_ENTRY_DELETE',
  'UI_MEMORY_AUTO_DREAM_SET_INTENT',
  'UI_MEMORY_ENTRY_MERGE',
  'UI_MEMORY_SIMILARITY_IGNORE',
  'UI_MEMORY_SIMILARITY_CONSOLIDATE_ALL',
  'UI_MEMORY_SIMILARITY_CONSOLIDATE_ALL_START',
  'PROMPTS_WRITE',
  'PROMPTS_DELETE',
  'PROMPT_INTENTS_COMMIT',
  'PROMPT_SECTIONS_WRITE',
  'PROMPT_SECTIONS_DELETE',
  'DASHBOARD_DAG_START',
  'DASHBOARD_DAG_DISPATCH_NODE',
  'DASHBOARD_DAG_TERMINATE',
  'DASHBOARD_DAG_DELETE',
  'DASHBOARD_DAG_APPLY_OPS',
  'SKILLS_LOCAL_DELETE',
  'SKILLS_LOCAL_WRITE',
  'SKILLS_LOCAL_IMPORT_DIR',
  'SKILLS_CREATE',
  'SKILLS_RESOLUTION_APPLY',
  'THREAD_START',
  'THREAD_ARCHIVE',
  'THREAD_UNARCHIVE',
  'THREAD_DELETE',
  'THREAD_CONFIG_SET',
  'THREAD_COMPACT_START',
  'THREAD_RECOVER',
  'THREAD_NAME_SET',
  'TURN_START',
  'TURN_INTERRUPT',
  'TURN_FORCE_COMPLETE',
  'APPROVAL_RESPOND',
]);

const CUSTOM_DECODER_METHODS = new Set([
  RPC_METHODS.THREAD_START,
  RPC_METHODS.TURN_START,
  RPC_METHODS.TURN_INTERRUPT,
]);

const PARAMS_EMPTY_OBJECT_ONLY = new Set([
  RPC_METHODS.DASHBOARD_SHARED_FILES,
]);

function isMutatingKey(key) {
  return /(^|_)(WRITE|DELETE|SAVE|APPLY|DISPATCH|START|RUN_ONCE|SET_ACTIVE|SET|UPSERT|MERGE|IGNORE|CONSOLIDATE|ARCHIVE|UNARCHIVE|RECOVER|RESPOND|IMPORT|CREATE|UPDATE|INSTALL|DOWNLOAD)(_|$)/.test(key);
}

function riskFor(key) {
  if (P0_KEYS.has(key)) return RPC_RISK_LEVELS.P0;
  if (isMutatingKey(key)) return RPC_RISK_LEVELS.P1;
  return RPC_RISK_LEVELS.P2;
}

function responseBehaviorFor(key) {
  if (key === 'TURN_INTERRUPT') return RPC_RESPONSE_BEHAVIORS.PASSTHROUGH;
  if (/(^|_)(GET|READ|LIST|STATUS|CHECK|PREVIEW|SUGGEST|RESOLVE)(_|$)/.test(key)) {
    return RPC_RESPONSE_BEHAVIORS.MEANINGFUL;
  }
  return RPC_RESPONSE_BEHAVIORS.STATUS;
}

function notesFor(method, risk) {
  const notes = [];
  if (PARAMS_EMPTY_OBJECT_ONLY.has(method)) notes.push('params:{}-only');
  if (CUSTOM_DECODER_METHODS.has(method)) notes.push('custom-decoder');
  if (risk !== RPC_RISK_LEVELS.P2) notes.push('response-shape-required');
  return notes;
}

export const RPC_CONTRACT_MATRIX = Object.freeze(
  Object.entries(RPC_METHODS).map(([key, method]) => {
    const risk = riskFor(key);
    return Object.freeze({
      key,
      method,
      risk,
      responseBehavior: responseBehaviorFor(key),
      contractNotes: Object.freeze(notesFor(method, risk)),
    });
  }),
);
