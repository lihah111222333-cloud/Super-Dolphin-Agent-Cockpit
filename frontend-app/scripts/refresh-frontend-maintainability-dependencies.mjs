import { execFileSync } from 'node:child_process';
import { createHash } from 'node:crypto';
import {
  copyFileSync,
  existsSync,
  mkdtempSync,
  readFileSync,
  rmSync,
  writeFileSync,
} from 'node:fs';
import { tmpdir } from 'node:os';
import path from 'node:path';
import process from 'node:process';
import { fileURLToPath } from 'node:url';
import { isDeepStrictEqual } from 'node:util';
import {
  DEPENDENCY_INSTALL_ARGS,
  DEPENDENCY_INSTALL_COMMAND,
  DEPENDENCY_REQUIRED_TOOL_PATHS,
  assertHiddenPackageLockClosure,
  currentDependencyEnvironment,
  dependencyEnvironmentProfile,
  dependencyIntegrityForTree,
  dependencyTreeIntegrity,
  validateDependencyIntegrity,
} from './frontend-maintainability-dependency-integrity.mjs';
import {
  materializeImmutableDependencyOverlay,
  nodeModulesPath,
  resolveImmutableDependencySeed,
} from './frontend-execution-closure.mjs';

const scriptRoot = path.dirname(fileURLToPath(import.meta.url));
const appRoot = path.resolve(scriptRoot, '..');
const defaultOutputPath = path.join(scriptRoot, 'frontend-maintainability-dependencies.json');
const runtimeContractFields = Object.freeze(['packageLockSha256', 'optionalLockSha256', 'binLockSha256', 'requiredTools']);

function dependencyRuntimeIntegrity(appRoot, environment) {
  const packageLockPath = path.join(appRoot, 'package-lock.json');
  const tree = dependencyTreeIntegrity(appRoot, { environment, runtimeOnly: true });
  assertHiddenPackageLockClosure(appRoot);
  const requiredTools = Object.fromEntries(DEPENDENCY_REQUIRED_TOOL_PATHS.map((relativePath) => {
    const absolutePath = nodeModulesPath(tree.nodeModulesRoot, relativePath);
    if (!existsSync(absolutePath)) throw new Error(`immutable dependency tool is missing: ${relativePath}`);
    return [relativePath, createHash('sha256').update(readFileSync(absolutePath)).digest('hex')];
  }));
  return { packageLockSha256: createHash('sha256').update(readFileSync(packageLockPath)).digest('hex'),
    optionalLockSha256: tree.optionalLockSha256, binLockSha256: tree.binLockSha256, requiredTools };
}

function optionsFromArguments(argv) {
  if (argv.length === 0) return { check: false, outputPath: defaultOutputPath };
  if (argv.length === 1 && argv[0] === '--check') return { check: true };
  if (argv.length === 2 && argv[0] === '--output' && argv[1]) {
    return { check: false, outputPath: path.resolve(argv[1]) };
  }
  throw new Error('usage: node scripts/refresh-frontend-maintainability-dependencies.mjs [--output <path> | --check]');
}

const { check, outputPath } = optionsFromArguments(process.argv.slice(2));
const environment = currentDependencyEnvironment();
const profileId = dependencyEnvironmentProfile(environment);

if (check) {
  const expected = JSON.parse(readFileSync(defaultOutputPath, 'utf8'));
  validateDependencyIntegrity(expected);
  const actual = dependencyRuntimeIntegrity(appRoot, environment);
  const mismatches = runtimeContractFields.filter((field) => !isDeepStrictEqual(actual[field], expected[field]));
  if (mismatches.length > 0) {
    throw new Error(`immutable runtime dependency contract mismatch (${mismatches.join(', ')}): ${defaultOutputPath}`);
  }

  process.stdout.write([
    `ENVIRONMENT_PROFILE\t${profileId}`,
    'DEPENDENCY_INTEGRITY_CHECK\tPASS',
    `VERIFIED_FIELDS\t${runtimeContractFields.join(',')}`,
    `EXPECTED\t${defaultOutputPath}`,
    '',
  ].join('\n'));
} else {
  const temporaryAppRoot = mkdtempSync(path.join(tmpdir(), 'frontend-maintainability-dependency-refresh-'));
  try {
    copyFileSync(path.join(appRoot, 'package.json'), path.join(temporaryAppRoot, 'package.json'));
    copyFileSync(path.join(appRoot, 'package-lock.json'), path.join(temporaryAppRoot, 'package-lock.json'));
    const runtimeSeed = resolveImmutableDependencySeed(appRoot);
    if (runtimeSeed) {
      materializeImmutableDependencyOverlay(temporaryAppRoot, runtimeSeed);
    } else {
      execFileSync(DEPENDENCY_INSTALL_COMMAND, [...DEPENDENCY_INSTALL_ARGS], {
        cwd: temporaryAppRoot,
        env: process.env,
        stdio: 'ignore',
        timeout: 180_000,
      });
    }
    const document = dependencyIntegrityForTree(temporaryAppRoot, environment);
    validateDependencyIntegrity(document);
    const tree = dependencyTreeIntegrity(temporaryAppRoot, {
      environment,
      expectedOptionalLockSha256: document.optionalLockSha256,
    });
    writeFileSync(outputPath, `${JSON.stringify(document, null, 2)}\n`);

    process.stdout.write([
      `ENVIRONMENT_PROFILE\t${profileId}`,
      `EXCLUDED_OPTIONAL_ROOTS\t${JSON.stringify(tree.excludedOptionalRoots)}`,
      `NEUTRAL_TREE_SHA256\t${tree.sha256}`,
      `NEUTRAL_PATH_COUNT\t${tree.pathCount}`,
      `OPTIONAL_LOCK_SHA256\t${tree.optionalLockSha256}`,
      `OPTIONAL_SELECTION_SHA256\t${tree.optionalSelectionSha256}`,
      `BIN_LOCK_SHA256\t${tree.binLockSha256}`,
      `OUTPUT\t${outputPath}`,
      '',
    ].join('\n'));
  }
  finally {
    rmSync(temporaryAppRoot, { recursive: true, force: true });
  }
}
