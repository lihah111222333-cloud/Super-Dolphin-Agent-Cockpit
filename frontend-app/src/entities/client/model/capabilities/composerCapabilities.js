export const CAPABILITY_READY = 'ready';
export const CAPABILITY_UNVERIFIED = 'unverified';
export const CAPABILITY_STALE = 'stale';

const CAPABILITY_KINDS = new Set(['skill', 'mcp_tool']);
const CAPABILITY_AVAILABILITIES = new Set([
  CAPABILITY_READY,
  CAPABILITY_UNVERIFIED,
  CAPABILITY_STALE,
]);

function requiredText(value, field) {
  if (typeof value !== 'string' || !value.trim()) {
    throw new TypeError(`composer capability ${field} must be a non-empty string`);
  }
  return value.trim();
}

function text(value, field) {
  if (typeof value !== 'string') {
    throw new TypeError(`composer capability ${field} must be a string`);
  }
  return value.trim();
}

function normalizedAvailability(raw, defaultAvailability) {
  let availability = raw.availability;
  if (availability === undefined) availability = defaultAvailability;
  if (!CAPABILITY_AVAILABILITIES.has(availability)) {
    throw new TypeError(`composer capability availability is unsupported: ${availability}`);
  }
  return availability;
}

function normalizedSkillRef(raw, name) {
  const ref = raw.ref;
  if (!ref || typeof ref !== 'object' || Array.isArray(ref)) {
    throw new TypeError('composer capability skill ref must be an object');
  }
  const refName = requiredText(ref.name, 'skill ref name');
  if (refName !== name) {
    throw new TypeError('composer capability skill ref name must match capability name');
  }
  const scope = requiredText(ref.scope, 'skill ref scope');
  if (scope !== 'project' && scope !== 'personal') {
    throw new TypeError(`composer capability skill ref scope is unsupported: ${scope}`);
  }
  return {
    name: refName,
    scope,
    personalType: text(ref.personalType, 'skill ref personalType'),
    path: requiredText(ref.path, 'skill ref path'),
  };
}

export function normalizeComposerCapability(raw, defaultAvailability = CAPABILITY_READY) {
  if (!raw || typeof raw !== 'object' || Array.isArray(raw)) {
    throw new TypeError('composer capability must be an object');
  }
  const kind = requiredText(raw.kind, 'kind');
  if (!CAPABILITY_KINDS.has(kind)) {
    throw new TypeError(`composer capability kind is unsupported: ${kind}`);
  }
  const name = requiredText(raw.name, 'name');
  const capability = {
    kind,
    key: requiredText(raw.key, 'key'),
    name,
    label: requiredText(raw.label, 'label'),
    availability: normalizedAvailability(raw, defaultAvailability),
  };
  if (kind === 'skill') {
    return { ...capability, ref: normalizedSkillRef(raw, name) };
  }
  return {
    ...capability,
    serverName: requiredText(raw.serverName, 'MCP serverName'),
  };
}

function normalizeComposerCapabilities(current, defaultAvailability = CAPABILITY_READY) {
  if (current === undefined) return [];
  if (!Array.isArray(current)) {
    throw new TypeError('composer capabilities must be an array');
  }
  return current.map((capability) => (
    normalizeComposerCapability(capability, defaultAvailability)
  ));
}

export function addComposerCapability(current, raw) {
  const capability = normalizeComposerCapability(raw, CAPABILITY_READY);
  const items = normalizeComposerCapabilities(current, CAPABILITY_READY);
  if (items.some((item) => item.key === capability.key)) return items;
  return [...items, capability];
}

export function removeComposerCapability(current, key) {
  const normalizedKey = requiredText(key, 'key');
  return normalizeComposerCapabilities(current).filter((item) => item.key !== normalizedKey);
}

export function snapshotComposerCapabilities(current) {
  return normalizeComposerCapabilities(current).map((item) => {
    const identity = structuredClone(item);
    delete identity.availability;
    return identity;
  });
}

export function restoreComposerCapabilities(snapshot) {
  return normalizeComposerCapabilities(snapshot, CAPABILITY_UNVERIFIED);
}

export function cloneComposerCapabilities(current) {
  return normalizeComposerCapabilities(current).map((item) => structuredClone(item));
}

function catalogCapabilityKey(item, index) {
  if (!item || typeof item !== 'object' || Array.isArray(item)) {
    throw new TypeError(`composer capability catalog item ${index} must be an object`);
  }
  const payload = item.payload;
  if (!payload || typeof payload !== 'object' || Array.isArray(payload)) {
    throw new TypeError(`composer capability catalog item ${index} payload must be an object`);
  }
  const capability = payload.capability;
  if (!capability || typeof capability !== 'object' || Array.isArray(capability)) {
    throw new TypeError(`composer capability catalog item ${index} capability must be an object`);
  }
  return requiredText(capability.key, 'catalog key');
}

export function reconcileComposerCapabilities(current, options) {
  if (!options || typeof options !== 'object' || Array.isArray(options)) {
    throw new TypeError('composer capability reconciliation options must be an object');
  }
  const kind = requiredText(options.kind, 'reconciliation kind');
  if (!CAPABILITY_KINDS.has(kind)) {
    throw new TypeError(`unsupported composer capability kind ${kind}`);
  }
  const status = requiredText(options.status, 'reconciliation status');
  let items = options.items;
  if (items === undefined) items = [];
  if (!Array.isArray(items)) {
    throw new TypeError('composer capability reconciliation items must be an array');
  }
  const availableKeys = new Set(items.map((item, index) => (
    catalogCapabilityKey(item, index)
  )));
  return normalizeComposerCapabilities(current).map((capability) => {
    if (capability.kind !== kind) return capability;
    if (status !== 'success') {
      return { ...capability, availability: CAPABILITY_UNVERIFIED };
    }
    return {
      ...capability,
      availability: availableKeys.has(capability.key) ? CAPABILITY_READY : CAPABILITY_STALE,
    };
  });
}

export function composerCapabilitiesReady(current) {
  return normalizeComposerCapabilities(current)
    .every((item) => item.availability === CAPABILITY_READY);
}
