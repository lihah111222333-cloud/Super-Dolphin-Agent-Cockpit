import { spawnSync } from 'node:child_process';
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { describe, expect, it } from 'vitest';

const appRoot = process.cwd();
const safeRejectedProjection = [
  'code=BASELINE_PUBLIC_ERROR_CONTRACT_REJECTED',
  'phase=stderr-projection',
  'recoveryAction=inspect-error-contract-without-outputting-private-data',
].join(' ');

describe('frontend code size guard stderr projection', () => {
  it('fails closed without leaking an unknown public code, raw error, path, or stack', () => {
    const fixtureRoot = fs.realpathSync(fs.mkdtempSync(path.join(os.tmpdir(), 'frontend-code-size-stderr-')));
    const scriptDir = path.join(fixtureRoot, 'scripts');
    const libDir = path.join(scriptDir, 'lib');
    const guardPath = path.join(scriptDir, 'frontend-code-size-guard.mjs');
    const unknownCode = 'BASELINE_SENTINEL_UNKNOWN_RAW_CODE';
    const rawMessage = 'SENTINEL_RAW_PRIMARY_MESSAGE';
    try {
      fs.mkdirSync(libDir, { recursive: true });
      fs.mkdirSync(path.join(fixtureRoot, 'src'));
      fs.mkdirSync(path.join(fixtureRoot, 'node_modules/@babel'), { recursive: true });
      fs.cpSync(
        path.join(appRoot, 'node_modules/@babel/parser'),
        path.join(fixtureRoot, 'node_modules/@babel/parser'),
        { recursive: true },
      );
      for (const fileName of [
        'frontend-code-size-baseline.mjs',
        'frontend-code-size-baseline-transaction.mjs',
        'frontend-code-size-guard-runner.mjs',
      ]) {
        fs.copyFileSync(path.join(appRoot, 'scripts/lib', fileName), path.join(libDir, fileName));
      }
      const guardSource = fs.readFileSync(path.join(appRoot, 'scripts/frontend-code-size-guard.mjs'), 'utf8');
      const injectedError = [
        `const injected = new Error(${JSON.stringify(`${fixtureRoot} ${rawMessage}`)});`,
        `Object.assign(injected, { code: ${JSON.stringify(unknownCode)}, phase: 'sentinel-phase', recoveryAction: 'sentinel-action' });`,
        'throw injected;',
      ].join(' ');
      fs.writeFileSync(guardPath, guardSource.replace('    main();', `    ${injectedError}`));

      const result = spawnSync(process.execPath, [guardPath, '--check', '--scope', 'production', '--dir', 'src'], {
        cwd: fixtureRoot,
        encoding: 'utf8',
      });
      expect(result.status).not.toBe(0);
      expect(result.stdout).toBe('');
      expect(result.stderr.trim()).toBe(`frontend code size guard: ${safeRejectedProjection}`);
      expect(result.stderr).not.toContain(unknownCode);
      expect(result.stderr).not.toContain(rawMessage);
      expect(result.stderr).not.toContain(fixtureRoot);
      expect(result.stderr).not.toContain('frontend-code-size-baseline-transaction.mjs:');
    } finally {
      fs.rmSync(fixtureRoot, { recursive: true, force: true });
    }
  });
});
