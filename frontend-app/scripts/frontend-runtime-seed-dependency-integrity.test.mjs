import { cpSync, mkdtempSync, mkdirSync, readFileSync, rmSync, symlinkSync, writeFileSync } from 'node:fs';
import { createHash } from 'node:crypto';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { afterEach, expect, it } from 'vitest';

import {
  DEPENDENCY_REQUIRED_TOOL_PATHS,
  dependencyTreeIntegrity,
} from './frontend-maintainability-dependency-integrity.mjs';
import {
  materializeImmutableDependencyOverlay,
  resolveImmutableDependencySeed,
} from './frontend-execution-closure.mjs';

const temporaryDirectories = [];

afterEach(() => {
  while (temporaryDirectories.length > 0) rmSync(temporaryDirectories.pop(), { recursive: true, force: true });
});

function createDependencyIntegrityFixture() {
  const appRoot = mkdtempSync(join(tmpdir(), 'frontend-runtime-seed-app-'));
  temporaryDirectories.push(appRoot);
  const nodeModulesRoot = join(appRoot, 'node_modules');
  const packageRoot = join(nodeModulesRoot, 'fixture');
  const packageRecord = {
    version: '1.2.3',
    resolved: 'https://registry.example.invalid/fixture-1.2.3.tgz',
    integrity: 'sha512-fixture-integrity',
    optional: true,
    os: ['darwin', 'linux'],
    cpu: ['arm64', 'x64'],
    bin: { 'fixture-tool': 'tool.js' },
  };
  const packageLock = { name: 'fixture-app', version: '1.0.0', lockfileVersion: 3, requires: true, packages: { '': { name: 'fixture-app', version: '1.0.0' }, 'node_modules/fixture': packageRecord } };
  mkdirSync(join(nodeModulesRoot, '.bin'), { recursive: true });
  mkdirSync(packageRoot, { recursive: true });
  writeFileSync(join(appRoot, 'package-lock.json'), `${JSON.stringify(packageLock, null, 2)}\n`);
  writeFileSync(join(packageRoot, 'package.json'), '{"name":"fixture","version":"1.2.3"}\n');
  writeFileSync(join(packageRoot, 'tool.js'), '#!/usr/bin/env node\nexport const version = 1;\n');
  symlinkSync('../fixture/tool.js', join(nodeModulesRoot, '.bin', 'fixture-tool'));
  writeFileSync(join(nodeModulesRoot, '.package-lock.json'), JSON.stringify({ ...packageLock, packages: { 'node_modules/fixture': packageRecord } }));
  return { appRoot, nodeModulesRoot };
}

function addRequiredToolFixtures(fixture) {
  for (const relativePath of DEPENDENCY_REQUIRED_TOOL_PATHS) {
    const absolutePath = join(fixture.nodeModulesRoot, relativePath);
    mkdirSync(join(absolutePath, '..'), { recursive: true });
    writeFileSync(absolutePath, `#!/usr/bin/env node\nexport const tool = ${JSON.stringify(relativePath)};\n`);
  }
}

function runtimeContract(fixture) {
  const tree = dependencyTreeIntegrity(fixture.appRoot, { runtimeOnly: true });
  const requiredTools = Object.fromEntries(DEPENDENCY_REQUIRED_TOOL_PATHS.map((relativePath) => {
    const digest = createHash('sha256').update(readFileSync(join(tree.nodeModulesRoot, relativePath))).digest('hex');
    return [relativePath, digest];
  }));
  return { packageLockSha256: createHash('sha256').update(readFileSync(join(fixture.appRoot, 'package-lock.json'))).digest('hex'),
    optionalLockSha256: tree.optionalLockSha256, binLockSha256: tree.binLockSha256, requiredTools };
}

it('uses only the lock/bin/tool runtime contract during normal dependency checks', () => {
  const fixture = createDependencyIntegrityFixture();
  addRequiredToolFixtures(fixture);
  const before = runtimeContract(fixture);
  const treeBefore = dependencyTreeIntegrity(fixture.appRoot);
  writeFileSync(join(fixture.nodeModulesRoot, 'unrelated-runtime-file.js'), 'export const unrelated = true;\n');
  const after = runtimeContract(fixture);
  const treeAfter = dependencyTreeIntegrity(fixture.appRoot);

  expect(after).toEqual(before);
  expect(treeAfter.sha256).not.toBe(treeBefore.sha256);
});

