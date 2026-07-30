import fs from 'node:fs';
import path from 'node:path';
import process from 'node:process';
import { fileURLToPath } from 'node:url';
import { parse as parseJavaScriptSource } from '@babel/parser';
import { canonicalViolationSignature, isCanonicalFullScan, sameCanonicalViolationBudget } from './lib/frontend-code-size-baseline.mjs';
import { assertFrontendCodeSizeBaselineSchema, BASELINE_PUBLIC_ERROR_CONTRACT_REJECTED_PROJECTION, formatBaselineTransactionErrorForStderr, hashBaselineBytes } from './lib/frontend-code-size-baseline-transaction.mjs';
import { runFrontendCodeSizeCheck, runFrontendCodeSizeUpdate } from './lib/frontend-code-size-guard-runner.mjs';

const appRoot = path.resolve(new URL('..', import.meta.url).pathname);
const baselinePath = path.join(appRoot, '.frontend_code_size_guard_baseline.json');
const baselineTestPath = path.join(appRoot, '.frontend_code_size_guard_baseline_test.json');

export const FRONTEND_CODE_SIZE_LIMITS = Object.freeze({
  maxFileLines: 800,
  maxTestFileLines: 800,
  maxFunctionLines: 150,
  maxNesting: 4,
  maxParams: 5,
  maxExports: 20,
  maxDirectoryFiles: 15,
  maxLineLength: 300,
});

const sourceExtensionPattern = /\.[cm]?[jt]sx?$/;
const defaultSourceRoots = Object.freeze(['src', 'scripts']);
const ignoreDirNames = new Set(['node_modules', 'dist', 'coverage']);

function readOptionValue(args, index, flag) {
  const value = args[index + 1];
  if (value === undefined || value === '' || value.startsWith('--')) throw new Error(`missing value for ${flag}`);
  return value;
}

function normalizeRel(filePath, root = appRoot) {
  return path.relative(root, filePath).split(path.sep).join('/');
}

function assertInsideAppRoot(targetPath) {
  const rel = path.relative(appRoot, targetPath);
  if (rel === '..' || rel.startsWith(`..${path.sep}`) || path.isAbsolute(rel)) {
    throw new Error(`scan directory is outside frontend app root: ${targetPath}`);
  }
}

function isFrontendTestFile(relFile) {
  return /\.(test|spec)(?:-helper)?\.[cm]?[jt]sx?$/.test(relFile) || relFile.includes('/__tests__/');
}

function assertValidScope(scope) {
  if (!['production', 'test', 'all'].includes(scope)) throw new Error(`invalid value for --scope: ${scope}`);
}

export function countEffectiveLines(lines) {
  let count = 0;
  let inBlock = false;
  for (const raw of lines) {
    const line = raw.trimStart();
    if (inBlock) {
      if (line.includes('*/')) inBlock = false;
      continue;
    }
    if (line === '' || line.startsWith('//') || line.startsWith('*')) continue;
    if (line.startsWith('/*')) {
      if (!line.includes('*/')) inBlock = true;
      continue;
    }
    count += 1;
  }
  return count;
}

