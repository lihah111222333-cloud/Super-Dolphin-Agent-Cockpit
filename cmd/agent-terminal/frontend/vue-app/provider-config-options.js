function sanitizeProviderConfigString(value) {
  const normalized = (value || '').toString().trim();
  const lower = normalized.toLowerCase();
  if (!normalized || lower === '[object object]' || lower === 'undefined' || lower === 'null') return '';
  return normalized;
}

export function normalizeProviderConfigValue(value) {
  if (value == null) return '';
  if (typeof value === 'string' || typeof value === 'number' || typeof value === 'boolean') {
    return sanitizeProviderConfigString(value);
  }
  if (typeof value === 'object') {
    for (const key of ['value', 'model', 'id', 'key', 'name']) {
      if (!Object.prototype.hasOwnProperty.call(value, key)) continue;
      const normalized = normalizeProviderConfigValue(value[key]);
      if (normalized) return normalized;
    }
    return '';
  }
  return sanitizeProviderConfigString(value);
}

export const CODEX_IDENTITY_DEFAULTS = Object.freeze({
  codexHome: '',
  codexInstanceKey: 'default',
  codexModelProvider: 'super-dolphin-relay',
});

// Claude CLI accepts both short aliases (opus/sonnet/haiku resolve to latest
// version on Anthropic's side) and explicit version slugs (claude-opus-4-6[1m]).
// 4.7 options use short aliases (they auto-track latest); 4.6 options use
// explicit long slugs so users can pin to 4.6 even after further upgrades.
// Canonicalize the 4.7 long slugs that orchestration_launch_agent historically
// writes into runtime — they collapse back to the short alias so the dropdown
// highlights an existing row instead of appending a raw long slug.
const CLAUDE_LONG_TO_SHORT = Object.freeze({
  'claude-opus-4-7': 'opus',
  'claude-opus-4-7[1m]': 'opus[1m]',
  'claude-haiku-4-5': 'haiku',
});

export function canonicalizeClaudeModelValue(value) {
  const normalized = normalizeProviderConfigValue(value);
  return CLAUDE_LONG_TO_SHORT[normalized] || normalized;
}

export function canonicalizeModelValue(providerKey, value) {
  if (normalizeProviderConfigValue(providerKey) === 'claude') {
    return canonicalizeClaudeModelValue(value);
  }
  return normalizeProviderConfigValue(value);
}

export const MODEL_OPTIONS_BY_PROVIDER = Object.freeze({
  codex: Object.freeze([
    { value: 'gpt-5-codex', label: 'GPT-5 Codex' },
    { value: 'gpt-5.5', label: 'GPT-5.5' },
    { value: 'gpt-5.4', label: 'GPT-5.4' },
    { value: 'gpt-5.3-codex', label: 'GPT-5.3 Codex' },
    { value: 'gpt-5.2', label: 'GPT-5.2' },
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

export const MODEL_OPTIONS = MODEL_OPTIONS_BY_PROVIDER.codex;

export const EFFORT_MODES_BY_PROVIDER = Object.freeze({
  codex: Object.freeze([
    { value: 'xhigh', label: '极高' },
    { value: 'high', label: '高' },
    { value: 'medium', label: '中' },
    { value: 'low', label: '低' },
    { value: 'minimal', label: '极低' },
    { value: 'none', label: '关闭' },
  ]),
  claude: Object.freeze([
    { value: 'max', label: 'max（仅 Opus）' },
    { value: 'high', label: 'high' },
    { value: 'medium', label: 'medium' },
    { value: 'low', label: 'low' },
  ]),
});

export const EFFORT_MODES = EFFORT_MODES_BY_PROVIDER.codex;

export function isClaudeOpusFamilyModel(model) {
  const normalizedModel = normalizeProviderConfigValue(model).toLowerCase();
  return normalizedModel === 'best' || normalizedModel.includes('opus');
}

export function appendCurrentOption(options, currentValue, labelBuilder = (value) => value) {
  const normalizedValue = normalizeProviderConfigValue(currentValue);
  if (!normalizedValue) {
    return options;
  }
  if (options.some((item) => normalizeProviderConfigValue(item?.value) === normalizedValue)) {
    return options;
  }
  return [...options, { value: normalizedValue, label: labelBuilder(normalizedValue) }];
}

export function getProviderDefaultConfig(providerId) {
  return normalizeProviderConfigValue(providerId) === 'claude'
    ? { model: 'sonnet', effort: 'high' }
    : { model: 'gpt-5-codex', effort: 'xhigh' };
}
