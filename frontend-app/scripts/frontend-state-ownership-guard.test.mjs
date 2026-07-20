import fs from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

import { describe, expect, it } from 'vitest';

import {
  discoverStateWriterRecordsFromSources,
  validateFrontendStateOwnership,
} from './frontend-state-ownership-guard.mjs';

const appRoot = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..');
const registry = JSON.parse(fs.readFileSync(path.join(appRoot, 'scripts/frontend-state-ownership-registry.json'), 'utf8'));
const terminalWriterPath = 'src/entities/client/model/helpers/assistantEventRuntime.js';

describe('frontend state ownership guard', () => {
  it('[A02-production] validates production writers against the exact registry', () => {
    expect(validateFrontendStateOwnership({ root: appRoot })).toEqual({
      'action-failure-health': expect.objectContaining({ writerCount: 3 }),
      'public-error-diagnostics': expect.objectContaining({ writerCount: 3 }),
      'terminal-truth': expect.objectContaining({ writerCount: 1 }),
    });

    const relativePath = 'src/cache-proof.js';
    const source = 'export const project = (outcome) => ({ terminalOutcome: outcome });';
    const sources = new Map([[relativePath, source]]);
    const analysisCache = new Map();
    const expected = discoverStateWriterRecordsFromSources(
      sources,
      ['terminalOutcome'],
      new Map(),
      analysisCache,
    );
    const rejectingParseCache = {
      get() {
        throw new Error('analysis cache unexpectedly missed unchanged source');
      },
    };
    expect(discoverStateWriterRecordsFromSources(
      sources,
      ['terminalOutcome'],
      rejectingParseCache,
      analysisCache,
    )).toEqual(expected);
    expect(() => discoverStateWriterRecordsFromSources(
      new Map([[relativePath, `${source}\nexport const changed = true;`]]),
      ['terminalOutcome'],
      rejectingParseCache,
      analysisCache,
    )).toThrow('analysis cache unexpectedly missed unchanged source');
  }, 15000);

  it('[A02-missing-writer] rejects a production writer missing from the registry', () => {
    const mutated = clone(registry);
    mutated.states['terminal-truth'].writers = [];
    expect(() => validateFrontendStateOwnership({ root: appRoot, registry: mutated }))
      .toThrow('terminal-truth must register at least one writer');
  });

  it('[A02-stale-writer] rejects a stale registry writer', () => {
    const mutated = clone(registry);
    mutated.states['terminal-truth'].writers.push({
      key: 'src/entities/client/model/fake.js:writeTerminal:object-property:terminalOutcome',
      value: 'terminal.outcome',
      role: 'projector',
    });
    expect(() => validateFrontendStateOwnership({ root: appRoot, registry: mutated }))
      .toThrow('stale=');
  });

  it('[A02-second-writer] rejects a second object writer outside the owner', () => {
    const source = `${read(terminalWriterPath)}
      export function secondTerminalWriter(outcome) {
        return { terminalOutcome: outcome };
      }
    `;
    expect(() => validateFrontendStateOwnership({
      root: appRoot,
      sourceOverrides: new Map([[terminalWriterPath, source]]),
    })).toThrow('secondTerminalWriter:object-property:terminalOutcome');
  });

  it('[A02-alias-indirect] rejects aliased and indirect member mutation', () => {
    const source = `${read(terminalWriterPath)}
      export function mutateTerminalThroughAlias(state) {
        const alias = state;
        alias.terminalOutcome = 'failed';
        return alias;
      }
    `;
    expect(() => validateFrontendStateOwnership({
      root: appRoot,
      sourceOverrides: new Map([[terminalWriterPath, source]]),
    })).toThrow('mutateTerminalThroughAlias:member-mutation:terminalOutcome');
  });

  it('[A02-computed-key] rejects a writer hidden behind a constant computed key', () => {
    const source = `${read(terminalWriterPath)}
      export function computedTerminalWriter(outcome) {
        let terminalKey = 'terminalOutcome';
        return { [terminalKey]: outcome };
      }
    `;
    expect(() => validateFrontendStateOwnership({
      root: appRoot,
      sourceOverrides: new Map([[terminalWriterPath, source]]),
    })).toThrow('computedTerminalWriter:object-property:terminalOutcome');
  });

  it('[A02-lexical-binding] keeps a computed writer visible when another scope shadows its key name', () => {
    const source = `${read(terminalWriterPath)}
      export function scopedComputedTerminalWriter(outcome) {
        const terminalKey = 'terminalOutcome';
        function unrelatedScope() {
          const terminalKey = 'notTerminalOutcome';
          return terminalKey;
        }
        unrelatedScope();
        return { [terminalKey]: outcome };
      }
    `;
    expect(() => validateFrontendStateOwnership({
      root: appRoot,
      sourceOverrides: new Map([[terminalWriterPath, source]]),
    })).toThrow('scopedComputedTerminalWriter:object-property:terminalOutcome');
  });

  it('[A02-reassigned-binding] does not resolve a reassigned key as its old string constant', () => {
    const source = `${read(terminalWriterPath)}
      export function reassignedComputedWriter(outcome) {
        let terminalKey = 'terminalOutcome';
        terminalKey = 'notTerminalOutcome';
        return { [terminalKey]: outcome };
      }
    `;
    expect(validateFrontendStateOwnership({
      root: appRoot,
      sourceOverrides: new Map([[terminalWriterPath, source]]),
    })['terminal-truth'].writerCount).toBe(1);
  });

  it('[A02-deleted-writer] rejects deletion of the registered production writer', () => {
    const original = read(terminalWriterPath);
    const source = original.replace('terminalOutcome: terminal.outcome,', 'outcomeLabel: terminal.outcome,');
    expect(source).not.toBe(original);
    expect(() => validateFrontendStateOwnership({
      root: appRoot,
      sourceOverrides: new Map([[terminalWriterPath, source]]),
    })).toThrow('stale=');
  });

  it('[A02-mapper-drift] rejects a registered writer whose source mapper changes', () => {
    const original = read(terminalWriterPath);
    const source = original.replace(
      'terminalOutcome: terminal.outcome,',
      'terminalOutcome: terminal.terminationCause,',
    );
    expect(source).not.toBe(original);
    expect(() => validateFrontendStateOwnership({
      root: appRoot,
      sourceOverrides: new Map([[terminalWriterPath, source]]),
    })).toThrow('value=terminal.terminationCause');
  });

  it('[A02-new-consumer] rejects a newly discovered reverse consumer missing from the registry', () => {
    const source = `${read(terminalWriterPath)}
      export function readTerminalOutcome(state) {
        return state.terminalOutcome;
      }
    `;
    expect(() => validateFrontendStateOwnership({
      root: appRoot,
      sourceOverrides: new Map([[terminalWriterPath, source]]),
    })).toThrow('readTerminalOutcome:member-read:terminalOutcome');
  });

  it('[A02-stale-consumer] rejects a stale registered reverse consumer', () => {
    const mutated = clone(registry);
    mutated.states['terminal-truth'].consumers.push({
      key: 'src/pages/chat/Fake.jsx:Fake:member-read:terminalOutcome',
      role: 'renderer',
    });
    expect(() => validateFrontendStateOwnership({ root: appRoot, registry: mutated }))
      .toThrow('stale=');
  });

  it('[A02-zero-tests] rejects a registry with zero regression cases', () => {
    const mutated = clone(registry);
    mutated.caseIds = [];
    expect(() => validateFrontendStateOwnership({ root: appRoot, registry: mutated }))
      .toThrow('must register at least one regression case');
  });

  it('[A02-false-positive] ignores ordinary same-name functions without a state write', () => {
    const source = `${read(terminalWriterPath)}
      export function terminalOutcome(value) {
        return String(value);
      }
    `;
    expect(validateFrontendStateOwnership({
      root: appRoot,
      sourceOverrides: new Map([[terminalWriterPath, source]]),
    })['terminal-truth'].writerCount).toBe(1);
  });
});

function read(relativePath) {
  return fs.readFileSync(path.join(appRoot, relativePath), 'utf8');
}

function clone(value) {
  return JSON.parse(JSON.stringify(value));
}
