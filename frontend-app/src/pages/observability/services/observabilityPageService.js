import {
  copyTextToClipboard as copyTextToClipboardBackend,
  getObservabilityTrace as getObservabilityTraceBackend,
  listObservabilityRecent as listObservabilityRecentBackend,
} from '../../../services/modules/observabilityService.js';

const defaultObservabilityPageApi = Object.freeze({
  copyTextToClipboard: copyTextToClipboardBackend,
  getObservabilityTrace: getObservabilityTraceBackend,
  listObservabilityRecent: listObservabilityRecentBackend,
});

function textValue(value) {
  return value === null || value === undefined ? '' : value.toString().trim();
}

function requiredText(value, fieldName) {
  const text = textValue(value);
  if (!text) throw new Error(`${fieldName} is required`);
  return text;
}

function normalizeLimit(value) {
  if (value === null || value === undefined) return undefined;
  if (typeof value === 'string') {
    const text = value.trim();
    if (!text) return undefined;
    if (!/^\d+$/.test(text)) throw new Error('limit must be a positive integer');
    const parsed = Number(text);
    if (!Number.isSafeInteger(parsed) || parsed <= 0) throw new Error('limit must be a positive integer');
    return parsed;
  }
  if (typeof value === 'number') {
    if (!Number.isSafeInteger(value) || value <= 0) throw new Error('limit must be a positive integer');
    return value;
  }
  throw new Error('limit must be a positive integer');
}

function withNormalizedLimit(params = {}) {
  const nextParams = { ...params };
  const limit = normalizeLimit(nextParams.limit);
  if (limit === undefined) {
    delete nextParams.limit;
  } else {
    nextParams.limit = limit;
  }
  return nextParams;
}

function createObservabilityPageService(api = defaultObservabilityPageApi) {
  return {
    copyTextToClipboard(text) {
      return api.copyTextToClipboard(requiredText(text, 'text'));
    },
    getObservabilityTrace(params = {}) {
      return api.getObservabilityTrace(withNormalizedLimit({
        ...params,
        traceId: requiredText(params.traceId, 'traceId'),
      }));
    },
    listObservabilityRecent(params = {}) {
      return api.listObservabilityRecent(withNormalizedLimit(params));
    },
  };
}

const observabilityPageService = createObservabilityPageService();
const { copyTextToClipboard, getObservabilityTrace, listObservabilityRecent } = observabilityPageService;

export {
  copyTextToClipboard,
  createObservabilityPageService,
  getObservabilityTrace,
  listObservabilityRecent,
  observabilityPageService,
};
