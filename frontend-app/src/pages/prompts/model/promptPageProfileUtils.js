export const emptyPersonalizationProfile = Object.freeze({
  displayName: "",
  role: "",
  background: "",
  customInstructions: "",
});

// 与后端 internal/module/personalization/dto.go 保持一致：短字段 80、长字段 1200 字符（按 code point 计数）。
const PROFILE_FIELD_LIMITS = Object.freeze({
  displayName: 80,
  role: 80,
  background: 1200,
  customInstructions: 1200,
});

export function validatePersonalizationProfile(profile) {
  const errors = {};
  for (const [field, limit] of Object.entries(PROFILE_FIELD_LIMITS)) {
    const value = profile?.[field];
    const length = typeof value === "string" ? [...value].length : 0;
    if (length > limit)
      errors[field] = `不能超过 ${limit} 个字符（当前 ${length} 个）`;
  }
  return errors;
}
