import { readFileSync, statSync } from 'node:fs';
import path from 'node:path';
import process from 'node:process';
import { spawnSync } from 'node:child_process';

const appRoot = path.resolve(import.meta.dirname, '..');
// 直接执行检测是公共门禁的一部分；必须先将模块 URL 转成本地路径，避免 Windows 盘符失真后静默跳过。
const modulePath = import.meta.filename;
const configPath = path.join(appRoot, 'tsconfig.contracts.json');
const registryPath = path.join(appRoot, 'scripts', 'critical-typecheck-files.json');
const tscPath = path.join(appRoot, 'node_modules', 'typescript', 'bin', 'tsc');
const REQUIRED_SURFACES = Object.freeze([
  'actionFeedback',
  'diagnostics',
  'promptHistory',
  'providerPreference',
  'rpcAdapter',
  'storeBridge',
  'terminalPublicError',
  'uiAction',
]);
const ALLOWED_CONFIG_EXCLUDES = Object.freeze(['coverage', 'dist', 'node_modules']);
const REQUIRED_TEST_FILES = Object.freeze(['scripts/contracts-typecheck-guard.test.mjs']);
const FORBIDDEN_DIRECTIVE_PATTERN = /@ts-(?:nocheck|ignore|expect-error)\b/;
const FORBIDDEN_EXPLICIT_ANY_PATTERN =
  /(?:@(?:type|param|returns?|typedef|property)\s+\{[^}\n]*\bany\b|\b[A-Za-z_$][\w$]*(?:\.[A-Za-z_$][\w$]*)*\s*<[^>\n]*\bany\b|(?:\b[A-Za-z_$][\w$]*\??|\))\s*:\s*[\w$.[\]{}|&<>,? \t]*\bany\b|\bas\s+any\b)/;

function sortedUnique(values, label) {
  if (!Array.isArray(values) || values.length === 0) {
    throw new Error(`${label} must be a non-empty array`);
  }
  if (values.some((value) => typeof value !== 'string' || !value.trim())) {
    throw new Error(`${label} must contain non-empty strings`);
  }
  const normalized = values.map((value) => value.trim().replaceAll('\\', '/'));
  const unique = [...new Set(normalized)].sort();
  if (unique.length !== normalized.length) throw new Error(`${label} contains duplicate paths`);
  return unique;
}

function exactDiff(expected, actual) {
  const expectedSet = new Set(expected);
  const actualSet = new Set(actual);
  return {
    missing: expected.filter((file) => !actualSet.has(file)),
    stale: actual.filter((file) => !expectedSet.has(file)),
  };
}

function assertExactSet(label, expected, actual) {
  const diff = exactDiff(expected, actual);
  if (diff.missing.length || diff.stale.length) {
    throw new Error(`${label} exact diff failed: missing=[${diff.missing.join(', ')}] stale=[${diff.stale.join(', ')}]`);
  }
}

export function validateCriticalTypecheckRegistry(registry) {
  if (!registry || typeof registry !== 'object' || Array.isArray(registry)) {
    throw new Error('critical typecheck registry must be an object');
  }
  const keys = Object.keys(registry).sort();
  assertExactSet(
    'critical typecheck registry keys',
    ['entrypoints', 'productionFiles', 'schemaVersion', 'surfaces', 'testFiles'],
    keys,
  );
  if (registry.schemaVersion !== 1) throw new Error('critical typecheck registry schemaVersion must be 1');
  if (!registry.surfaces || typeof registry.surfaces !== 'object' || Array.isArray(registry.surfaces)) {
    throw new Error('critical typecheck registry surfaces must be an object');
  }
  const surfaceNames = Object.keys(registry.surfaces).sort();
  assertExactSet('critical typecheck surfaces', REQUIRED_SURFACES, surfaceNames);
  const surfaceFiles = surfaceNames.flatMap((name) => sortedUnique(registry.surfaces[name], `surface ${name}`));
  const entrypoints = sortedUnique(registry.entrypoints, 'critical typecheck entrypoints');
  const productionFiles = sortedUnique(registry.productionFiles, 'critical typecheck productionFiles');
  const testFiles = sortedUnique(registry.testFiles, 'critical typecheck testFiles');
  assertExactSet('critical typecheck testFiles', REQUIRED_TEST_FILES, testFiles);
  assertExactSet('critical typecheck surface entrypoints', entrypoints, [...new Set(surfaceFiles)].sort());
  for (const entrypoint of entrypoints) {
    if (!productionFiles.includes(entrypoint)) {
      throw new Error(`critical typecheck entrypoint is absent from productionFiles: ${entrypoint}`);
    }
  }
  return { entrypoints, productionFiles, testFiles };
}

