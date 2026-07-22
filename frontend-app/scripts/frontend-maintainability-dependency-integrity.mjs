import { execFileSync } from 'node:child_process';
import { createHash } from 'node:crypto';
import fs from 'node:fs';
import path from 'node:path';

export const DEPENDENCY_TREE_ALGORITHM = 'portable-node-modules-integrity-v2';
export const DEPENDENCY_INTEGRITY_GENERATOR = Object.freeze({
  script: 'frontend-app/scripts/refresh-frontend-maintainability-dependencies.mjs',
  revision: 1,
});
export const DEPENDENCY_INSTALL_COMMAND = 'npm';
export const DEPENDENCY_INSTALL_ARGS = Object.freeze([
  'ci', '--ignore-scripts', '--no-audit', '--no-fund', '--offline',
]);
export const DEPENDENCY_REQUIRED_TOOL_PATHS = Object.freeze([
  '@playwright/test/index.js',
  'eslint/bin/eslint.js',
  'vite/bin/vite.js',
  'vitest/vitest.mjs',
]);
export const DEPENDENCY_SUPPORTED_ENVIRONMENTS = Object.freeze([{
  id: 'repository-node-npm-portability-v1',
  node: '^20.19.0 || ^22.13.0 || >=24.0.0',
  npmMajors: [10, 11],
  targets: [
    { platform: 'darwin', arch: 'arm64' },
    { platform: 'darwin', arch: 'x64' },
    { platform: 'linux', arch: 'arm64' },
    { platform: 'linux', arch: 'x64' },
    { platform: 'win32', arch: 'x64' },
  ],
  evidence: [
    '.github/workflows/ci.yml#actions/setup-node:20',
    'build/gate/runtime-deps.lock#linux/arm64',
    'frontend-app/package-lock.json#node_modules/eslint.engines',
    'frontend-app/package-lock.json#node_modules/vite.engines',
    'frontend-app/scripts/frontend-maintainability-score.test.mjs#portable-dependency-integrity',
  ],
}]);

const SHA256_PATTERN = /^[0-9a-f]{64}$/u;
const HIDDEN_LOCK_FIELDS = ['version', 'resolved', 'integrity', 'optional', 'os', 'cpu'];

function fail(message) {
  throw new Error(message);
}

function exactKeys(value, expected, label) {
  if (!value || typeof value !== 'object' || Array.isArray(value)) fail(`${label} must be an object`);
  const actual = Object.keys(value).sort();
  const wanted = [...expected].sort();
  if (JSON.stringify(actual) !== JSON.stringify(wanted)) fail(`${label} keys mismatch`);
}

function hashValue(value) {
  return createHash('sha256').update(value).digest('hex');
}

function canonicalHash(value) {
  return hashValue(JSON.stringify(value));
}

function readJson(filePath, label) {
  try {
    return JSON.parse(fs.readFileSync(filePath, 'utf8'));
  }
  catch (error) {
    fail(`${label} is unreadable: ${filePath}: ${error.message}`);
  }
}

function parseVersion(value, label) {
  const match = /^(\d+)\.(\d+)\.(\d+)(?:[-+].*)?$/u.exec(value || '');
  if (!match) fail(`${label} must be an exact semantic version`);
  return match.slice(1).map(Number);
}

function nodeVersionSupported(version) {
  const [major, minor] = parseVersion(version, 'Node version');
  return (major === 20 && minor >= 19) || (major === 22 && minor >= 13) || major >= 24;
}

function validateSupportedEnvironmentRule(rule) {
  exactKeys(rule, ['id', 'node', 'npmMajors', 'targets', 'evidence'], 'immutable dependency environment rule');
  if (rule.id !== DEPENDENCY_SUPPORTED_ENVIRONMENTS[0].id
    || rule.node !== DEPENDENCY_SUPPORTED_ENVIRONMENTS[0].node
    || JSON.stringify(rule.npmMajors) !== JSON.stringify(DEPENDENCY_SUPPORTED_ENVIRONMENTS[0].npmMajors)
    || JSON.stringify(rule.targets) !== JSON.stringify(DEPENDENCY_SUPPORTED_ENVIRONMENTS[0].targets)
    || JSON.stringify(rule.evidence) !== JSON.stringify(DEPENDENCY_SUPPORTED_ENVIRONMENTS[0].evidence)) {
    fail('immutable dependency environment rule differs from repository CI and engine evidence');
  }
}

