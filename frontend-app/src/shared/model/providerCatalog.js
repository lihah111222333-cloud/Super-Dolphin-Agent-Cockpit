const MODEL_OPTIONS_BY_PROVIDER = Object.freeze({
  codex: Object.freeze([
    { value: 'gpt-5.5', label: 'GPT-5.5', short: '5.5' },
    { value: 'gpt-5.4', label: 'GPT-5.4', short: '5.4' },
    { value: 'gpt-5.4-mini', label: 'GPT-5.4 Mini', short: '5.4 Mini' },
    { value: 'gpt-5', label: 'GPT-5', short: '5' },
    { value: 'codex-auto-review', label: 'Codex Auto Review', short: 'Auto Review' },
  ]),
  claude: Object.freeze([
    { value: 'opus', label: 'Opus 4.7' },
    { value: 'opus[1m]', label: 'Opus 4.7 [1M]' },
    { value: 'claude-opus-4-6', label: 'Opus 4.6' },
    { value: 'claude-opus-4-6[1m]', label: 'Opus 4.6 [1M]' },
    { value: 'sonnet', label: 'Sonnet 4.7' },
    { value: 'sonnet[1m]', label: 'Sonnet 4.7 [1M]' },
    { value: 'claude-sonnet-4-6', label: 'Sonnet 4.6' },
    { value: 'claude-sonnet-4-6[1m]', label: 'Sonnet 4.6 [1M]' },
    { value: 'haiku', label: 'Haiku 4.5' },
  ]),
});

const MODEL_DEFAULTS_BY_PROVIDER = Object.freeze({
  codex: Object.freeze({ model: 'gpt-5.5', effort: 'xhigh' }),
  claude: Object.freeze({ model: 'sonnet', effort: 'high' }),
});

const EFFORT_VALUES_BY_PROVIDER = Object.freeze({
  codex: Object.freeze(['xhigh', 'high', 'medium', 'low', 'none']),
  claude: Object.freeze(['max', 'high', 'medium', 'low']),
});

const CLAUDE_LONG_TO_SHORT = Object.freeze({
  'claude-opus-4-7': 'opus',
  'claude-opus-4-7[1m]': 'opus[1m]',
  'claude-haiku-4-5': 'haiku',
});

function normalizeConfigText(value) {
  return value === null || value === undefined ? '' : value.toString().trim();
}

function normalizeProviderKey(value) {
  return normalizeConfigText(value).toLowerCase() === 'claude' ? 'claude' : 'codex';
}

function providerDefaults(provider) {
  return MODEL_DEFAULTS_BY_PROVIDER[normalizeProviderKey(provider)];
}

function canonicalizeModelValue(provider, value) {
  const normalized = normalizeConfigText(value);
  if (normalizeProviderKey(provider) === 'claude') return CLAUDE_LONG_TO_SHORT[normalized] || normalized;
  return normalized;
}

function isClaudeOpusFamilyModel(model) {
  const normalized = normalizeConfigText(model).toLowerCase();
  return normalized === 'best' || normalized.includes('opus');
}

function modelOptionFor(provider, value) {
  const providerKey = normalizeProviderKey(provider);
  const normalized = canonicalizeModelValue(providerKey, value);
  const options = MODEL_OPTIONS_BY_PROVIDER[providerKey];
  return options.find((item) => canonicalizeModelValue(providerKey, item.value) === normalized)
    || (normalized ? { value: normalized, label: normalized, short: normalized } : null);
}

function appendCurrentModelOption(provider, value) {
  const providerKey = normalizeProviderKey(provider);
  const options = MODEL_OPTIONS_BY_PROVIDER[providerKey];
  const current = modelOptionFor(providerKey, value);
  if (!current || options.some((item) => canonicalizeModelValue(providerKey, item.value) === current.value)) return options;
  return [...options, current];
}

function effortOptionsForProvider(provider, labels = {}) {
  return EFFORT_VALUES_BY_PROVIDER[normalizeProviderKey(provider)].map((value) => ({ value, label: labels[value] || value }));
}

export {
  appendCurrentModelOption,
  canonicalizeModelValue,
  CLAUDE_LONG_TO_SHORT,
  EFFORT_VALUES_BY_PROVIDER,
  effortOptionsForProvider,
  isClaudeOpusFamilyModel,
  MODEL_DEFAULTS_BY_PROVIDER,
  MODEL_OPTIONS_BY_PROVIDER,
  modelOptionFor,
  normalizeConfigText,
  normalizeProviderKey,
  providerDefaults,
};