it('uses a verified read-only seed through physical and image-linked Vite overlays', () => {
  const fixture = createDependencyIntegrityFixture();
  const original = dependencyTreeIntegrity(fixture.appRoot);
  expect(resolveImmutableDependencySeed(fixture.appRoot)).toBeUndefined();
  const seedContainer = mkdtempSync(join(tmpdir(), 'frontend-runtime-seed-'));
  temporaryDirectories.push(seedContainer);
  const seedRoot = join(seedContainer, 'node_modules');
  cpSync(fixture.nodeModulesRoot, seedRoot, { recursive: true, dereference: false });
  rmSync(join(seedRoot, '.bin', 'fixture-tool'));
  symlinkSync('../fixture/tool.js', join(seedRoot, '.bin', 'fixture-tool'));
  rmSync(fixture.nodeModulesRoot, { recursive: true });
  materializeImmutableDependencyOverlay(fixture.appRoot, seedRoot);
  expect(resolveImmutableDependencySeed(fixture.appRoot)).toBe(seedRoot);
  const { nodeModulesRoot: _originalRoot, ...expected } = original;
  const { nodeModulesRoot: _inferredRoot, ...inferred } = dependencyTreeIntegrity(fixture.appRoot);
  expect(inferred).toEqual(expected);
  const previousSeed = process.env.SUPER_DOLPHIN_FRONTEND_DEPENDENCY_SEED;
  process.env.SUPER_DOLPHIN_FRONTEND_DEPENDENCY_SEED = seedRoot;
  try {
    const { nodeModulesRoot: _overlayRoot, ...actual } = dependencyTreeIntegrity(fixture.appRoot);
    expect(actual).toEqual(expected);
    const viteSeed = join(seedContainer, 'vite-cache');
    mkdirSync(viteSeed);
    rmSync(join(fixture.nodeModulesRoot, '.vite'), { recursive: true });
    symlinkSync(viteSeed, join(fixture.nodeModulesRoot, '.vite'));
    const { nodeModulesRoot: _linkedRoot, ...linked } = dependencyTreeIntegrity(fixture.appRoot);
    expect(linked).toEqual(expected);

    const privateCacheContainer = mkdtempSync(join(tmpdir(), 'frontend-private-vite-cache-'));
    temporaryDirectories.push(privateCacheContainer);
    const privateViteCache = join(privateCacheContainer, '.vite-temp');
    mkdirSync(privateViteCache);
    expect(privateViteCache.startsWith(seedRoot)).toBe(false);
    expect(privateViteCache).not.toBe(join(fixture.nodeModulesRoot, '.vite-temp'));
    mkdirSync(join(fixture.nodeModulesRoot, '.vite-temp'));
    let mismatch;
    try {
      dependencyTreeIntegrity(fixture.appRoot);
    } catch (error) {
      mismatch = error;
    }
    expect(mismatch?.message).toBe(
      'immutable dependency overlay entries do not match the configured seed (extra=.vite-temp)',
    );
    expect(mismatch?.message).not.toContain(seedContainer);
  } finally {
    if (previousSeed === undefined) delete process.env.SUPER_DOLPHIN_FRONTEND_DEPENDENCY_SEED;
    else process.env.SUPER_DOLPHIN_FRONTEND_DEPENDENCY_SEED = previousSeed;
  }
});

it('rejects image and private Vite entries in a configured dependency seed', () => {
  const fixture = createDependencyIntegrityFixture();
  const seedContainer = mkdtempSync(join(tmpdir(), 'frontend-runtime-seed-reserved-'));
  temporaryDirectories.push(seedContainer);
  const seedRoot = join(seedContainer, 'node_modules');
  cpSync(fixture.nodeModulesRoot, seedRoot, { recursive: true, dereference: false });
  mkdirSync(join(seedRoot, '.vite'));
  mkdirSync(join(seedRoot, '.vite-temp'));
  const previousSeed = process.env.SUPER_DOLPHIN_FRONTEND_DEPENDENCY_SEED;
  process.env.SUPER_DOLPHIN_FRONTEND_DEPENDENCY_SEED = seedRoot;
  try {
    expect(() => dependencyTreeIntegrity(fixture.appRoot)).toThrow(
      'immutable dependency seed contains reserved overlay entries: .vite,.vite-temp',
    );
  } finally {
    if (previousSeed === undefined) delete process.env.SUPER_DOLPHIN_FRONTEND_DEPENDENCY_SEED;
    else process.env.SUPER_DOLPHIN_FRONTEND_DEPENDENCY_SEED = previousSeed;
  }
});