export function currentDependencyEnvironment() {
  let npm = /(?:^|\s)npm\/([^\s]+)/u.exec(process.env.npm_config_user_agent || '')?.[1];
  if (!npm) {
    npm = execFileSync(DEPENDENCY_INSTALL_COMMAND, ['--version'], { encoding: 'utf8' }).trim();
  }
  return { platform: process.platform, arch: process.arch, node: process.versions.node, npm };
}

export function validateDependencyIntegrity(document) {
  exactKeys(document, [
    'schemaVersion', 'algorithm', 'generator', 'install', 'supportedEnvironments',
    'packageLockSha256', 'neutralTree', 'optionalLockSha256', 'binLockSha256', 'requiredTools',
  ], 'immutable dependency integrity');
  if (document.schemaVersion !== 2) fail('unsupported immutable dependency integrity schema version');
  if (document.algorithm !== DEPENDENCY_TREE_ALGORITHM) fail('immutable dependency integrity algorithm mismatch');
  if (JSON.stringify(document.generator) !== JSON.stringify(DEPENDENCY_INTEGRITY_GENERATOR)) {
    fail('immutable dependency integrity generator mismatch');
  }
  exactKeys(document.install, ['command', 'args'], 'immutable dependency install');
  if (document.install.command !== DEPENDENCY_INSTALL_COMMAND
    || JSON.stringify(document.install.args) !== JSON.stringify(DEPENDENCY_INSTALL_ARGS)) {
    fail('immutable dependency install command mismatch');
  }
  if (!Array.isArray(document.supportedEnvironments) || document.supportedEnvironments.length !== 1) {
    fail('immutable dependency environment rules mismatch');
  }
  validateSupportedEnvironmentRule(document.supportedEnvironments[0]);
  exactKeys(document.neutralTree, ['sha256', 'pathCount'], 'immutable dependency neutral tree');
  if (!SHA256_PATTERN.test(document.packageLockSha256)
    || !SHA256_PATTERN.test(document.neutralTree.sha256)
    || !Number.isSafeInteger(document.neutralTree.pathCount)
    || document.neutralTree.pathCount <= 0
    || !SHA256_PATTERN.test(document.optionalLockSha256)
    || !SHA256_PATTERN.test(document.binLockSha256)) {
    fail('immutable dependency integrity is invalid');
  }
  const toolPaths = Object.keys(document.requiredTools || {}).sort();
  if (JSON.stringify(toolPaths) !== JSON.stringify([...DEPENDENCY_REQUIRED_TOOL_PATHS].sort())) {
    fail('immutable dependency required tools mismatch');
  }
  for (const [relativePath, sha256] of Object.entries(document.requiredTools)) {
    if (!SHA256_PATTERN.test(sha256)) fail(`immutable dependency tool SHA-256 is invalid: ${relativePath}`);
  }
  return true;
}

function matchesEnvironment(rule, environment) {
  const [, , npmPatch] = parseVersion(environment.npm, 'npm version');
  void npmPatch;
  const npmMajor = Number(environment.npm.split('.')[0]);
  return nodeVersionSupported(environment.node)
    && rule.npmMajors.includes(npmMajor)
    && rule.targets.some(({ platform, arch }) => platform === environment.platform && arch === environment.arch);
}

export function dependencyEnvironmentProfile(environment = currentDependencyEnvironment()) {
  const rule = DEPENDENCY_SUPPORTED_ENVIRONMENTS.find((candidate) => matchesEnvironment(candidate, environment));
  if (!rule) {
    fail(`immutable dependency environment is not supported before installation: ${environment.platform}-${environment.arch} node/${environment.node} npm/${environment.npm}`);
  }
  return rule.id;
}

