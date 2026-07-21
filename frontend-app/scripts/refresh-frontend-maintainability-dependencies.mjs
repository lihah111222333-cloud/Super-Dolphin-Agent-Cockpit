import { execFileSync } from 'node:child_process';
import {
  copyFileSync,
  mkdtempSync,
  rmSync,
  writeFileSync,
} from 'node:fs';
import { tmpdir } from 'node:os';
import path from 'node:path';
import process from 'node:process';
import { fileURLToPath } from 'node:url';
import {
  DEPENDENCY_INSTALL_ARGS,
  DEPENDENCY_INSTALL_COMMAND,
  currentDependencyEnvironment,
  dependencyEnvironmentProfile,
  dependencyIntegrityForTree,
  dependencyTreeIntegrity,
  validateDependencyIntegrity,
} from './frontend-maintainability-dependency-integrity.mjs';

const scriptRoot = path.dirname(fileURLToPath(import.meta.url));
const appRoot = path.resolve(scriptRoot, '..');
const defaultOutputPath = path.join(scriptRoot, 'frontend-maintainability-dependencies.json');

function outputPathFromArguments(argv) {
  if (argv.length === 0) return defaultOutputPath;
  if (argv.length === 2 && argv[0] === '--output' && argv[1]) return path.resolve(argv[1]);
  throw new Error('usage: node scripts/refresh-frontend-maintainability-dependencies.mjs [--output <path>]');
}

const outputPath = outputPathFromArguments(process.argv.slice(2));
const environment = currentDependencyEnvironment();
const profileId = dependencyEnvironmentProfile(environment);
const temporaryAppRoot = mkdtempSync(path.join(tmpdir(), 'frontend-maintainability-dependency-refresh-'));

try {
  copyFileSync(path.join(appRoot, 'package.json'), path.join(temporaryAppRoot, 'package.json'));
  copyFileSync(path.join(appRoot, 'package-lock.json'), path.join(temporaryAppRoot, 'package-lock.json'));
  execFileSync(DEPENDENCY_INSTALL_COMMAND, [...DEPENDENCY_INSTALL_ARGS], {
    cwd: temporaryAppRoot,
    env: process.env,
    stdio: 'ignore',
    timeout: 180_000,
  });
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
