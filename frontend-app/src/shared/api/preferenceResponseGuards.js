import { getPreference } from './backendApi.js';
import { parseStrictDiagnosticPreviewJSON } from './support/safeDiagnosticPreview.js';

const STRING_PREFERENCE_KEYS = new Set([
  'settings.provider.codex.codexHome',
  'settings.provider.codex.codexInstanceKey',
  'settings.provider.codex.codexModelProvider',
  'settings.provider.codex.model',
  'settings.activePromptKey',
]);

const CODEX_EFFORT_VALUES = new Set(['xhigh', 'high', 'medium', 'low', 'none']);
const PERSONALITY_VALUES = new Set(['pragmatic', 'friendly', 'none']);
const SUMMARY_VALUES = new Set(['detailed', 'auto', 'concise', 'none']);
const APPROVAL_POLICY_VALUES = new Set(['on-request', 'untrusted', 'on-failure', 'never']);
const LEGACY_SANDBOX_VALUES = new Set([
  'workspace-write',
  'read-only',
  'danger-full-access',
  'workspaceWrite',
  'readOnly',
  'dangerFullAccess',
]);

function invalidPreference(key, detail) {
  throw new Error(`invalid UI preference response for ${key}: ${detail}`);
}

function comparePreferenceKeys(left, right) {
  if (left < right) return -1;
  if (left > right) return 1;
  return 0;
}

function hasExactKeys(value, keys) {
  if (!value || typeof value !== 'object' || Array.isArray(value)) return false;
  const actual = Object.keys(value).sort(comparePreferenceKeys);
  const expected = [...keys].sort(comparePreferenceKeys);
  return actual.length === expected.length && actual.every((key, index) => key === expected[index]);
}

function isExactTombstone(value) {
  return hasExactKeys(value, ['cleared']) && value.cleared === true;
}

function isAbsolutePath(value) {
  return typeof value === 'string'
    && value.length > 0
    && (value.startsWith('/') || /^[a-zA-Z]:[\\/]/.test(value) || /^\\\\[^\\]+\\[^\\]+/.test(value));
}

function assertSandboxObject(key, value) {
  if (!value || typeof value !== 'object' || Array.isArray(value)) invalidPreference(key, 'sandbox must be an object or supported legacy string');
  if (value.type === 'dangerFullAccess') {
    if (!hasExactKeys(value, ['type'])) invalidPreference(key, 'dangerFullAccess sandbox has unexpected fields');
    return;
  }
  if (value.type === 'workspaceWrite') {
    if (!hasExactKeys(value, ['type', 'writableRoots', 'networkAccess'])) invalidPreference(key, 'workspaceWrite sandbox fields are invalid');
    if (!Array.isArray(value.writableRoots) || !value.writableRoots.every(isAbsolutePath)) invalidPreference(key, 'workspaceWrite writableRoots must be absolute path strings');
    if (typeof value.networkAccess !== 'boolean') invalidPreference(key, 'workspaceWrite networkAccess must be boolean');
    return;
  }
  if (value.type === 'readOnly' && hasExactKeys(value, ['type'])) return;
  if (value.type === 'readOnly' && hasExactKeys(value, ['type', 'access'])) {
    const access = value.access;
    if (!hasExactKeys(access, ['type', 'readableRoots', 'includePlatformDefaults'])
      || access.type !== 'restricted'
      || !Array.isArray(access.readableRoots)
      || !access.readableRoots.every(isAbsolutePath)
      || access.includePlatformDefaults !== true) {
      invalidPreference(key, 'restricted readOnly sandbox fields are invalid');
    }
    return;
  }
  invalidPreference(key, 'unsupported sandbox shape');
}

function parseSandboxJson(key, value) {
  try {
    return parseStrictDiagnosticPreviewJSON(value, `preference ${key}`);
  } catch {
    invalidPreference(key, 'sandbox string must be a supported mode or valid sandbox JSON');
  }
}

function assertSandbox(key, value) {
  if (typeof value !== 'string') {
    assertSandboxObject(key, value);
    return;
  }
  if (LEGACY_SANDBOX_VALUES.has(value)) return;
  assertSandboxObject(key, parseSandboxJson(key, value));
}

function assertContextThresholds(key, value) {
  if (!Array.isArray(value) || value.length !== 3 || !value.every(Number.isInteger)) {
    invalidPreference(key, 'thresholds must be an integer tuple of length 3');
  }
  const [warn, danger, critical] = value;
  if (!(warn > 0 && warn < danger && danger < critical && critical <= 100)) {
    invalidPreference(key, 'thresholds must satisfy 0 < warn < danger < critical <= 100');
  }
}

function assertEnum(key, value, allowed) {
  if (typeof value !== 'string' || !allowed.has(value)) invalidPreference(key, 'value is outside the accepted enum');
}

function assertKnownPreferenceKey(key) {
  if (STRING_PREFERENCE_KEYS.has(key)) return;
  switch (key) {
    case 'stallThresholdSec':
    case 'contextUsageAlerts.thresholds':
    case 'settings.provider.active':
    case 'settings.provider.codex.effort':
    case 'settings.provider.codex.personality':
    case 'settings.provider.codex.sandbox':
    case 'settings.provider.codex.summary':
    case 'settings.provider.codex.approvalPolicy':
    case 'settings.showInjectedPromptInChat':
      return;
    default:
      throw new Error(`unknown UI preference response key: ${key}`);
  }
}

function assertPreferenceResponseShape(key, value, options = {}) {
  assertKnownPreferenceKey(key);
  if (value === null) return value;
  if (options.allowTombstone === true && isExactTombstone(value)) return value;
  if (isExactTombstone(value)) invalidPreference(key, 'tombstone is only valid for scoped preference lookup');

  if (!STRING_PREFERENCE_KEYS.has(key)) {
    switch (key) {
      case 'stallThresholdSec':
        if (!Number.isInteger(value) || value < 30) invalidPreference(key, 'value must be an integer >= 30');
        break;
      case 'contextUsageAlerts.thresholds':
        assertContextThresholds(key, value);
        break;
      case 'settings.provider.active':
        assertEnum(key, value, new Set(['codex']));
        break;
      case 'settings.provider.codex.effort':
        assertEnum(key, value, CODEX_EFFORT_VALUES);
        break;
      case 'settings.provider.codex.personality':
        assertEnum(key, value, PERSONALITY_VALUES);
        break;
      case 'settings.provider.codex.sandbox':
        assertSandbox(key, value);
        break;
      case 'settings.provider.codex.summary':
        assertEnum(key, value, SUMMARY_VALUES);
        break;
      case 'settings.provider.codex.approvalPolicy':
        assertEnum(key, value, APPROVAL_POLICY_VALUES);
        break;
      case 'settings.showInjectedPromptInChat':
        if (typeof value !== 'boolean') invalidPreference(key, 'value must be boolean');
        break;
      default:
        throw new Error(`unknown UI preference response key: ${key}`);
    }
    return value;
  }
  if (typeof value !== 'string') {
    throw new Error(`invalid UI preference response for ${key}: value must be a string`);
  }
  return value;
}

async function getValidatedPreference(params, options = {}) {
  const result = await getPreference(params);
  assertPreferenceResponseShape(params.key, result, options);
  return result;
}

export { assertPreferenceResponseShape, getValidatedPreference };