export function dependencyInstallPlan(document, environment = currentDependencyEnvironment()) {
  validateDependencyIntegrity(document);
  const profileId = dependencyEnvironmentProfile(environment);
  return {
    profileId,
    command: document.install.command,
    args: [...document.install.args],
    environment: { ...environment },
  };
}

function lockPackages(appRoot) {
  const packageLockPath = path.join(appRoot, 'package-lock.json');
  const packageLock = readJson(packageLockPath, 'immutable dependency package-lock');
  if (packageLock.lockfileVersion !== 3 || !packageLock.packages || typeof packageLock.packages !== 'object') {
    fail(`immutable dependency package-lock schema mismatch: ${packageLockPath}`);
  }
  return packageLock.packages;
}

function immutableLockRecord(packagePath, entry) {
  const result = { path: packagePath };
  for (const field of HIDDEN_LOCK_FIELDS) {
    if (Object.hasOwn(entry, field)) result[field] = entry[field];
  }
  return result;
}

function platformConstrainedOptionalRecords(packages) {
  return Object.entries(packages)
    .filter(([packagePath, entry]) => packagePath && entry.optional === true
      && (Array.isArray(entry.os) || Array.isArray(entry.cpu)))
    .map(([packagePath, entry]) => immutableLockRecord(packagePath, entry))
    .sort((left, right) => left.path.localeCompare(right.path));
}

function allowedByConstraint(values, actual) {
  if (!Array.isArray(values) || values.length === 0) return true;
  const positives = values.filter((value) => !value.startsWith('!'));
  const denied = values.some((value) => value === `!${actual}`);
  return !denied && (positives.length === 0 || positives.includes(actual));
}

function selectedForEnvironment(record, environment) {
  return allowedByConstraint(record.os, environment.platform)
    && allowedByConstraint(record.cpu, environment.arch);
}

function optionalDependencyClosure(appRoot, packages, environment) {
  const records = platformConstrainedOptionalRecords(packages);
  const selections = records.map((record) => {
    const selected = selectedForEnvironment(record, environment);
    const absolutePath = path.join(appRoot, record.path);
    const present = fs.existsSync(absolutePath);
    if (present !== selected) fail(`optional dependency presence mismatch: ${record.path}`);
    if (present) {
      const stat = fs.lstatSync(absolutePath);
      if (!stat.isDirectory() || stat.isSymbolicLink()) {
        fail(`optional dependency root type mismatch: ${record.path}`);
      }
    }
    return { path: record.path, selected };
  });
  return {
    excludedRoots: records.map(({ path: packagePath }) => packagePath),
    optionalLockSha256: canonicalHash(records),
    optionalSelectionSha256: canonicalHash(selections),
  };
}

