import fs from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

import { describe, expect, it } from 'vitest';

import { validateTurnContractFieldGuard } from './turn-contract-field-guard.mjs';

const repoRoot = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '../..');
const mapperPath = 'frontend-app/src/shared/api/backend/backendApiFactoryThread.js';
const runtimePath = 'frontend-app/src/entities/client/model/runtimeAssistantTimeline.js';
const registryPath = 'internal/dto/turn/schema/field_consumers.json';
const barrelPath = 'frontend-app/src/shared/contracts/turnContractBarrel.js';

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

  it('fails when a direct namespace import adds an unregistered consumer', () => {
    const source = `${read(runtimePath)}
import * as directValidators from '../../../shared/contracts/turnContractValidators.js';
export function directNamespaceConsumer(payload) { return directValidators.validateTurnTerminalV2(payload); }
`;
    expect(() => validateTurnContractFieldGuard({
      repoRoot,
      sourceOverrides: new Map([[runtimePath, source]]),
    })).toThrow('directNamespaceConsumer');
  });

  it('fails fast when a named validator binding escapes through an assignment', () => {
    const source = `${read(runtimePath)}
const escapedValidator = validateTurnTerminalV2;
export function namedEscape(payload) { return escapedValidator(payload); }
`;
    expect(() => validateTurnContractFieldGuard({
      repoRoot,
      sourceOverrides: new Map([[runtimePath, source]]),
    })).toThrow('validator binding validateTurnTerminalV2 escapes direct calls');
  });

  it('fails fast when a namespace validator member escapes through an assignment', () => {
    const source = `${read(runtimePath)}
import * as validators from '../../../shared/contracts/turnContractBarrel.js';
const escapedValidator = validators.validateTurnTerminalV2;
export function namespaceEscape(payload) { return escapedValidator(payload); }
`;
    const barrel = "export { validateTurnTerminalV2 } from './turnContractValidators.js';\n";
    expect(() => validateTurnContractFieldGuard({
      repoRoot,
      sourceOverrides: new Map([
        [runtimePath, source],
        [barrelPath, barrel],
      ]),
    })).toThrow('validator namespace member validators.validateTurnTerminalV2 escapes direct calls');
  });

  it.each([
    ['named optional call', 'validateTurnTerminalV2?.(payload)'],
    ['namespace optional member call', 'validators?.validateTurnTerminalV2(payload)'],
  ])('fails fast for a %s', (_label, expression) => {
    const namespaceImport = expression.startsWith('validators')
      ? "import * as validators from '../../../shared/contracts/turnContractValidators.js';\n"
      : '';
    const source = `${read(runtimePath)}\n${namespaceImport}export function optionalEscape(payload) { return ${expression}; }\n`;
    expect(() => validateTurnContractFieldGuard({
      repoRoot,
      sourceOverrides: new Map([[runtimePath, source]]),
    })).toThrow('escapes direct calls');
  });

  it.each([
    [
      'named import',
      "import { validateTurnTerminalV2 as validateBarrelTerminal } from '../../../shared/contracts/turnContractBarrel.js';",
      'validateBarrelTerminal(payload)',
      'barrelNamedConsumer',
    ],
    [
      'namespace import',
      "import * as barrelValidators from '../../../shared/contracts/turnContractBarrel.js';",
      'barrelValidators.validateTurnTerminalV2(payload)',
      'barrelNamespaceConsumer',
    ],
  ])('fails when a one-level barrel %s adds an unregistered consumer', (_label, importLine, call, symbol) => {
    const source = `${read(runtimePath)}\n${importLine}\nexport function ${symbol}(payload) { return ${call}; }\n`;
    const barrel = "export { validateTurnTerminalV2 } from './turnContractValidators.js';\n";
    expect(() => validateTurnContractFieldGuard({
      repoRoot,
      sourceOverrides: new Map([
        [runtimePath, source],
        [barrelPath, barrel],
      ]),
    })).toThrow(symbol);
  });

  it.each([
    [
      'direct namespace',
      "import * as registeredValidators from '../../../shared/contracts/turnContractValidators.js';",
      'registeredValidators.validateTurnTerminalV2(payload)',
      'registeredDirectNamespaceConsumer',
      '',
    ],
    [
      'barrel named import',
      "import { validateTurnTerminalV2 as registeredBarrelValidator } from '../../../shared/contracts/turnContractBarrel.js';",
      'registeredBarrelValidator(payload)',
      'registeredBarrelNamedConsumer',
      "export { validateTurnTerminalV2 } from './turnContractValidators.js';\n",
    ],
    [
      'barrel namespace import',
      "import * as registeredBarrelValidators from '../../../shared/contracts/turnContractBarrel.js';",
      'registeredBarrelValidators.validateTurnTerminalV2(payload)',
      'registeredBarrelNamespaceConsumer',
      "export { validateTurnTerminalV2 } from './turnContractValidators.js';\n",
    ],
  ])('resolves a registered %s consumer by exact path, symbol, and call', (_label, importLine, call, symbol, barrel) => {
    const source = `${read(runtimePath)}\n${importLine}\nexport function ${symbol}(payload) { return ${call}; }\n`;
    const registry = JSON.parse(read(registryPath));
    registry.schemas.TurnTerminalV2.jsConsumers.push({
      path: runtimePath,
      symbol,
      calls: 'validateTurnTerminalV2',
    });
    const overrides = new Map([
      [runtimePath, source],
      [registryPath, JSON.stringify(registry)],
    ]);
    if (barrel) overrides.set(barrelPath, barrel);
    expect(validateTurnContractFieldGuard({ repoRoot, sourceOverrides: overrides }))
      .toEqual({ schemaCount: 3, mapperCount: 1 });
  });

  it('fails fast when export-all lets validator bindings escape exact analysis', () => {
    const source = `${read(runtimePath)}
import { validateTurnTerminalV2 as escapedValidator } from '../../../shared/contracts/turnContractBarrel.js';
export function exportAllConsumer(payload) { return escapedValidator(payload); }
`;
    const barrel = "export * from './turnContractValidators.js';\n";
    expect(() => validateTurnContractFieldGuard({
      repoRoot,
      sourceOverrides: new Map([
        [runtimePath, source],
        [barrelPath, barrel],
      ]),
    })).toThrow('validator export escape');
  });

  it('fails when a validator call is hidden in a nested callback', () => {
    const source = `${read(runtimePath)}
export function nestedCallbackConsumer(payloads) {
  return payloads.map((payload) => validateTurnTerminalV2(payload));
}
`;
    expect(() => validateTurnContractFieldGuard({
      repoRoot,
      sourceOverrides: new Map([[runtimePath, source]]),
    })).toThrow('cannot be attributed to a stable production symbol');
  });

  it('does not treat an ordinary same-named member call as a validator consumer', () => {
    const source = `${read(runtimePath)}
export function ordinarySameNamedMember(payload) {
  const ordinary = { validateTurnTerminalV2: (value) => value };
  return ordinary.validateTurnTerminalV2(payload);
}
`;
    expect(validateTurnContractFieldGuard({
      repoRoot,
      sourceOverrides: new Map([[runtimePath, source]]),
    })).toEqual({ schemaCount: 3, mapperCount: 1 });
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
