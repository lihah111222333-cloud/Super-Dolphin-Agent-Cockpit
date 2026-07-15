import { readFileSync } from 'node:fs';
import { describe, expect, it } from 'vitest';

const contractsConfig = JSON.parse(readFileSync('tsconfig.contracts.json', 'utf8'));
const backendApiFactoryThreadSource = readFileSync(
  'src/shared/api/backend/backendApiFactoryThread.js',
  'utf8',
);
const forbiddenTypeScriptDirectivePattern = /@ts-(?:nocheck|ignore|expect-error)\b/;
const explicitAnyTypePattern = /(?:@(?:type|param|returns?|typedef|property)\s+\{[^}\n]*\bany\b|\b[A-Za-z_$][\w$]*(?:\.[A-Za-z_$][\w$]*)*\s*<[^>\n]*\bany\b|(?:\b[A-Za-z_$][\w$]*\??|\))\s*:\s*[\w$.[\]{}|&<>,? \t]*\bany\b|\bas\s+any\b)/;

describe('contracts typecheck guard', () => {
  it('includes the required contract entrypoints at their real paths', () => {
    expect(contractsConfig.include).toEqual(expect.arrayContaining([
      'src/shared/api/backendApi.js',
      'src/shared/api/backendApi.contractMatrix.js',
      'src/entities/client/model/helpers/providerPreferences.js',
      'src/shared/api/backend/backendApiFactoryThread.js',
    ]));
    expect(contractsConfig.include).not.toContain(
      'src/entities/client/model/providerPreferences.js',
    );
  });

  it('keeps the fork builder checked by TypeScript', () => {
    expect(backendApiFactoryThreadSource).not.toMatch(forbiddenTypeScriptDirectivePattern);
    expect(backendApiFactoryThreadSource).not.toMatch(explicitAnyTypePattern);
    expect('accepts any supported backend response').not.toMatch(explicitAnyTypePattern);
    expect(backendApiFactoryThreadSource).toMatch(/^\s*\/\/\s*@ts-check\b/m);
  });
});
