export const EVENT_TYPED_WIRE_METHODS = Object.freeze([
  'ui/state/changed',
  'ui/thread/changed',
  'ui/sidebar/changed',
  'turn/started',
  'turn/completed',
  'turn/interrupted',
  'turn/stalled',
  'turn/resumed',
  'turn/output/delta',
  'item/agentMessage/delta',
  'item/reasoning/textDelta',
  'item/commandExecution/outputDelta',
  'item/tool/call',
  'item/completed',
  'item/commandExecution/requestApproval',
  'item/fileChange/requestApproval',
  'skill/requestApproval',
  'approval/resolved',
  'thread/started',
  'thread/stopped',
  'thread/messages/page',
  'thread/compacted',
  'thread/tokenusage/updated',
  'skills/changed',
  'ui/preferences/changed',
  'ui/thread/patch',
  'ui/shared-files/changed',
  'ui/memory/changed',
  'ui/prompts/changed',
  'agent/launched',
  'agent/stopped',
  'agent/recovering',
  'agent/failed',
  'agent/runtime/reported',
  'task/node/statusChanged',
  'cron/job/runStateChanged',
]);

export const EVENT_RAW_WIRE_METHODS = Object.freeze([
  'error',
  'configWarning',
  'deprecationNotice',
  'approval/request',
  'thread/name/updated',
  'thread/tokenUsage/updated',
  'thread/tokenusage/updated',
]);

export const EVENT_RAW_WIRE_PREFIXES = Object.freeze([
  'item/',
  'turn/plan/',
  'turn/diff/',
  'agent/event/',
  'account/',
  'app/list/',
  'fuzzyFileSearch/',
]);

export const EVENT_RAW_WIRE_SUFFIXES = Object.freeze([
  '/requestApproval',
]);

export const EVENT_COMPAT_WIRE_PREFIXES = Object.freeze([
  'workspace/run/',
]);

// Compatibility alias for any existing caller that imports the old name.
export const EVENT_WIRE_METHODS = EVENT_TYPED_WIRE_METHODS;
