import {
  canonicalizeModelValue,
  effortOptionsForProvider,
  isClaudeOpusFamilyModel,
  MODEL_OPTIONS_BY_PROVIDER,
  providerDefaults,
} from "../../shared/model/providerCatalog.js";

const PROVIDER_LABELS = Object.freeze({ claude: "Claude", codex: "Codex" });

const EFFORT_MODES_BY_PROVIDER = Object.freeze({
  codex: Object.freeze(
    effortOptionsForProvider("codex", {
      xhigh: "极高",
      high: "高",
      medium: "中",
      low: "低",
      none: "关闭",
    }),
  ),
  claude: Object.freeze(
    effortOptionsForProvider("claude", { max: "max（仅 Opus）" }),
  ),
});

const PERSONALITY_OPTIONS = Object.freeze([
  { value: "pragmatic", label: "pragmatic（务实高效，默认）" },
  { value: "friendly", label: "friendly（友好气氛）" },
  { value: "none", label: "none（默认风格）" },
]);

function textValue(value) {
  return value === null || value === undefined ? "" : value.toString();
}

function providerConfigValue(value) {
  if (value && typeof value === "object" && !Array.isArray(value)) {
    for (const key of ["value", "model", "id", "key", "name"]) {
      const text = providerConfigValue(value[key]);
      if (text) return text;
    }
    return "";
  }
  return textValue(value).trim();
}

function normalizeProviderName(_value) {
  return "codex";
}

function normalizeProviderModelSetting(provider, value) {
  return (
    canonicalizeModelValue(
      normalizeProviderName(provider),
      providerConfigValue(value),
    ) || providerDefaults(normalizeProviderName(provider)).model
  );
}

function normalizeProviderEffortSetting(provider, model, value) {
  const normalizedProvider = normalizeProviderName(provider);
  const normalizedValue = providerConfigValue(value).toLowerCase();
  if (normalizedProvider !== "claude") {
    if (normalizedValue === "minimal") return "low";
    return EFFORT_MODES_BY_PROVIDER.codex.some(
      (item) => item.value === normalizedValue,
    )
      ? normalizedValue
      : providerDefaults(normalizedProvider).effort;
  }
  switch (normalizedValue) {
    case "max":
      return isClaudeOpusFamilyModel(model) ? "max" : "high";
    case "high":
    case "xhigh":
      return "high";
    case "medium":
      return "medium";
    case "low":
    case "minimal":
      return "low";
    default:
      return providerDefaults(normalizedProvider).effort;
  }
}

function appendCurrentOption(options, currentValue) {
  const normalized = providerConfigValue(currentValue);
  if (
    !normalized ||
    options.some((option) => providerConfigValue(option.value) === normalized)
  )
    return options;
  return [...options, { value: normalized, label: normalized }];
}

const providerSettingsViewConfig = Object.freeze({
  appendCurrentOption,
  effortModesByProvider: EFFORT_MODES_BY_PROVIDER,
  isClaudeOpusFamilyModel,
  modelOptionsByProvider: MODEL_OPTIONS_BY_PROVIDER,
  normalizeProviderEffortSetting,
  personalityOptions: PERSONALITY_OPTIONS,
});

export {
  PROVIDER_LABELS,
  normalizeProviderEffortSetting,
  normalizeProviderModelSetting,
  normalizeProviderName,
  providerConfigValue,
  providerSettingsViewConfig,
  textValue,
};