function binName(packagePath, entry) {
  if (typeof entry.name === 'string' && entry.name) return entry.name.replace(/^@[^/]+\//u, '');
  const segments = packagePath.split('/');
  return segments.at(-1);
}

function binDirectory(packagePath) {
  const segments = packagePath.split('/');
  const packageNodeModulesIndex = segments.lastIndexOf('node_modules');
  if (packageNodeModulesIndex < 0) fail(`immutable dependency package path is invalid: ${packagePath}`);
  return [...segments.slice(0, packageNodeModulesIndex + 1), '.bin'].join('/');
}

function binRecords(packages) {
  const records = [];
  const root = packages[''] || {};
  const directPackagePaths = new Set(Object.keys({
    ...(root.dependencies || {}),
    ...(root.devDependencies || {}),
    ...(root.optionalDependencies || {}),
  }).map((name) => `node_modules/${name}`));
  for (const [packagePath, entry] of Object.entries(packages)) {
    if (!packagePath || !entry.bin) continue;
    const mappings = typeof entry.bin === 'string' ? { [binName(packagePath, entry)]: entry.bin } : entry.bin;
    if (!mappings || typeof mappings !== 'object' || Array.isArray(mappings)) {
      fail(`immutable dependency bin mapping is invalid: ${packagePath}`);
    }
    for (const [name, target] of Object.entries(mappings)) {
      if (!name || typeof target !== 'string' || !target) fail(`immutable dependency bin mapping is invalid: ${packagePath}`);
      records.push({
        packagePath,
        binDirectory: binDirectory(packagePath),
        name,
        target,
        direct: directPackagePaths.has(packagePath),
      });
    }
  }
  return records.sort((left, right) => JSON.stringify(left).localeCompare(JSON.stringify(right)));
}

function expectedWindowsShims(relativeTarget) {
  const slashTarget = relativeTarget.replaceAll('\\', '/');
  const cmdTarget = slashTarget.replaceAll('/', '\\');
  return {
    '': [
      '#!/bin/sh',
      'basedir=$(dirname "$(echo "$0" | sed -e \'s,\\\\,/,g\')")',
      '',
      'case `uname` in',
      '    *CYGWIN*|*MINGW*|*MSYS*)',
      '        if command -v cygpath > /dev/null 2>&1; then',
      '            basedir=`cygpath -w "$basedir"`',
      '        fi',
      '    ;;',
      'esac',
      '',
      'if [ -x "$basedir/node" ]; then',
      `  exec "$basedir/node"  "$basedir/${slashTarget}" "$@"`,
      'else ',
      `  exec node  "$basedir/${slashTarget}" "$@"`,
      'fi',
      '',
    ].join('\n'),
    '.cmd': [
      '@ECHO off',
      'GOTO start',
      ':find_dp0',
      'SET dp0=%~dp0',
      'EXIT /b',
      ':start',
      'SETLOCAL',
      'CALL :find_dp0',
      '',
      'IF EXIST "%dp0%\\node.exe" (',
      '  SET "_prog=%dp0%\\node.exe"',
      ') ELSE (',
      '  SET "_prog=node"',
      '  SET PATHEXT=%PATHEXT:;.JS;=;%',
      ')',
      '',
      `endLocal & goto #_undefined_# 2>NUL || title %COMSPEC% & "%_prog%"  "%dp0%\\${cmdTarget}" %*`,
      '',
    ].join('\r\n'),
    '.ps1': [
      '#!/usr/bin/env pwsh',
      '$basedir=Split-Path $MyInvocation.MyCommand.Definition -Parent',
      '',
      '$exe=""',
      'if ($PSVersionTable.PSVersion -lt "6.0" -or $IsWindows) {',
      '  # Fix case when both the Windows and Linux builds of Node',
      '  # are installed in the same directory',
      '  $exe=".exe"',
      '}',
      '$ret=0',
      'if (Test-Path "$basedir/node$exe") {',
      '  # Support pipeline input',
      '  if ($MyInvocation.ExpectingInput) {',
      `    $input | & "$basedir/node$exe"  "$basedir/${slashTarget}" $args`,
      '  } else {',
      `    & "$basedir/node$exe"  "$basedir/${slashTarget}" $args`,
      '  }',
      '  $ret=$LASTEXITCODE',
      '} else {',
      '  # Support pipeline input',
      '  if ($MyInvocation.ExpectingInput) {',
      `    $input | & "node$exe"  "$basedir/${slashTarget}" $args`,
      '  } else {',
      `    & "node$exe"  "$basedir/${slashTarget}" $args`,
      '  }',
      '  $ret=$LASTEXITCODE',
      '}',
      'exit $ret',
      '',
    ].join('\n'),
  };
}

function relativeBinTarget(record) {
  const targetPath = path.posix.join(record.packagePath, record.target);
  return path.posix.relative(record.binDirectory, targetPath);
}

export function assertBinLinkClosure(appRoot, environment = currentDependencyEnvironment()) {
  const packages = lockPackages(appRoot);
  const records = binRecords(packages);
  const expectedByDirectory = new Map();
  for (const record of records) {
    if (!fs.existsSync(path.join(appRoot, record.packagePath))) continue;
    const names = expectedByDirectory.get(record.binDirectory) || new Map();
    const existing = names.get(record.name);
    if (!existing || (!existing.direct && record.direct)) names.set(record.name, record);
    expectedByDirectory.set(record.binDirectory, names);
  }
  for (const [relativeDirectory, names] of expectedByDirectory) {
    const absoluteDirectory = path.join(appRoot, relativeDirectory);
    const expectedEntries = [...names.keys()].flatMap((name) => (
      environment.platform === 'win32' ? [name, `${name}.cmd`, `${name}.ps1`] : [name]
    )).sort();
    const actualEntries = fs.existsSync(absoluteDirectory) ? fs.readdirSync(absoluteDirectory).sort() : [];
    if (JSON.stringify(actualEntries) !== JSON.stringify(expectedEntries)) {
      fail(`bin link closure mismatch: ${relativeDirectory}`);
    }
    for (const record of names.values()) {
      const relativeTarget = relativeBinTarget(record);
      const absoluteBin = path.join(absoluteDirectory, record.name);
      if (environment.platform === 'win32') {
        const templates = expectedWindowsShims(relativeTarget);
        for (const [suffix, expected] of Object.entries(templates)) {
          const actualPath = `${absoluteBin}${suffix}`;
          if (!fs.existsSync(actualPath) || fs.readFileSync(actualPath, 'utf8') !== expected) {
            fail(`bin link closure mismatch: ${relativeDirectory}/${record.name}${suffix}`);
          }
        }
      }
      else if (!fs.existsSync(absoluteBin)
        || !fs.lstatSync(absoluteBin).isSymbolicLink()
        || fs.readlinkSync(absoluteBin).replaceAll('\\', '/') !== relativeTarget) {
        fail(`bin link closure mismatch: ${relativeDirectory}/${record.name}`);
      }
    }
  }
  return { binLockSha256: canonicalHash(records), binDirectories: [...expectedByDirectory.keys()].sort() };
}

export function assertHiddenPackageLockClosure(appRoot) {
  const packages = lockPackages(appRoot);
  const hiddenLockPath = path.join(appRoot, 'node_modules', '.package-lock.json');
  const hiddenLock = readJson(hiddenLockPath, 'immutable dependency hidden package-lock');
  if (hiddenLock.lockfileVersion !== 3 || !hiddenLock.packages || typeof hiddenLock.packages !== 'object') {
    fail(`immutable dependency hidden package-lock schema mismatch: ${hiddenLockPath}`);
  }
  const expected = Object.entries(packages)
    .filter(([packagePath]) => packagePath && fs.existsSync(path.join(appRoot, packagePath)))
    .map(([packagePath, entry]) => immutableLockRecord(packagePath, entry))
    .sort((left, right) => left.path.localeCompare(right.path));
  const actual = Object.entries(hiddenLock.packages)
    .filter(([packagePath]) => packagePath)
    .map(([packagePath, entry]) => immutableLockRecord(packagePath, entry))
    .sort((left, right) => left.path.localeCompare(right.path));
  if (JSON.stringify(actual) !== JSON.stringify(expected)) {
    fail(`immutable dependency hidden package-lock semantic closure mismatch: ${hiddenLockPath}`);
  }
  return { packageCount: actual.length, sha256: canonicalHash(actual) };
}

export function dependencyTreeIntegrity(appRoot, {
  environment = currentDependencyEnvironment(),
  expectedOptionalLockSha256,
} = {}) {
  const nodeModulesRoot = path.join(appRoot, 'node_modules');
  const rootStat = fs.lstatSync(nodeModulesRoot);
  if (!rootStat.isDirectory() || rootStat.isSymbolicLink()) {
    fail(`immutable dependency root must be a physical directory: ${nodeModulesRoot}`);
  }
  const packageLockPath = path.join(appRoot, 'package-lock.json');
  let optional = { excludedRoots: [], optionalLockSha256: canonicalHash([]), optionalSelectionSha256: canonicalHash([]) };
  let bins = { binLockSha256: canonicalHash([]), binDirectories: [] };
  if (fs.existsSync(packageLockPath)) {
    const packages = lockPackages(appRoot);
    optional = optionalDependencyClosure(appRoot, packages, environment);
    if (expectedOptionalLockSha256 && optional.optionalLockSha256 !== expectedOptionalLockSha256) {
      fail('optional dependency lock closure mismatch');
    }
    bins = assertBinLinkClosure(appRoot, environment);
  }
  const excludedRoots = new Set(optional.excludedRoots);
  const excludedBinDirectories = new Set(bins.binDirectories);
  const aggregate = createHash('sha256');
  let pathCount = 0;
  const add = (value) => {
    aggregate.update(value);
    aggregate.update('\0');
  };
  const collect = (absoluteRoot, relativeRoot = '') => {
    const records = [];
    for (const entry of fs.readdirSync(absoluteRoot).sort()) {
      const absolutePath = path.join(absoluteRoot, entry);
      const relativePath = relativeRoot ? `${relativeRoot}/${entry}` : entry;
      const packageRelativePath = `node_modules/${relativePath}`;
      if (relativePath === '.package-lock.json'
        || excludedRoots.has(packageRelativePath)
        || excludedBinDirectories.has(packageRelativePath)) continue;
      const stat = fs.lstatSync(absolutePath);
      if (stat.isDirectory()) {
        const descendants = collect(absolutePath, relativePath);
        if (descendants.length === 0) continue;
        records.push({ absolutePath, relativePath, kind: 'directory' }, ...descendants);
      }
      else if (stat.isFile()) {
        records.push({ absolutePath, relativePath, kind: 'file' });
      }
      else if (stat.isSymbolicLink()) {
        records.push({ absolutePath, relativePath, kind: 'symlink' });
      }
      else {
        fail(`unsupported immutable dependency entry: ${relativePath}`);
      }
    }
    return records;
  };
  for (const record of collect(nodeModulesRoot)) {
    pathCount += 1;
    add(record.relativePath);
    add(record.kind);
    if (record.kind === 'file') {
      aggregate.update(fs.readFileSync(record.absolutePath));
      aggregate.update('\0');
    }
    else if (record.kind === 'symlink') {
      add(fs.readlinkSync(record.absolutePath));
    }
  }
  return {
    nodeModulesRoot,
    pathCount,
    sha256: aggregate.digest('hex'),
    excludedOptionalRoots: optional.excludedRoots,
    optionalLockSha256: optional.optionalLockSha256,
    optionalSelectionSha256: optional.optionalSelectionSha256,
    binLockSha256: bins.binLockSha256,
  };
}

export function dependencyIntegrityForTree(appRoot, environment = currentDependencyEnvironment()) {
  const packageLockPath = path.join(appRoot, 'package-lock.json');
  const tree = dependencyTreeIntegrity(appRoot, { environment });
  assertHiddenPackageLockClosure(appRoot);
  const requiredTools = Object.fromEntries(DEPENDENCY_REQUIRED_TOOL_PATHS.map((relativePath) => {
    const absolutePath = path.join(tree.nodeModulesRoot, relativePath);
    if (!fs.existsSync(absolutePath)) fail(`immutable dependency tool is missing: ${relativePath}`);
    return [relativePath, hashValue(fs.readFileSync(absolutePath))];
  }));
  return {
    schemaVersion: 2,
    algorithm: DEPENDENCY_TREE_ALGORITHM,
    generator: DEPENDENCY_INTEGRITY_GENERATOR,
    install: { command: DEPENDENCY_INSTALL_COMMAND, args: [...DEPENDENCY_INSTALL_ARGS] },
    supportedEnvironments: DEPENDENCY_SUPPORTED_ENVIRONMENTS,
    packageLockSha256: hashValue(fs.readFileSync(packageLockPath)),
    neutralTree: { sha256: tree.sha256, pathCount: tree.pathCount },
    optionalLockSha256: tree.optionalLockSha256,
    binLockSha256: tree.binLockSha256,
    requiredTools,
  };
}
