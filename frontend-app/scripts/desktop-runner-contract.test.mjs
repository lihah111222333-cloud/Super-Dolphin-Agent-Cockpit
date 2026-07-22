import { spawnSync } from 'node:child_process';
import { readFile } from 'node:fs/promises';
import { resolve } from 'node:path';
import { execPath } from 'node:process';
import { describe, expect, it } from 'vitest';

const repoRoot = resolve(import.meta.dirname, '..', '..');
const desktopSmokeCodexStub = resolve(import.meta.dirname, 'desktop-smoke-codex-stub.mjs');

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
  it('prepares the embedded frontend after Vite is ready and before agent-terminal starts', async () => {
    const bash = await scriptText('run-new-ui-desktop.sh');

    expect(bash).toContain('make frontend-build');
    expect(bash.lastIndexOf('wait_for_http "$FRONTEND_DEVSERVER_URL"')).toBeLessThan(
      bash.lastIndexOf('ensure_embedded_frontend'),
    );
    expect(bash.lastIndexOf('ensure_embedded_frontend')).toBeLessThan(
      bash.lastIndexOf('start_desktop_backend'),
    );
  });

  it('keeps the desktop UX suite separate from the failure-host fixture', async () => {
    const desktopConfig = await scriptText('frontend-app/playwright.desktop.config.js');

    expect(desktopConfig).toContain("testMatch: 'desktop-ux.spec.js'");
  });

  it('forces the strict Codex stub unless the smoke explicitly exercises a provider turn', async () => {
    const bash = await scriptText('run-new-ui-desktop.sh');
    const smokeSelection = '[ "${SUPER_DOLPHIN_DESKTOP_SMOKE_ACTIVE:-}" = "1" ]';
    const stubPath = 'frontend-app/scripts/desktop-smoke-codex-stub.mjs';

    expect(bash).toContain(smokeSelection);
    expect(bash).toContain('case "${SUPER_DOLPHIN_DESKTOP_SMOKE_TURN:-}" in');
    expect(bash).toContain('1|true|yes) ;;');
    expect(bash).toContain(stubPath);
    expect(bash.indexOf(smokeSelection)).toBeLessThan(bash.indexOf('command -v codex'));
  });

  it('allows only the Codex app-server help probe through the desktop smoke stub', () => {
    const help = spawnSync(execPath, [desktopSmokeCodexStub, 'app-server', '--help'], { encoding: 'utf8' });
    expect(help.status, help.stderr).toBe(0);

    const rejectedArgumentLists = [[], ['--version'], ['app-server'], ['app-server', '--listen', 'stdio']];
    for (const args of rejectedArgumentLists) {
      const rejected = spawnSync(execPath, [desktopSmokeCodexStub, ...args], { encoding: 'utf8' });
      expect(rejected.status, JSON.stringify(args)).not.toBe(0);
      expect(rejected.stderr).toContain('refuses provider execution');
    }
  });
});