export function validateCriticalTypecheckConfig(config, entrypoints) {
  if (!config || typeof config !== 'object' || Array.isArray(config)) {
    throw new Error('tsconfig.contracts.json must be an object');
  }
  const options = config.compilerOptions;
  if (!options || typeof options !== 'object' || Array.isArray(options)) {
    throw new Error('tsconfig.contracts.json compilerOptions must be an object');
  }
  for (const option of ['checkJs', 'strict', 'noImplicitAny', 'useUnknownInCatchVariables']) {
    if (options[option] !== true) throw new Error(`tsconfig.contracts.json ${option} must be true`);
  }
  if (options.skipLibCheck !== false) {
    throw new Error('tsconfig.contracts.json skipLibCheck must be false');
  }
  if (options.baseUrl !== undefined || options.paths !== undefined) {
    throw new Error('tsconfig.contracts.json must not remap critical files through baseUrl or paths');
  }
  const includes = sortedUnique(config.include, 'tsconfig.contracts.json include');
  const excludes = sortedUnique(config.exclude, 'tsconfig.contracts.json exclude');
  assertExactSet('tsconfig.contracts.json include', entrypoints, includes);
  assertExactSet('tsconfig.contracts.json exclude', ALLOWED_CONFIG_EXCLUDES, excludes);
}

export function productionFilesFromListOutput(output, root = appRoot) {
  const srcRoot = `${path.resolve(root, 'src')}${path.sep}`;
  const files = String(output)
    .split(/\r?\n/)
    .map((line) => line.trim())
    .filter(Boolean)
    .map((file) => path.resolve(root, file))
    .filter((file) => file.startsWith(srcRoot) && /\.(?:js|jsx)$/.test(file))
    .map((file) => path.relative(root, file).split(path.sep).join('/'));
  return [...new Set(files)].sort();
}

export function assertExactProductionFiles(expected, actual) {
  if (actual.length === 0) throw new Error('critical typecheck listFiles returned zero production files');
  assertExactSet('critical typecheck production files', expected, actual);
}

export function assertCompilerSucceeded(result, phase) {
  if (result.error) throw new Error(`critical typecheck ${phase} failed to start: ${result.error.message}`);
  if (result.status !== 0) {
    const output = `${result.stdout || ''}${result.stderr || ''}`.trim();
    throw new Error(`critical typecheck ${phase} exited ${String(result.status)}${output ? `\n${output}` : ''}`);
  }
}

function runCompiler(args) {
  return spawnSync(process.execPath, [tscPath, ...args], {
    cwd: appRoot,
    encoding: 'utf8',
    env: process.env,
  });
}

function readJSON(file) {
  return JSON.parse(readFileSync(file, 'utf8'));
}

export function assertRegisteredFiles(files, root = appRoot) {
  const srcRoot = `${path.resolve(root, 'src')}${path.sep}`;
  for (const file of files) {
    const absolute = path.resolve(root, file);
    if (!absolute.startsWith(srcRoot)) {
      throw new Error(`critical typecheck production file is outside src: ${file}`);
    }
    let info;
    try {
      info = statSync(absolute);
    } catch {
      throw new Error(`critical typecheck production path is missing: ${file}`);
    }
    if (!info.isFile()) throw new Error(`critical typecheck production path is not a file: ${file}`);
    const source = readFileSync(absolute, 'utf8');
    if (FORBIDDEN_DIRECTIVE_PATTERN.test(source)) {
      throw new Error(`critical typecheck production file contains a forbidden TypeScript directive: ${file}`);
    }
    if (FORBIDDEN_EXPLICIT_ANY_PATTERN.test(source)) {
      throw new Error(`critical typecheck production file contains an explicit any type: ${file}`);
    }
  }
}

export function assertRegisteredTests(files, root = appRoot) {
  for (const file of files) {
    const absolute = path.resolve(root, file);
    const scriptsRoot = `${path.resolve(root, 'scripts')}${path.sep}`;
    if (!absolute.startsWith(scriptsRoot)) {
      throw new Error(`critical typecheck test file is outside scripts: ${file}`);
    }
    let info;
    try {
      info = statSync(absolute);
    } catch {
      throw new Error(`critical typecheck test path is missing: ${file}`);
    }
    if (!info.isFile()) throw new Error(`critical typecheck test path is not a file: ${file}`);
  }
}

export function verifyCriticalTypecheck() {
  const registry = validateCriticalTypecheckRegistry(readJSON(registryPath));
  validateCriticalTypecheckConfig(readJSON(configPath), registry.entrypoints);
  assertRegisteredFiles(registry.productionFiles);
  assertRegisteredTests(registry.testFiles);

  const check = runCompiler([
    '-p',
    'tsconfig.contracts.json',
    '--noEmit',
    '--listFiles',
    '--pretty',
    'false',
  ]);
  assertCompilerSucceeded(check, 'compiler');
  const actual = productionFilesFromListOutput(check.stdout);
  assertExactProductionFiles(registry.productionFiles, actual);
  return actual;
}

if (path.resolve(process.argv[1] || '') === modulePath) {
  try {
    const files = verifyCriticalTypecheck();
    console.log(`critical typecheck passed: ${files.length} production files`);
    for (const file of files) console.log(`- ${file}`);
  } catch (error) {
    console.error(error instanceof Error ? error.message : String(error));
    process.exit(1);
  }
}
