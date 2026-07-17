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

  it('fails when production adds an unregistered validator consumer', () => {
    const source = read(runtimePath);
    const mutated = `${source}\nexport function unregisteredTerminalConsumer(payload) { return validateTurnTerminalV2(payload); }\n`;
    expect(() => validateTurnContractFieldGuard({
      repoRoot,
      sourceOverrides: new Map([[runtimePath, mutated]]),
    })).toThrow('TurnTerminalV2 JS production consumers');
  });

  it('fails when production adds an unregistered exported arrow consumer', () => {
    const source = read(runtimePath);
    const mutated = `${source}\nexport const unregisteredArrowConsumer = (payload) => validateTurnTerminalV2(payload);\n`;
    expect(() => validateTurnContractFieldGuard({
      repoRoot,
      sourceOverrides: new Map([[runtimePath, mutated]]),
    })).toThrow('unregisteredArrowConsumer');
  });

  it('fails when production adds an unregistered function expression consumer', () => {
    const source = read(runtimePath);
    const mutated = `${source}\nexport const unregisteredExpressionConsumer = function (payload) { return validateTurnTerminalV2(payload); };\n`;
    expect(() => validateTurnContractFieldGuard({
      repoRoot,
      sourceOverrides: new Map([[runtimePath, mutated]]),
    })).toThrow('unregisteredExpressionConsumer');
  });

  it('fails when production adds an unregistered object method consumer', () => {
    const source = read(runtimePath);
    const mutated = `${source}\nexport const unregisteredConsumers = { parse(payload) { return validateTurnTerminalV2(payload); } };\n`;
    expect(() => validateTurnContractFieldGuard({
      repoRoot,
      sourceOverrides: new Map([[runtimePath, mutated]]),
    })).toThrow('unregisteredConsumers.parse');
  });

  it('fails when production adds an unregistered class method consumer', () => {
    const source = read(runtimePath);
    const mutated = `${source}\nexport class UnregisteredConsumers { parse(payload) { return validateTurnTerminalV2(payload); } }\n`;
    expect(() => validateTurnContractFieldGuard({
      repoRoot,
      sourceOverrides: new Map([[runtimePath, mutated]]),
    })).toThrow('UnregisteredConsumers.parse');
  });

  it.each([
    ['exported arrow', 'export const registeredConsumer = (payload) => validateTurnTerminalV2(payload);', 'registeredConsumer'],
    ['object method', 'export const registeredConsumers = { parse(payload) { return validateTurnTerminalV2(payload); } };', 'registeredConsumers.parse'],
  ])('resolves a registered %s consumer by exact path, symbol, and call', (_label, declaration, symbol) => {
    const source = `${read(runtimePath)}\n${declaration}\n`;
    const registry = JSON.parse(read(registryPath));
    registry.schemas.TurnTerminalV2.jsConsumers.push({
      path: runtimePath,
      symbol,
      calls: 'validateTurnTerminalV2',
    });
    expect(validateTurnContractFieldGuard({
      repoRoot,
      sourceOverrides: new Map([
        [runtimePath, source],
        [registryPath, JSON.stringify(registry)],
      ]),
    })).toEqual({ schemaCount: 3, mapperCount: 1 });
  });

  it('fails when a registered consumer call target drifts', () => {
    const registry = JSON.parse(read(registryPath));
    registry.schemas.TurnTerminalV2.jsConsumers[0].calls = 'validateTurnRefV1';
    expect(() => validateTurnContractFieldGuard({
      repoRoot,
      sourceOverrides: new Map([[registryPath, JSON.stringify(registry)]]),
    })).toThrow('missing call validateTurnRefV1');
  });

  it('fails when a validator call cannot be attributed to a stable production symbol', () => {
    const source = read(runtimePath);
    const mutated = `${source}\nexport default ((payload) => validateTurnTerminalV2(payload));\n`;
    expect(() => validateTurnContractFieldGuard({
      repoRoot,
      sourceOverrides: new Map([[runtimePath, mutated]]),
    })).toThrow('cannot be attributed to a stable production symbol');
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
