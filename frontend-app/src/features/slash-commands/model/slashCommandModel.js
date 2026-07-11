const SLASH_TRIGGER_RE = /^(\s*)\/([^\s]*)$/u;

export const SLASH_COMMAND_KIND_ORDER = Object.freeze([
  'builtin',
  'skill',
  'prompt',
  'automation',
  'mcp_tool',
]);

const KIND_RANK = new Map(SLASH_COMMAND_KIND_ORDER.map((kind, index) => [kind, index]));

function normalizedString(value, field, { allowEmpty = false } = {}) {
  if (typeof value !== 'string') {
    throw new TypeError(`slash command ${field} must be a string`);
  }
  const normalized = value.trim();
  if (!allowEmpty && !normalized) {
    throw new TypeError(`slash command ${field} must be a non-empty string`);
  }
  return normalized;
}

function normalizedKeywords(value) {
  if (!Array.isArray(value)) {
    throw new TypeError('slash command keywords must be an array');
  }
  const seen = new Set();
  const keywords = [];
  value.forEach((keyword) => {
    const normalized = normalizedString(keyword, 'keyword', { allowEmpty: true });
    if (!normalized || seen.has(normalized)) return;
    seen.add(normalized);
    keywords.push(normalized);
  });
  return keywords;
}

export function parseSlashCommandTrigger(value) {
  const draft = typeof value === 'string' ? value : '';
  const match = SLASH_TRIGGER_RE.exec(draft);
  if (!match) return null;
  return { leading: match[1], query: match[2], raw: `/${match[2]}` };
}

export function replaceSlashCommandTrigger(draft, replacement) {
  const trigger = parseSlashCommandTrigger(draft);
  if (!trigger) throw new Error('slash command trigger is not active');
  return `${trigger.leading}${normalizedString(replacement, 'replacement', { allowEmpty: true })}`;
}

export function normalizeSlashCommandItem(raw) {
  if (!raw || typeof raw !== 'object' || Array.isArray(raw)) {
    throw new TypeError('slash command item must be an object');
  }
  const kind = normalizedString(raw.kind, 'kind');
  if (!KIND_RANK.has(kind)) {
    throw new TypeError(`slash command kind is unsupported: ${kind}`);
  }
  if (!raw.payload || typeof raw.payload !== 'object' || Array.isArray(raw.payload)) {
    throw new TypeError('slash command payload must be an object');
  }
  if (typeof raw.disabled !== 'boolean') {
    throw new TypeError('slash command disabled must be a boolean');
  }
  return {
    id: normalizedString(raw.id, 'id'),
    kind,
    name: normalizedString(raw.name, 'name'),
    label: normalizedString(raw.label, 'label'),
    description: normalizedString(raw.description, 'description', { allowEmpty: true }),
    keywords: normalizedKeywords(raw.keywords),
    payload: raw.payload,
    disabled: raw.disabled,
    disabledReason: normalizedString(raw.disabledReason, 'disabledReason', { allowEmpty: true }),
  };
}

function matchRank(item, query) {
  if (!query) return 0;
  const name = item.name.toLocaleLowerCase();
  const label = item.label.toLocaleLowerCase();
  if (name.startsWith(query) || label.startsWith(query)) return 0;
  if (name.includes(query) || label.includes(query)) return 1;
  const secondary = [item.description, ...item.keywords]
    .map((value) => value.toLocaleLowerCase());
  return secondary.some((value) => value.includes(query)) ? 2 : Number.POSITIVE_INFINITY;
}

export function rankSlashCommandItems(items, query) {
  if (!Array.isArray(items)) {
    throw new TypeError('slash command items must be an array');
  }
  const normalizedQuery = typeof query === 'string' ? query.trim().toLocaleLowerCase() : '';
  return items
    .map((raw, index) => {
      const item = normalizeSlashCommandItem(raw);
      return { item, index, match: matchRank(item, normalizedQuery) };
    })
    .filter(({ match }) => Number.isFinite(match))
    .sort((left, right) => (
      left.match - right.match
      || KIND_RANK.get(left.item.kind) - KIND_RANK.get(right.item.kind)
      || left.index - right.index
    ))
    .map(({ item }) => item);
}

export function slashCommandOptionId(item) {
  const id = normalizedString(item?.id, 'id');
  return `slash-command-${id.replace(/[^a-zA-Z0-9_-]/g, '-')}`;
}
