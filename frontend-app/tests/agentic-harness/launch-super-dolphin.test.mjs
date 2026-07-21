import { EventEmitter } from 'node:events';
import { mkdtemp, rm } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join } from 'node:path';

import { afterEach, describe, expect, it, vi } from 'vitest';

import {
  allocateAuxiliaryPorts,
  buildProductEnvironment,
  parseAthTargetPort,
  resolveAthNonce,
  runSuperDolphinTarget,
} from './launch-super-dolphin.mjs';
import { BUILD_IDENTITY, SOURCE_ROOT } from './identity.mjs';

const roots = [];
const nonce = 'A'.repeat(43);

afterEach(async () => {
  await Promise.all(roots.splice(0).map((root) => rm(root, { force: true, recursive: true })));
  vi.restoreAllMocks();
});

async function isolatedEnvironment() {
  const root = await mkdtemp(join(tmpdir(), 'super-dolphin-ath-launcher-'));
  roots.push(root);
  return {
    PATH: process.env.PATH,
    HOME: join(root, 'home'),
    TMPDIR: join(root, 'tmp'),
    ATH_TARGET_PORT: '5127',
    ATH_TARGET_NONCE: nonce,
  };
}

async function moduleCache() {
  const root = await mkdtemp(join(tmpdir(), 'super-dolphin-ath-go-mod-'));
  roots.push(root);
  return root;
}

describe('agentic harness product launcher contract', () => {
  it('fails fast for malformed port, nonce, or auxiliary collisions', async () => {
    expect(() => parseAthTargetPort({ ATH_TARGET_PORT: '5175x' })).toThrow(/integer TCP port/);
    expect(() => parseAthTargetPort({ ATH_TARGET_PORT: '0' })).toThrow(/between 1 and 65535/);
    expect(() => resolveAthNonce({ ATH_TARGET_NONCE: 'short' })).toThrow(/256-bit base64url/);
    const env = await isolatedEnvironment();
    const goMod = await moduleCache();
    expect(() => buildProductEnvironment(env, { backendPort: 5127, controlPort: 5129 }, goMod)).toThrow(/distinct TCP ports/);
  });

  it('allocates distinct ports and builds an isolated exact-identity environment', async () => {
    const ports = await allocateAuxiliaryPorts(5127);
    expect(new Set([5127, ports.backendPort, ports.controlPort]).size).toBe(3);
    const env = await isolatedEnvironment();
    const product = buildProductEnvironment(env, { backendPort: 5128, controlPort: 5129 }, await moduleCache());
    expect(product).toMatchObject({
      VITE_DEV_URL: 'http://127.0.0.1:5127',
      SUPER_DOLPHIN_HTTP_ADDR: '127.0.0.1:5128',
      GO_AGENT_CTL_RPC_ADDR: '127.0.0.1:5129',
      SUPER_DOLPHIN_ATH_SOURCE_ROOT: SOURCE_ROOT,
      SUPER_DOLPHIN_ATH_BUILD_IDENTITY: BUILD_IDENTITY,
      GOPROXY: 'off',
      GOTOOLCHAIN: 'local',
    });
  });

  it('launches the repository supervisor and mirrors its clean exit', async () => {
    const env = await isolatedEnvironment();
    const child = Object.assign(new EventEmitter(), {
      exitCode: null,
      signalCode: null,
      kill: vi.fn(),
    });
    const spawnImpl = vi.fn((_command, _args, options) => {
      expect(options.cwd).toBe(SOURCE_ROOT);
      expect(options.env.VITE_DEV_URL).toBe('http://127.0.0.1:5127');
      queueMicrotask(() => child.emit('exit', 0, null));
      return child;
    });
    await expect(runSuperDolphinTarget(env, spawnImpl, await moduleCache())).resolves.toEqual({ code: 0, signal: null });
    expect(spawnImpl).toHaveBeenCalledOnce();
  });
});