function stripStrings(line) {
  return line
    .replace(/`(?:[^`\\]|\\.)*`/g, '""')
    .replace(/"(?:[^"\\]|\\.)*"/g, '""')
    .replace(/'(?:[^'\\]|\\.)*'/g, '""');
}

export function extractFunctions(lines) {
  const functions = [];
  let depth = 0;
  let activeName = '';
  let activeStart = 0;
  let activeStartDepth = 0;
  const functionRe = /^(?:export\s+(?:default\s+)?)?(?:async\s+)?function\s+([A-Za-z_$][\w$]*)?\s*\(/;
  const arrowAssignRe = /^(?:export\s+)?(?:const|let|var)\s+([A-Za-z_$][\w$]*)\s*(?::\s*[^=]+)?\s*=\s*(?:async\s+)?(?:\([^)]*\)|[A-Za-z_$][\w$]*)\s*=>/;
  const methodRe = /^\s*(?:async\s+)?([A-Za-z_$][\w$]*)\s*\([^)]*\)\s*(?::\s*[\w<>[\], |&.?]+)?\s*\{/;

  for (let index = 0; index < lines.length; index += 1) {
    const line = lines[index];
    const trimmed = line.trimStart();
    if (trimmed.startsWith('//') || trimmed.startsWith('*') || trimmed.startsWith('/*')) continue;
    if (!activeName) {
      const functionMatch = trimmed.match(functionRe);
      const arrowMatch = !functionMatch && trimmed.match(arrowAssignRe);
      const methodMatch = !functionMatch && !arrowMatch && depth > 0 && trimmed.match(methodRe);
      if (functionMatch) {
        activeName = functionMatch[1] || 'anonymous';
        activeStart = index;
        activeStartDepth = depth;
      } else if (arrowMatch && line.includes('{') && !line.trimEnd().endsWith('{}')) {
        activeName = arrowMatch[1];
        activeStart = index;
        activeStartDepth = depth;
      } else if (methodMatch) {
        activeName = methodMatch[1];
        activeStart = index;
        activeStartDepth = depth;
      }
    }
    for (const ch of stripStrings(line)) {
      if (ch === '{') depth += 1;
      if (ch === '}') depth -= 1;
    }
    if (activeName && depth <= activeStartDepth) {
      functions.push({ name: activeName, start: activeStart + 1, end: index + 1, lines: index - activeStart + 1 });
      activeName = '';
      if (depth < 0) depth = 0;
    }
  }
  return functions;
}

export function measureMaxNesting(lines) {
  let maxDepth = 0;
  let depth = 0;
  for (const line of lines) {
    const trimmed = line.trimStart();
    if (trimmed.startsWith('//') || trimmed.startsWith('*')) continue;
    for (const ch of stripStrings(line)) {
      if (ch === '{') {
        depth += 1;
        if (depth > maxDepth) maxDepth = depth;
      }
      if (ch === '}') depth -= 1;
    }
  }
  return maxDepth;
}

function countFunctionParams(lines, funcStartLine) {
  const match = (lines[funcStartLine - 1] || '').match(/\(([^)]*)\)/);
  if (!match) return 0;
  const params = match[1].trim();
  return params === '' ? 0 : params.split(',').filter((part) => part.trim() !== '').length;
}

function countExports(source) {
  return source.match(/^export\s+/gm)?.length || 0;
}

function makeViolation(file, line, rule, message) {
  return { file, line, rule, message };
}

function rulesForSource(relFile, source, limits = FRONTEND_CODE_SIZE_LIMITS) {
  const lines = source.split('\n');
  const violations = [];
  const testFile = isFrontendTestFile(relFile);
  const effectiveLines = countEffectiveLines(lines);
  const maxFileLines = testFile ? limits.maxTestFileLines : limits.maxFileLines;
  if (effectiveLines > maxFileLines) {
    violations.push(makeViolation(relFile, 1, 'file-length', `文件有效代码 ${effectiveLines} 行，超过上限 ${maxFileLines} 行`));
  }
  const functions = extractFunctions(lines);
  for (const func of functions) {
    if (func.lines > limits.maxFunctionLines) {
      violations.push(makeViolation(relFile, func.start, 'func-length', `函数 ${func.name} 共 ${func.lines} 行，超过上限 ${limits.maxFunctionLines} 行`));
    }
    const params = countFunctionParams(lines, func.start);
    if (params > limits.maxParams) {
      violations.push(makeViolation(relFile, func.start, 'params', `函数 ${func.name} 有 ${params} 个参数，超过上限 ${limits.maxParams} 个`));
    }
  }
  const maxNesting = measureMaxNesting(lines);
  if (!testFile && maxNesting > limits.maxNesting) {
    violations.push(makeViolation(relFile, 1, 'nesting', `最大嵌套 ${maxNesting} 层，超过上限 ${limits.maxNesting} 层`));
  }
  const exportCount = countExports(source);
  if (exportCount > limits.maxExports) {
    violations.push(makeViolation(relFile, 1, 'exports', `${exportCount} 个 export，超过上限 ${limits.maxExports} 个`));
  }
  lines.forEach((line, index) => {
    const trimmed = line.trimStart();
    if (trimmed.startsWith('//') || trimmed.startsWith('*')) return;
    if (!testFile && /console\.log\s*\(/.test(trimmed)) violations.push(makeViolation(relFile, index + 1, 'console-log', '生产代码禁止 console.log()，请用 logger 或删除'));
    if (/(?::\s*any\b|<any>|as\s+any\b|\bany\[\]|\bany\s*[|&]|[|&]\s*any\b)/.test(trimmed)) violations.push(makeViolation(relFile, index + 1, 'any', '禁止使用 any 类型，请使用具体类型或 unknown'));
    if (!isFrontendTestFile(relFile) && line.length > limits.maxLineLength) {
      violations.push(makeViolation(relFile, index + 1, 'line-length', `单行 ${line.length} 字符，超过上限 ${limits.maxLineLength}，禁止用超长单行绕过复杂度守卫`));
    }
  });
  for (let index = 0; index < lines.length - 1; index += 1) {
    const line = lines[index].trimEnd();
    if (!testFile && (/(?:function\s+\w+|=>\s*)\{\s*\}\s*[;,]?\s*$/.test(line) || (line.endsWith('{') && (lines[index + 1] || '').trim() === '}'))) {
      violations.push(makeViolation(relFile, index + 1, 'empty-func', '空函数体，可能是未实现'));
    }
  }
  lines.forEach((line, index) => {
    const upper = line.toUpperCase();
    if (upper.includes('TODO') || upper.includes('FIXME') || upper.includes('HACK')) {
      violations.push(makeViolation(relFile, index + 1, 'todo', line.trim()));
    }
  });
  return violations;
}

export function checkFrontendCodeSizeSource(relFile, source, limits = FRONTEND_CODE_SIZE_LIMITS) {
  return rulesForSource(relFile, source, limits);
}

export function measureFrontendCodeSizeSource(relFile, source) {
  const lines = source.split('\n');
  const functions = extractFunctions(lines);
  const violations = rulesForSource(relFile, source);
  return {
    lines: countEffectiveLines(lines),
    maxFuncLen: functions.reduce((max, func) => Math.max(max, func.lines), 0),
    maxNesting: measureMaxNesting(lines),
    maxParams: functions.reduce((max, func) => Math.max(max, countFunctionParams(lines, func.start)), 0),
    exportCount: countExports(source),
    consoleLogs: violations.filter((entry) => entry.rule === 'console-log').length,
    anyCount: violations.filter((entry) => entry.rule === 'any').length,
    emptyFuncs: violations.filter((entry) => entry.rule === 'empty-func').length,
    todoCount: violations.filter((entry) => entry.rule === 'todo').length,
    longLineCount: violations.filter((entry) => entry.rule === 'line-length').length,
  };
}

export function measureFrontendCodeSizeSourceAstShadow(relFile, source) {
  const lines = source.split('\n');
  const ast = parseFrontendCodeSizeAst(source);
  const functions = extractFunctionsFromAst(ast);
  const violations = rulesForSource(relFile, source);
  return {
    lines: countEffectiveLines(lines),
    maxFuncLen: functions.reduce((max, func) => Math.max(max, func.lines), 0),
    maxNesting: measureMaxNestingFromAst(ast),
    maxParams: functions.reduce((max, func) => Math.max(max, func.params), 0),
    exportCount: countExportsFromAst(ast),
    consoleLogs: violations.filter((entry) => entry.rule === 'console-log').length,
    anyCount: violations.filter((entry) => entry.rule === 'any').length,
    emptyFuncs: violations.filter((entry) => entry.rule === 'empty-func').length,
    todoCount: violations.filter((entry) => entry.rule === 'todo').length,
    longLineCount: violations.filter((entry) => entry.rule === 'line-length').length,
  };
}

function parseFrontendCodeSizeAst(source) {
  return parseJavaScriptSource(source, {
    sourceType: 'module',
    plugins: ['jsx', 'typescript'],
  });
}

function extractFunctionsFromAst(ast) {
  const functions = [];
  traverseAst(ast, (node, parent) => {
    const name = astFunctionName(node, parent);
    if (!name || !node.loc) return;
    functions.push({
      name,
      start: node.loc.start.line,
      end: node.loc.end.line,
      lines: node.loc.end.line - node.loc.start.line + 1,
      params: Array.isArray(node.params) ? node.params.length : 0,
    });
  });
  return functions.sort((left, right) => left.start - right.start || left.name.localeCompare(right.name));
}

function astFunctionName(node, parent) {
  if (node.type === 'FunctionDeclaration') return node.id?.name || 'anonymous';
  if (node.type === 'ObjectMethod' || node.type === 'ClassMethod' || node.type === 'ClassPrivateMethod') return astPropertyKeyName(node.key);
  if (node.type !== 'ArrowFunctionExpression' && node.type !== 'FunctionExpression') return '';
  if (parent?.type === 'VariableDeclarator' && parent.id.type === 'Identifier') return parent.id.name;
  return '';
}

function measureMaxNestingFromAst(ast) {
  let maxDepth = 0;
  walkAstNesting(ast, 0, (depth) => {
    if (depth > maxDepth) maxDepth = depth;
  });
  return maxDepth;
}

function walkAstNesting(node, depth, updateMax) {
  if (!node || typeof node.type !== 'string') return;
  const nextDepth = isNestingAstNode(node) ? depth + 1 : depth;
  updateMax(nextDepth);
  for (const child of astChildren(node)) {
    walkAstNesting(child, nextDepth, updateMax);
  }
}

function isNestingAstNode(node) {
  return node.type === 'BlockStatement' || node.type === 'ObjectExpression' || node.type === 'ClassBody' || node.type === 'SwitchStatement';
}

function countExportsFromAst(ast) {
  return ast.program.body.filter((node) => (
    node.type === 'ExportNamedDeclaration'
    || node.type === 'ExportDefaultDeclaration'
    || node.type === 'ExportAllDeclaration'
  )).length;
}

function traverseAst(node, visit, parent = null) {
  if (!node || typeof node.type !== 'string') return;
  visit(node, parent);
  for (const child of astChildren(node)) {
    traverseAst(child, visit, node);
  }
}

function astChildren(node) {
  const children = [];
  for (const [key, value] of Object.entries(node)) {
    if (key === 'loc' || key === 'start' || key === 'end') continue;
    if (Array.isArray(value)) {
      children.push(...value.filter((entry) => entry && typeof entry.type === 'string'));
    } else if (value && typeof value.type === 'string') {
      children.push(value);
    }
  }
  return children;
}

function astPropertyKeyName(key) {
  if (key?.type === 'Identifier') return key.name;
  if (key?.type === 'StringLiteral' || key?.type === 'NumericLiteral') return String(key.value);
  if (key?.type === 'PrivateName') return key.id.name;
  return 'anonymous';
}

function walkSourceFiles(dir, files = []) {
  if (!fs.existsSync(dir)) throw new Error(`frontend code size guard root does not exist: ${dir}`);
  if (!fs.statSync(dir).isDirectory()) throw new Error(`frontend code size guard root is not a directory: ${dir}`);
  for (const entry of fs.readdirSync(dir, { withFileTypes: true })) {
    const fullPath = path.join(dir, entry.name);
    if (entry.isDirectory()) {
      if (!ignoreDirNames.has(entry.name) && !entry.name.startsWith('.')) walkSourceFiles(fullPath, files);
    } else if (entry.isFile() && sourceExtensionPattern.test(entry.name) && !entry.name.endsWith('.d.ts')) {
      files.push(fullPath);
    }
  }
  return files;
}

function collectFiles(scanDirs) {
  return scanDirs.flatMap((dir) => walkSourceFiles(dir)).map((abs) => ({ abs, rel: normalizeRel(abs) }));
}

function assertAllowedSourceFile(relFile, absFile) {
  if (path.isAbsolute(relFile)) throw new Error(`--file must be relative to frontend app root: ${relFile}`);
  assertInsideAppRoot(absFile);
  if (!fs.existsSync(absFile)) throw new Error(`--file does not exist: ${relFile}`);
  if (!fs.statSync(absFile).isFile()) throw new Error(`--file is not a file: ${relFile}`);
  if (!sourceExtensionPattern.test(relFile) || relFile.endsWith('.d.ts')) throw new Error(`--file is not a frontend source file: ${relFile}`);
  for (const part of relFile.split('/')) {
    if (ignoreDirNames.has(part) || part.startsWith('.')) throw new Error(`--file is under an ignored directory: ${relFile}`);
  }
}

function collectExplicitFiles(relFiles) {
  return relFiles.map((relFile) => {
    const abs = path.resolve(appRoot, relFile);
    const normalizedRel = normalizeRel(abs);
    assertAllowedSourceFile(relFile, abs);
    return { abs, rel: normalizedRel };
  });
}

function filterFilesByScope(files, scope) {
  assertValidScope(scope);
  if (scope === 'all') return files;
  if (scope === 'production') return files.filter((file) => !isFrontendTestFile(file.rel));
  return files.filter((file) => isFrontendTestFile(file.rel));
}

function loadBaselineSnapshot(filePath) {
  if (!fs.existsSync(filePath)) throw new Error(`tracked baseline is missing: ${filePath}; restore it before running check or update`);
  const bytes = fs.readFileSync(filePath);
  const baseline = JSON.parse(bytes.toString('utf8'));
  assertFrontendCodeSizeBaselineSchema(baseline, path.basename(filePath));
  return { baseline, bytes, hash: hashBaselineBytes(bytes) };
}

function violationSignature(entry) {
  return `${entry.rule}\u0000${entry.message}`;
}

function signatureBudget(signatures) {
  const budget = new Map();
  for (const signature of Array.isArray(signatures) ? signatures : []) {
    const canonicalSignature = canonicalViolationSignature(signature);
    budget.set(canonicalSignature, (budget.get(canonicalSignature) || 0) + 1);
  }
  return budget;
}

function unfrozenViolations(violations, frozen) {
  const budget = signatureBudget(frozen?.frozenViolations);
  if (budget.size === 0) return violations;
  return violations.filter((entry) => {
    const signature = canonicalViolationSignature(violationSignature(entry));
    const remaining = budget.get(signature) || 0;
    if (remaining <= 0) return true;
    budget.set(signature, remaining - 1);
    return false;
  });
}

function ratchetViolations(relFile, current, frozen) {
  const checks = [
    ['freeze/file-length', 'lines', '行'],
    ['freeze/func-length', 'maxFuncLen', '行'],
    ['freeze/nesting', 'maxNesting', '层'],
    ['freeze/params', 'maxParams', '个'],
    ['freeze/exports', 'exportCount', '个'],
    ['freeze/console-log', 'consoleLogs', '处'],
    ['freeze/any', 'anyCount', '处'],
    ['freeze/empty-func', 'emptyFuncs', '个'],
    ['freeze/todo', 'todoCount', '处'],
    ['freeze/line-length', 'longLineCount', '处'],
  ];
  return checks.flatMap(([rule, key, unit]) => {
    if (frozen?.[key] === undefined || current?.[key] === undefined || current[key] <= frozen[key]) return [];
    return [makeViolation(relFile, 1, rule, `从 ${frozen[key]}${unit} 增长到 ${current[key]}${unit}（已冻结，只允许减少）`)];
  });
}

function baselineEntryForCurrentViolations(relFile, source, currentViolations, frozen) {
  const metrics = measureFrontendCodeSizeSource(relFile, source);
  metrics.frozenViolations = frozen && sameCanonicalViolationBudget(currentViolations.map(violationSignature), frozen.frozenViolations)
    ? [...frozen.frozenViolations]
    : currentViolations.map(violationSignature).sort();
  return metrics;
}

function baselineFilesEqual(left = {}, right = {}) {
  return JSON.stringify(left) === JSON.stringify(right);
}

function directorySizeViolations(files, baseline) {
  const counts = new Map();
  for (const { rel } of files.filter((file) => !isFrontendTestFile(file.rel))) {
    const dir = path.dirname(rel);
    counts.set(dir, (counts.get(dir) || 0) + 1);
  }
  const violations = [];
  for (const [dir, count] of counts.entries()) {
    const key = `__dir__:${dir}`;
    const frozen = baseline.files?.[key];
    if (frozen && count > frozen.lines) violations.push(makeViolation(dir, 0, 'freeze/dir-size', `从 ${frozen.lines} 个文件增长到 ${count} 个（已冻结，只允许减少）`));
    else if (!frozen && count > FRONTEND_CODE_SIZE_LIMITS.maxDirectoryFiles) violations.push(makeViolation(dir, 0, 'dir-size', `目录含 ${count} 个生产文件，超过上限 ${FRONTEND_CODE_SIZE_LIMITS.maxDirectoryFiles} 个，应拆分`));
  }
  return violations;
}

function refreshDirectoryBaseline(files, baseline) {
  const nextFiles = { ...(baseline.files || {}) };
  const counts = new Map();
  for (const { rel } of files.filter((file) => !isFrontendTestFile(file.rel))) {
    const dir = path.dirname(rel);
    counts.set(dir, (counts.get(dir) || 0) + 1);
  }
  for (const key of Object.keys(nextFiles)) {
    if (key.startsWith('__dir__:') && !counts.has(key.slice('__dir__:'.length))) delete nextFiles[key];
  }
  for (const [dir, count] of counts.entries()) {
    const key = `__dir__:${dir}`;
    if (count > FRONTEND_CODE_SIZE_LIMITS.maxDirectoryFiles) nextFiles[key] = { lines: count };
    else delete nextFiles[key];
  }
  return { ...baseline, files: nextFiles };
}

function buildRecords(files) {
  return files.map((file) => ({ rel: file.rel, source: fs.readFileSync(file.abs, 'utf8') }));
}

function planFrontendCodeSizeGuard({ records, productionBaseline, testBaseline, canonical = false, strict = false, scope = 'all' }) {
  const prodBaseline = strict ? { _meta: productionBaseline._meta, files: {} } : productionBaseline;
  const testBaselineForPlan = strict ? { _meta: testBaseline._meta, files: {} } : testBaseline;
  const nextProdBaseline = { ...prodBaseline, files: { ...(prodBaseline.files || {}) } };
  const nextTestBaseline = { ...testBaselineForPlan, files: { ...(testBaselineForPlan.files || {}) } };
  const scannedFiles = new Set(records.map((record) => record.rel));
  const violations = [];
  let frozenFiles = 0;
  for (const record of records) {
    const currentViolations = rulesForSource(record.rel, record.source);
    const baseline = isFrontendTestFile(record.rel) ? testBaselineForPlan : prodBaseline;
    const nextBaseline = isFrontendTestFile(record.rel) ? nextTestBaseline : nextProdBaseline;
    const frozen = baseline.files?.[record.rel];
    violations.push(...(frozen ? unfrozenViolations(currentViolations, frozen) : currentViolations));
    if (frozen) {
      const ratchets = ratchetViolations(record.rel, measureFrontendCodeSizeSource(record.rel, record.source), frozen);
      violations.push(...ratchets);
      if (currentViolations.length > 0 || ratchets.length > 0) {
        frozenFiles += 1;
        if (canonical && ratchets.length === 0) {
          nextBaseline.files[record.rel] = baselineEntryForCurrentViolations(record.rel, record.source, currentViolations, frozen);
        }
      } else {
        if (canonical) delete nextBaseline.files[record.rel];
      }
    }
  }
  const fileShapes = records.map((record) => ({ rel: record.rel }));
  violations.push(...directorySizeViolations(fileShapes, strict ? { files: {} } : prodBaseline));
  let productionCandidate = nextProdBaseline;
  if (!strict && canonical) {
    if (scope !== 'test') {
      for (const key of Object.keys(nextProdBaseline.files)) {
        if (!key.startsWith('__dir__:') && (!scannedFiles.has(key) || isFrontendTestFile(key))) delete nextProdBaseline.files[key];
      }
      productionCandidate = refreshDirectoryBaseline(fileShapes, nextProdBaseline);
    }
    if (scope !== 'production') {
      for (const key of Object.keys(nextTestBaseline.files)) {
        if (!scannedFiles.has(key) || !isFrontendTestFile(key)) delete nextTestBaseline.files[key];
      }
    }
  }
  const driftScopes = [];
  if (!strict && canonical && scope !== 'test' && !baselineFilesEqual(prodBaseline.files, productionCandidate.files)) {
    driftScopes.push('production');
  }
  if (!strict && canonical && scope !== 'production' && !baselineFilesEqual(testBaselineForPlan.files, nextTestBaseline.files)) {
    driftScopes.push('test');
  }
  return { violations, frozenFiles, productionCandidate, testCandidate: nextTestBaseline, driftScopes };
}

export function parseFrontendCodeSizeGuardArgs(args) {
  const options = { mode: 'check', dirs: [], files: [], scope: 'all', printScanDirs: false };
  let explicitMode = '';
  for (let index = 0; index < args.length; index += 1) {
    const arg = args[index];
    if (['--check', '--strict', '--update', '--freeze'].includes(arg)) {
      if (explicitMode) throw new Error(`choose exactly one mode; received ${explicitMode} and ${arg}`);
      explicitMode = arg;
      options.mode = arg.slice(2);
    }
    else if (arg === '--print-scan-dirs') options.printScanDirs = true;
    else if (arg === '--scope') {
      options.scope = readOptionValue(args, index, arg);
      assertValidScope(options.scope);
      index += 1;
    } else if (arg === '--file') {
      options.files.push(readOptionValue(args, index, arg));
      index += 1;
    }
    else if (arg === '--dir') {
      options.dirs.push(path.resolve(appRoot, readOptionValue(args, index, arg)));
      index += 1;
    } else {
      throw new Error(`unknown argument: ${arg}`);
    }
  }
  if (options.dirs.length === 0) options.dirs = defaultSourceRoots.map((root) => path.join(appRoot, root));
  return options;
}

function main() {
  const options = parseFrontendCodeSizeGuardArgs(process.argv.slice(2));
  if (options.mode === 'freeze') {
    throw new Error('--freeze is retired and never writes; use the controlled canonical --update --scope production|test migration command');
  }
  for (const dir of options.dirs) assertInsideAppRoot(dir);
  if (options.printScanDirs) {
    for (const dir of options.dirs) console.log(normalizeRel(dir));
    return;
  }
  const files = filterFilesByScope(
    options.files.length > 0 ? collectExplicitFiles(options.files) : collectFiles(options.dirs),
    options.scope,
  );
  if (files.length === 0) throw new Error('no frontend source files found');
  const snapshots = {
    production: loadBaselineSnapshot(baselinePath),
    test: loadBaselineSnapshot(baselineTestPath),
  };
  options.canonical = isCanonicalFullScan(
    options.dirs,
    options.files,
    defaultSourceRoots.map((root) => path.join(appRoot, root)),
  );
  const records = buildRecords(files);
  if (options.mode === 'update') {
    runFrontendCodeSizeUpdate({
      records,
      snapshots,
      options,
      planGuard: planFrontendCodeSizeGuard,
      productionPath: baselinePath,
      testPath: baselineTestPath,
    });
  } else if (!runFrontendCodeSizeCheck({
    records,
    snapshots,
    options,
    planGuard: planFrontendCodeSizeGuard,
  })) {
    process.exitCode = 1;
  }
}

if (process.argv[1] === fileURLToPath(import.meta.url)) {
  try {
    main();
  } catch (error) {
    let publicProjection; try { publicProjection = formatBaselineTransactionErrorForStderr(error); } catch { publicProjection = BASELINE_PUBLIC_ERROR_CONTRACT_REJECTED_PROJECTION; }
    console.error(`frontend code size guard: ${publicProjection ?? (error instanceof Error ? error.message : String(error))}`); process.exit(2);
  }
}
