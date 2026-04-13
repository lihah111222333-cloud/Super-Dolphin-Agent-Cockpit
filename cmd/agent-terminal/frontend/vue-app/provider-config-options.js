export function normalizeProviderConfigValue(value) {
  return (value || '').toString().trim();
}

export const MODEL_OPTIONS_BY_PROVIDER = Object.freeze({
  codex: Object.freeze([
    { value: 'gpt-5.4', label: 'GPT-5.4' },
    { value: 'gpt-5.3-codex', label: 'GPT-5.3 Codex' },
    { value: 'gpt-5.2-codex', label: 'GPT-5.2 Codex' },
    { value: 'gpt-5.2', label: 'GPT-5.2' },
  ]),
  claude: Object.freeze([
    { value: 'best', label: 'Best（Opus 4.6）' },
    { value: 'opus', label: 'Opus 4.6' },
    { value: 'opus[1m]', label: 'Opus 4.6 [1M]' },
    { value: 'sonnet', label: 'Sonnet 4.6' },
    { value: 'sonnet[1m]', label: 'Sonnet 4.6 [1M]' },
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
