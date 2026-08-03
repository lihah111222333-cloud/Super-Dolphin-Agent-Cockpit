import { describe, expect, it } from 'vitest';
import {
  APPEARANCE_ALTERNATE_SETTINGS,
  APPEARANCE_DOM_PROJECTORS,
  APPEARANCE_FIELD_KEYS,
  APPEARANCE_INITIAL_SETTINGS,
  APPEARANCE_PARSERS,
  APPEARANCE_SERIALIZERS,
  appearanceRootProjection,
  resolveAppearanceTheme,
  serializeAppearanceEnvelope,
} from './appearanceSchema.js';
import { APPEARANCE_STORE_ACTIONS, APPEARANCE_UI_CONTROLS } from './appearanceStore.js';

export function assertAppearanceCoverage(label, registry) {
  const compareFields = (left, right) => left.localeCompare(right);
  const producer = [...APPEARANCE_FIELD_KEYS].sort(compareFields);
  const consumer = Object.keys(registry).sort(compareFields);
  const missing = producer.filter((field) => !consumer.includes(field));
  const stale = consumer.filter((field) => !producer.includes(field));
  if (missing.length > 0) throw new Error(`missing ${label}: ${missing.join(', ')}`);
  if (stale.length > 0) throw new Error(`stale ${label}: ${stale.join(', ')}`);
}

describe('appearance field guard', () => {
  it('keeps every consumer exact with the dynamically enumerated producer', () => {
    assertAppearanceCoverage('parser', APPEARANCE_PARSERS);
    assertAppearanceCoverage('serializer', APPEARANCE_SERIALIZERS);
    assertAppearanceCoverage('DOM consumer', APPEARANCE_DOM_PROJECTORS);
    assertAppearanceCoverage('store action', APPEARANCE_STORE_ACTIONS);
    assertAppearanceCoverage('UI control', APPEARANCE_UI_CONTROLS);
  });

  it.each(APPEARANCE_FIELD_KEYS)('%s changes serialization and root projection one-hot', (field) => {
    const base = APPEARANCE_INITIAL_SETTINGS;
    const alternate = { ...base, [field]: APPEARANCE_ALTERNATE_SETTINGS[field] };
    const media = () => ({ matches: false });
    const baseTheme = resolveAppearanceTheme(base.themeMode, media);
    const alternateTheme = resolveAppearanceTheme(alternate.themeMode, media);
    expect(serializeAppearanceEnvelope(alternate)).not.toBe(serializeAppearanceEnvelope(base));
    expect(appearanceRootProjection(alternate, alternateTheme))
      .not.toEqual(appearanceRootProjection(base, baseTheme));
  });

  it('reports exact fail-first gaps', () => {
    const { accent: _accent, ...missingAccent } = APPEARANCE_DOM_PROJECTORS;
    const { uiScale: _uiScale, ...missingScale } = APPEARANCE_SERIALIZERS;
    expect(() => assertAppearanceCoverage('DOM consumer', missingAccent))
      .toThrow('missing DOM consumer: accent');
    expect(() => assertAppearanceCoverage('serializer', missingScale))
      .toThrow('missing serializer: uiScale');
  });
});
