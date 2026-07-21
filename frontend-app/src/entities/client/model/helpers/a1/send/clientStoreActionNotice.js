import { clockNowISO, normalizeString } from '../clientStoreUtils.js';

function actionNotice(message, tone = 'info', category = '') {
  const normalized = normalizeString(message);
  if (!normalized) return null;
  const normalizedCategory = normalizeString(category);
  return {
    message: normalized,
    tone,
    timestamp: clockNowISO(),
    ...(normalizedCategory ? { category: normalizedCategory } : {}),
  };
}

function actionNoticeRuntimeFields(fields = {}) {
  const out = {};
  const error = normalizeString(fields.error || fields.message);
  const category = normalizeString(fields.category);
  if (error) out.error = error;
  if (category) out.category = category;
  if (typeof fields.recoverable === 'boolean') out.recoverable = fields.recoverable;
  return out;
}

export { actionNotice, actionNoticeRuntimeFields };
