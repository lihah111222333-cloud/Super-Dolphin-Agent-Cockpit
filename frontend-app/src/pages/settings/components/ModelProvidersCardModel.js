import { parseModelProviderRegistryResponse } from '../../../shared/api/backendSchemas.js';

const RUNTIME_VENDOR_FIELDS = new Set(['configured', 'maskedEnv', 'envStatus']);

const EMPTY_REGISTRY = Object.freeze({
  activeVendorId: '',
  vendors: Object.freeze([]),
});

function normalizeRegistry(payload) {
  const registry = parseModelProviderRegistryResponse(payload);
  return {
    ...registry,
    activeVendorId: textValue(registry.activeVendorId),
    vendors: registry.vendors.map(normalizeVendor),
  };
}

function normalizeVendor(vendor) {
  const budget = plainObject(vendor?.budget) ? vendor.budget : {};
  const tokenPool = plainObject(vendor?.tokenPool) ? vendor.tokenPool : {};
  return {
    ...vendor,
    id: textValue(vendor?.id),
    label: textValue(vendor?.label || vendor?.id),
    enabled: Boolean(vendor?.enabled),
    baseURL: textValue(vendor?.baseURL),
    envKey: textValue(vendor?.envKey),
    codexModelProvider: textValue(vendor?.codexModelProvider),
    defaultModel: textValue(vendor?.defaultModel),
    codexHome: textValue(vendor?.codexHome),
    codexInstanceKey: textValue(vendor?.codexInstanceKey),
    configured: Boolean(vendor?.configured),
    maskedEnv: textValue(vendor?.maskedEnv),
    envStatus: textValue(vendor?.envStatus),
    budget: {
      ...budget,
      dailyUsd: inputNumberValue(budget.dailyUsd),
      monthlyUsd: inputNumberValue(budget.monthlyUsd),
    },
    tokenPool: {
      ...tokenPool,
      priority: inputNumberValue(tokenPool.priority),
      fallbackVendorId: textValue(tokenPool.fallbackVendorId),
    },
  };
}

function selectVendorId(registry, preferredVendorId) {
  const preferred = textValue(preferredVendorId);
  if (preferred && registry.vendors.some((vendor) => vendor.id === preferred)) return preferred;
  if (registry.activeVendorId && registry.vendors.some((vendor) => vendor.id === registry.activeVendorId)) return registry.activeVendorId;
  return registry.vendors[0]?.id || '';
}

function updateSelectedVendor(registry, selectedVendorId, update) {
  const targetId = selectedVendorId || registry.vendors[0]?.id || '';
  return {
    ...registry,
    vendors: registry.vendors.map((vendor) => (vendor.id === targetId ? normalizeVendor(update(vendor)) : vendor)),
  };
}

function registrySavePayload(registry) {
  return {
    ...registry,
    vendors: registry.vendors.map(vendorSavePayload),
  };
}

// 保存前移除只用于展示的环境变量状态，并把预算与 token 池里的数字字段转成数字。
function vendorSavePayload(vendor) {
  const nextVendor = {};
  for (const [key, value] of Object.entries(vendor)) {
    if (!RUNTIME_VENDOR_FIELDS.has(key)) nextVendor[key] = value;
  }
  nextVendor.budget = {
    ...(plainObject(vendor.budget) ? vendor.budget : {}),
    dailyUsd: finiteNumberOrZero(vendor.budget?.dailyUsd),
    monthlyUsd: finiteNumberOrZero(vendor.budget?.monthlyUsd),
  };
  nextVendor.tokenPool = {
    ...(plainObject(vendor.tokenPool) ? vendor.tokenPool : {}),
    priority: finiteNumberOrZero(vendor.tokenPool?.priority),
    fallbackVendorId: textValue(vendor.tokenPool?.fallbackVendorId),
  };
  return nextVendor;
}

function vendorStatusLabel(vendor, activeVendorId, modelCopy) {
  const status = vendor.envStatus || (vendor.configured ? modelCopy.configured : modelCopy.missing);
  const parts = [vendor.enabled ? modelCopy.enabled.toLowerCase() : 'disabled', status];
  if (vendor.id === activeVendorId) parts.push(modelCopy.active);
  return parts.join(' / ');
}

function envStatusLabel(vendor, modelCopy) {
  const status = vendor.envStatus || (vendor.configured ? modelCopy.configured : modelCopy.missing);
  return vendor.maskedEnv ? status + ' / ' + vendor.maskedEnv : status;
}

function finiteNumberOrZero(value) {
  const number = Number(value);
  return Number.isFinite(number) ? number : 0;
}

function inputNumberValue(value) {
  return value === undefined || value === null ? '' : String(value);
}

function textValue(value) {
  return (value || '').toString();
}

function plainObject(value) {
  return Boolean(value && typeof value === 'object' && !Array.isArray(value));
}

export {
  EMPTY_REGISTRY,
  envStatusLabel,
  normalizeRegistry,
  plainObject,
  registrySavePayload,
  selectVendorId,
  textValue,
  updateSelectedVendor,
  vendorStatusLabel,
};
