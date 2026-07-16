import fs from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

import { describe, expect, it } from 'vitest';

import { validateTurnContractFieldGuard } from './turn-contract-field-guard.mjs';

const repoRoot = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '../..');
const mapperPath = 'frontend-app/src/shared/api/backend/backendApiFactoryThread.js';
const runtimePath = 'frontend-app/src/entities/client/model/runtimeAssistantTimeline.js';
const registryPath = 'internal/dto/turn/schema/field_consumers.json';

describe('turn contract production field guard', () => {
  it('resolves canonical schemas, production validators, consumers, and mapper fields', () => {
    expect(validateTurnContractFieldGuard({ repoRoot })).toEqual({ schemaCount: 3, mapperCount: 1 });
  });

  it('fails when the Stop mapper drops expectedTurnId', () => {
    const source = read(mapperPath);
    const mutated = source.replace("    { key: 'expectedTurnId', value: takePayloadField(unused, 'expectedTurnId') },\n", '');
    expect(mutated).not.toBe(source);
    expect(() => validateTurnContractFieldGuard({
      repoRoot,
      sourceOverrides: new Map([[mapperPath, mutated]]),
    })).toThrow('expectedTurnId aliases');
  });

  it('fails when the runtime terminal consumer stops calling its validator', () => {
    const source = read(runtimePath);
    const mutated = source.replace('validateTurnTerminalV2(payload)', 'payload');
    expect(mutated).not.toBe(source);
    expect(() => validateTurnContractFieldGuard({
      repoRoot,
      sourceOverrides: new Map([[runtimePath, mutated]]),
    })).toThrow('missing call validateTurnTerminalV2');
  });

  it('fails when a registry locator becomes stale', () => {
    const source = read(registryPath);
    const mutated = source.replace('"symbol": "parseRuntimeTurnTerminal"', '"symbol": "missingRuntimeTurnTerminal"');
    expect(mutated).not.toBe(source);
    expect(() => validateTurnContractFieldGuard({
      repoRoot,
      sourceOverrides: new Map([[registryPath, mutated]]),
    })).toThrow('resolved 0 production functions');
  });
});

function read(relativePath) {
  return fs.readFileSync(path.join(repoRoot, relativePath), 'utf8');
}
