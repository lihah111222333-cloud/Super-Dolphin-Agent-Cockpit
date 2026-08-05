import { parseStrictJsonValue } from '../../pages/shared/pageShared.js';

export const APPEARANCE_SCHEMA_VERSION = 1;
export const APPEARANCE_STORAGE_KEY = 'super-dolphin.appearance';
export const LEGACY_THEME_STORAGE_KEY = 'super-dolphin-theme';

export class AppearanceValidationError extends Error {
  constructor(message) {
    super(message);
    this.name = 'AppearanceValidationError';
  }
}

function enumDescriptor(defaultValue, alternateValue, values) {
  const allowed = Object.freeze([...values]);
  return Object.freeze({
    allowed,
    alternateValue,
    defaultValue,
    parse(value, field) {
      if (!allowed.includes(value)) {
        throw new AppearanceValidationError(`appearance.${field} must be one of: ${allowed.join(', ')}`);
      }
      return value;
    },
  });
}

export const APPEARANCE_FIELD_DESCRIPTORS = Object.freeze({
  themeMode: enumDescriptor('system', 'dark', ['system', 'light', 'dark']),
  uiScale: enumDescriptor(100, 125, [80, 90, 100, 110, 125, 150]),
  accent: enumDescriptor('violet', 'mint', ['violet', 'blue', 'mint', 'amber', 'rose']),
});

export const APPEARANCE_FIELD_KEYS = Object.freeze(Object.keys(APPEARANCE_FIELD_DESCRIPTORS));

export const APPEARANCE_INITIAL_SETTINGS = Object.freeze(Object.fromEntries(
  APPEARANCE_FIELD_KEYS.map((field) => [field, APPEARANCE_FIELD_DESCRIPTORS[field].defaultValue]),
));

export const APPEARANCE_ALTERNATE_SETTINGS = Object.freeze(Object.fromEntries(
  APPEARANCE_FIELD_KEYS.map((field) => [field, APPEARANCE_FIELD_DESCRIPTORS[field].alternateValue]),
));

export const APPEARANCE_ACCENT_TOKENS = Object.freeze({
  violet: Object.freeze({ light: '#6558d9', dark: '#a99cff' }),
  blue: Object.freeze({ light: '#2563eb', dark: '#72a7ff' }),
  mint: Object.freeze({ light: '#087f5b', dark: '#64d9b0' }),
  amber: Object.freeze({ light: '#b45309', dark: '#f3bd67' }),
  rose: Object.freeze({ light: '#be315f', dark: '#ff8fb3' }),
});

export const APPEARANCE_PARSERS = Object.freeze(Object.fromEntries(
  APPEARANCE_FIELD_KEYS.map((field) => [
    field,
    (value) => APPEARANCE_FIELD_DESCRIPTORS[field].parse(value, field),
  ]),
));

export const APPEARANCE_SERIALIZERS = Object.freeze(Object.fromEntries(
  APPEARANCE_FIELD_KEYS.map((field) => [field, (value) => value]),
));

function assertExactKeys(value, expectedKeys, scope) {
  if (!value || typeof value !== 'object' || Array.isArray(value)) {
    throw new AppearanceValidationError(`${scope} must be an object`);
  }
  const compareFields = (left, right) => left.localeCompare(right);
  const actual = Object.keys(value).sort(compareFields);
  const expected = [...expectedKeys].sort(compareFields);
  const missing = expected.filter((field) => !actual.includes(field));
  const unknown = actual.filter((field) => !expected.includes(field));
  if (missing.length > 0) {
    throw new AppearanceValidationError(`${scope} missing fields: ${missing.join(', ')}`);
  }
  if (unknown.length > 0) {
    throw new AppearanceValidationError(`${scope} has unknown fields: ${unknown.join(', ')}`);
  }
}

export function parseAppearanceSettings(value) {
  assertExactKeys(value, APPEARANCE_FIELD_KEYS, 'appearance.settings');
  return Object.freeze(Object.fromEntries(
    APPEARANCE_FIELD_KEYS.map((field) => [field, APPEARANCE_PARSERS[field](value[field])]),
  ));
}

