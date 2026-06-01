const SUPPORTED_TRACE_HEADERS = new Set(['traceparent', 'sw8', 'b3', 'sentry-trace']);
const REGEX_PREFIX = 'regex:';
const DEFAULT_RUM_HOST_URL = 'https://rumt-zh.com';
const SENSITIVE_PARAM_PATTERN = /([?&])([^=&#]+)=([^&#]*)/g;
const SENSITIVE_PARAM_NAME_PATTERN = /(^|[_-])(token|key|secret|password|authorization|session|code|jwt|auth)($|[_-])/i;
const FILE_URL_LOCAL_PATH_PATTERN = /file:\/\/\/[^?#\s]+/gi;
const WINDOWS_LOCAL_PATH_PATTERN = /[A-Za-z]:\\Users\\[^?#\s]+/g;
const LOCAL_PATH_PATTERN = /\/(?:home|Users)\/[^?#\s]+/g;
const LONG_NUMBER_PATTERN = /\/\d{8,}(?=\/|$)/g;

let tencentRumInstancePromise = null;

export function initTencentRum(env = import.meta.env, loadAegis = () => import('aegis-web-sdk')) {
  const config = buildTencentRumConfig(env);
  if (!config) {
    return null;
  }
  if (!tencentRumInstancePromise) {
    tencentRumInstancePromise = loadAegis().then(({ default: Aegis }) => new Aegis(config));
  }
  return tencentRumInstancePromise;
}

export function buildTencentRumConfig(env = import.meta.env) {
  const id = readEnv(env, 'VITE_TENCENT_RUM_ID');
  const enabled = readBoolEnv(env, 'VITE_TENCENT_RUM_ENABLED');
  if (!id) {
    if (enabled) {
      throw new Error('VITE_TENCENT_RUM_ID is required when Tencent RUM is enabled');
    }
    return null;
  }

  const api = {};
  const injectTraceUrls = readListEnv(env, 'VITE_TENCENT_RUM_TRACE_URLS');
  const hostUrl = readEnv(env, 'VITE_TENCENT_RUM_HOST_URL');
  if (injectTraceUrls.length > 0) {
    const traceHeader = readTraceHeader(env);
    api.injectTraceHeader = traceHeader;
    api.injectTraceUrls = injectTraceUrls;
    api.reqHeaders = [traceHeader];
  }
  const injectTraceIgnoreUrls = [
    ...readListEnv(env, 'VITE_TENCENT_RUM_TRACE_IGNORE_URLS'),
    ...(injectTraceUrls.length > 0 ? rumHostTraceIgnorePatterns(hostUrl) : []),
  ];
  if (injectTraceIgnoreUrls.length > 0) {
    api.injectTraceIgnoreUrls = injectTraceIgnoreUrls;
  }

  const config = {
    id,
    reportApiSpeed: {
      urlHandler: sanitizeRumUrl,
    },
    reportAssetSpeed: true,
    spa: true,
    urlHandler: sanitizeRumUrl,
    api,
  };
  applyOptionalConfig(config, 'uin', readEnv(env, 'VITE_TENCENT_RUM_UIN'));
  applyOptionalConfig(config, 'hostUrl', hostUrl);
  return config;
}

export function resetTencentRumForTest() {
  tencentRumInstancePromise = null;
}

function readTraceHeader(env) {
  const value = readEnv(env, 'VITE_TENCENT_RUM_TRACE_HEADER') || 'traceparent';
  if (!SUPPORTED_TRACE_HEADERS.has(value)) {
    throw new Error(`VITE_TENCENT_RUM_TRACE_HEADER must be one of: ${Array.from(SUPPORTED_TRACE_HEADERS).join(', ')}`);
  }
  return value;
}

function readBoolEnv(env, key) {
  const value = readEnv(env, key);
  if (!value) {
    return false;
  }
  if (value === 'true') {
    return true;
  }
  if (value === 'false') {
    return false;
  }
  throw new Error(`${key} must be "true" or "false"`);
}

function readListEnv(env, key) {
  const value = readEnv(env, key);
  if (!value) {
    return [];
  }
  return value.split(',').map((item) => parseUrlPattern(key, item.trim())).filter(Boolean);
}

function readEnv(env, key) {
  return String(env?.[key] || '').trim();
}

function applyOptionalConfig(config, key, value) {
  if (value) {
    config[key] = value;
  }
}

function parseUrlPattern(key, value) {
  if (!value) {
    return null;
  }
  if (!value.startsWith(REGEX_PREFIX)) {
    return value;
  }
  const pattern = value.slice(REGEX_PREFIX.length);
  if (!pattern) {
    throw new Error(`${key} regex pattern is empty`);
  }
  try {
    return new RegExp(pattern);
  } catch (error) {
    throw new Error(`${key} contains invalid regex "${pattern}": ${error.message}`, { cause: error });
  }
}

function rumHostTraceIgnorePatterns(hostUrl) {
  return [DEFAULT_RUM_HOST_URL, hostUrl]
    .map((value) => value && value.trim())
    .filter(Boolean)
    .map((value) => {
      const url = new URL(value);
      return new RegExp(`^${escapeRegExp(url.origin)}(?:/|$)`);
    });
}

function escapeRegExp(value) {
  return value.replace(/[.*+?^${}()|[\]\\]/g, '\\$&');
}

function redactSensitiveParams(url) {
  return url.replace(SENSITIVE_PARAM_PATTERN, (match, separator, name) => {
    if (!SENSITIVE_PARAM_NAME_PATTERN.test(name)) {
      return match;
    }
    return `${separator}${name}=<redacted>`;
  });
}

function sanitizeRumUrl(url) {
  return redactSensitiveParams(String(url || ''))
    .replace(FILE_URL_LOCAL_PATH_PATTERN, 'file:///<local-path>')
    .replace(WINDOWS_LOCAL_PATH_PATTERN, '<local-path>')
    .replace(LOCAL_PATH_PATTERN, '/<local-path>')
    .replace(LONG_NUMBER_PATTERN, '/<id>')
    .replace(/#.*$/, '');
}
