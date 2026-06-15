import { readFile } from 'node:fs/promises';
import { resolve } from 'node:path';
import { describe, expect, it } from 'vitest';

const repoRoot = resolve(import.meta.dirname, '..', '..');

async function scriptText(name) {
  return readFile(resolve(repoRoot, name), 'utf8');
}

describe('new UI desktop runner contract', () => {
  it('keeps Vite readiness before backend readiness in both launchers', async () => {
    const bash = await scriptText('run-new-ui-desktop.sh');
    const powershell = await scriptText('run-new-ui-desktop.ps1');

    expect(bash.lastIndexOf('wait_for_http "$FRONTEND_DEVSERVER_URL"')).toBeLessThan(
      bash.lastIndexOf('wait_for_backend'),
    );
    expect(powershell.lastIndexOf('Wait-ForHttp -Url $env:FRONTEND_DEVSERVER_URL')).toBeLessThan(
      powershell.lastIndexOf('Wait-ForBackend'),
    );
  });

  it('requires FRONTEND_DEVSERVER_URL to match VITE_DEV_URL in both launchers', async () => {
    const bash = await scriptText('run-new-ui-desktop.sh');
    const powershell = await scriptText('run-new-ui-desktop.ps1');

    expect(bash).toContain('FRONTEND_DEVSERVER_URL must match VITE_DEV_URL');
    expect(powershell).toContain('FRONTEND_DEVSERVER_URL must match VITE_DEV_URL');
  });
});