export function parseAppearanceEnvelope(raw) {
  if (typeof raw !== 'string' || raw.length === 0) {
    throw new AppearanceValidationError('appearance payload must be a non-empty JSON string');
  }
  let envelope;
  try {
    envelope = parseStrictJsonValue(raw, 'appearance payload');
  } catch (error) {
    throw new AppearanceValidationError(`appearance payload is malformed JSON: ${error.message}`);
  }
  assertExactKeys(envelope, ['version', 'settings'], 'appearance envelope');
  if (envelope.version !== APPEARANCE_SCHEMA_VERSION) {
    throw new AppearanceValidationError(
      `appearance schema version ${String(envelope.version)} is unsupported; expected ${APPEARANCE_SCHEMA_VERSION}`,
    );
  }
  return Object.freeze({
    version: APPEARANCE_SCHEMA_VERSION,
    settings: parseAppearanceSettings(envelope.settings),
  });
}

export function serializeAppearanceSettings(settings) {
  const parsed = parseAppearanceSettings(settings);
  return Object.fromEntries(
    APPEARANCE_FIELD_KEYS.map((field) => [field, APPEARANCE_SERIALIZERS[field](parsed[field])]),
  );
}

export function serializeAppearanceEnvelope(settings) {
  return JSON.stringify({
    version: APPEARANCE_SCHEMA_VERSION,
    settings: serializeAppearanceSettings(settings),
  });
}

export function migrateLegacyTheme(legacyTheme) {
  if (legacyTheme !== 'light' && legacyTheme !== 'dark') {
    throw new AppearanceValidationError('legacy appearance theme must be light or dark');
  }
  return Object.freeze({
    ...APPEARANCE_INITIAL_SETTINGS,
    themeMode: legacyTheme,
  });
}

export function resolveAppearanceTheme(themeMode, matchMedia) {
  const parsed = APPEARANCE_PARSERS.themeMode(themeMode);
  if (parsed !== 'system') return parsed;
  if (typeof matchMedia !== 'function') {
    throw new AppearanceValidationError('system appearance requires matchMedia');
  }
  const query = matchMedia('(prefers-color-scheme: dark)');
  if (!query || typeof query.matches !== 'boolean') {
    throw new AppearanceValidationError('system appearance media query is invalid');
  }
  return query.matches ? 'dark' : 'light';
}

export const APPEARANCE_DOM_PROJECTORS = Object.freeze({
  themeMode(value, context) {
    return {
      attributes: {
        'data-theme': context.resolvedTheme,
        'data-theme-mode': value,
      },
      styles: {},
    };
  },
  uiScale(value) {
    return {
      attributes: { 'data-ui-scale': String(value) },
      styles: {
        '--ui-scale': String(value / 100),
        '--ui-scale-percent': `${value}%`,
      },
    };
  },
  accent(value, context) {
    const tokens = APPEARANCE_ACCENT_TOKENS[value];
    if (!tokens) throw new AppearanceValidationError(`appearance accent tokens missing for ${value}`);
    return {
      attributes: { 'data-accent': value },
      styles: {
        '--appearance-accent': tokens[context.resolvedTheme],
      },
    };
  },
});

export function appearanceRootProjection(settings, resolvedTheme) {
  const parsed = parseAppearanceSettings(settings);
  if (resolvedTheme !== 'light' && resolvedTheme !== 'dark') {
    throw new AppearanceValidationError('resolved appearance theme must be light or dark');
  }
  return APPEARANCE_FIELD_KEYS.reduce((projection, field) => {
    const fieldProjection = APPEARANCE_DOM_PROJECTORS[field](parsed[field], { resolvedTheme });
    return {
      attributes: { ...projection.attributes, ...fieldProjection.attributes },
      styles: { ...projection.styles, ...fieldProjection.styles },
    };
  }, { attributes: {}, styles: {} });
}
