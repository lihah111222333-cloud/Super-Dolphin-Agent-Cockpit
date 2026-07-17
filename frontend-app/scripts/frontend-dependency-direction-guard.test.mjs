import fs from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

import { describe, expect, it } from 'vitest';

import {
  dependencyDirectionResult,
  validateFrontendDependencyDirection,
} from './frontend-dependency-direction-guard.mjs';

const appRoot = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..');
const registry = JSON.parse(fs.readFileSync(path.join(appRoot, 'scripts/frontend-dependency-direction-registry.json'), 'utf8'));

describe('frontend dependency direction guard', () => {
  it('[A03-production] validates the production graph and exact expiring exemptions', () => {
    expect(validateFrontendDependencyDirection({ root: appRoot, today: '2026-07-17' }))
      .toEqual(expect.objectContaining({ exemptionCount: 5, layerCount: 8 }));
  });

  it('[A03-upward-import] rejects a shared-to-feature upward import', () => {
    const result = syntheticResult(new Map([
      ['src/shared/unsafe.js', "import { feature } from '../features/feature.js';"],
      ['src/features/feature.js', 'export const feature = true;'],
    ]));
    expect(result.unexempted).toEqual([
      'src/shared/unsafe.js|import|../features/feature.js|src/features/feature.js',
    ]);
  });

  it('[A03-dynamic-import] rejects an upward dynamic import', () => {
    const result = syntheticResult(new Map([
      ['src/entities/load.js', "export const load = () => import('../pages/page.js');"],
      ['src/pages/page.js', 'export const page = true;'],
    ]));
    expect(result.unexempted[0]).toContain('|dynamic-import|');
  });

  it('[A03-computed-import] fails closed on a non-literal dynamic import in a guarded layer', () => {
    const result = syntheticResult(new Map([
      ['src/entities/load.js', 'export const load = (target) => import(target);'],
    ]));
    expect(result.unresolved).toEqual([
      'src/entities/load.js|dynamic-import|<non-literal>|<unresolved>',
    ]);
  });

  it('[A03-require] rejects CommonJS require bypasses and fails closed on computed targets', () => {
    const staticResult = syntheticResult(new Map([
      ['src/shared/load.js', "export const page = require('../pages/page.js');"],
      ['src/pages/page.js', 'export const page = true;'],
    ]));
    expect(staticResult.unexempted[0]).toContain('|require|');

    const computedResult = syntheticResult(new Map([
      ['src/shared/load.js', 'export const load = (target) => require(target);'],
    ]));
    expect(computedResult.unresolved[0]).toContain('|require|<non-literal>|');
  });

  it('[A03-re-export] rejects an upward re-export', () => {
    const result = syntheticResult(new Map([
      ['src/features/index.js', "export { Page } from '../pages/Page.jsx';"],
      ['src/pages/Page.jsx', 'export function Page() { return null; }'],
    ]));
    expect(result.unexempted[0]).toContain('|re-export|');
  });

  it('[A03-alias-barrel] resolves aliases and extensionless index barrels', () => {
    const result = syntheticResult(new Map([
      ['src/shared/unsafe.js', "import { feature } from '@/features/catalog';"],
      ['src/features/catalog/index.js', 'export const feature = true;'],
    ]));
    expect(result.unexempted).toEqual([
      'src/shared/unsafe.js|import|@/features/catalog|src/features/catalog/index.js',
    ]);
  });

  it('[A03-transitive-barrel] rejects a lower-layer barrel that re-exports an upper layer', () => {
    const result = syntheticResult(new Map([
      ['src/features/consumer.js', "import { Page } from '../shared/barrel.js';"],
      ['src/shared/barrel.js', "export { Page } from '@/pages/Page.jsx';"],
      ['src/pages/Page.jsx', 'export function Page() { return null; }'],
    ]));
    expect(result.unexempted).toEqual([
      'src/shared/barrel.js|re-export|@/pages/Page.jsx|src/pages/Page.jsx',
    ]);
  });

  it('[A03-expired-exemption] rejects a matched exemption after its expiry', () => {
    const sources = new Map([
      ['src/shared/unsafe.js', "import { feature } from '../features/feature.js';"],
      ['src/features/feature.js', 'export const feature = true;'],
    ]);
    const mutated = syntheticRegistry();
    mutated.exemptions = [{
      from: 'src/shared/unsafe.js',
      kind: 'import',
      specifier: '../features/feature.js',
      to: 'src/features/feature.js',
      expiresOn: '2026-07-18',
      owner: 'frontend-architecture',
      reason: 'Narrow fixture exemption.',
    }];
    const result = dependencyDirectionResult({ sources, registry: mutated, today: '2026-07-19' });
    expect(result.expiredExemptions).toHaveLength(1);
  });

  it('[A03-exemption-drift] rejects both missing and stale exemption entries', () => {
    const missing = clone(registry);
    missing.exemptions = missing.exemptions.slice(1);
    expect(() => validateFrontendDependencyDirection({ root: appRoot, registry: missing, today: '2026-07-17' }))
      .toThrow('violations=');

    const stale = clone(registry);
    stale.exemptions.push({
      from: 'src/shared/fake.js',
      kind: 'import',
      specifier: '../pages/fake.js',
      to: 'src/pages/fake.js',
      expiresOn: '2026-08-31',
      owner: 'frontend-architecture',
      reason: 'Fixture stale exemption.',
    });
    expect(() => validateFrontendDependencyDirection({ root: appRoot, registry: stale, today: '2026-07-17' }))
      .toThrow('stale=');
  });

  it('[A03-zero-tests] rejects a registry with zero regression cases', () => {
    const mutated = clone(registry);
    mutated.caseIds = [];
    expect(() => validateFrontendDependencyDirection({ root: appRoot, registry: mutated, today: '2026-07-17' }))
      .toThrow('must register at least one regression case');
  });

  it('[A03-legal-import] allows downward, same-layer, external imports and ordinary same-name functions', () => {
    const result = syntheticResult(new Map([
      ['src/pages/page.js', `
        import React from 'react';
        import { feature } from '../features/feature.js';
        import { sibling } from './sibling.js';
        function importFeatureByNameOnly() { return feature && sibling; }
      `],
      ['src/pages/sibling.js', 'export const sibling = true;'],
      ['src/features/feature.js', "import { shared } from '../shared/value.js'; export const feature = shared;"],
      ['src/shared/value.js', 'export const shared = true;'],
    ]));
    expect(result.unexempted).toEqual([]);
    expect(result.unresolved).toEqual([]);
  });
});

function syntheticResult(sources) {
  return dependencyDirectionResult({
    sources,
    registry: syntheticRegistry(),
    today: '2026-07-17',
  });
}

function syntheticRegistry() {
  const value = clone(registry);
  value.exemptions = [];
  return value;
}

function clone(value) {
  return JSON.parse(JSON.stringify(value));
}
