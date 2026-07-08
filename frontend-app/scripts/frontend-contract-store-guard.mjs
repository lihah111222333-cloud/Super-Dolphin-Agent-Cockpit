import fs from 'node:fs';
import path from 'node:path';
import process from 'node:process';
import { fileURLToPath } from 'node:url';
import ts from 'typescript';

const appRoot = path.resolve(new URL('..', import.meta.url).pathname);
const sourceExtensionPattern = /\.[cm]?[jt]sx?$/;
const guardedSourceRoots = Object.freeze(['src']);

const ratchetLimits = Object.freeze({
  'compat-field-fallback': 134,
  'date-parse-order': 92,
  'default-value-fallback': 540,
  'json-parse': 13,
  'mutable-browser-storage': 8,
  'sort-without-comparator': 1,
  'store-hook-import': 0,
});

const allowedStoreHookImportFiles = new Set([
  'src/App.jsx',
  'src/pages/settings/SettingsPage.jsx',
]);

function walkSourceFiles(dir) {
  if (!fs.existsSync(dir)) return [];
  const entries = fs.readdirSync(dir, { withFileTypes: true });
  const files = [];
  for (const entry of entries) {
    if (entry.name === 'node_modules' || entry.name === 'dist' || entry.name === 'coverage') continue;
    const fullPath = path.join(dir, entry.name);
    if (entry.isDirectory()) {
      files.push(...walkSourceFiles(fullPath));
      continue;
    }
    if (sourceExtensionPattern.test(entry.name) && !/\.test\.[cm]?[jt]sx?$/.test(entry.name)) {
      files.push(fullPath);
    }
  }
  return files;
}

function scriptKindForPath(relFile) {
  if (relFile.endsWith('.tsx')) return ts.ScriptKind.TSX;
  if (relFile.endsWith('.jsx')) return ts.ScriptKind.JSX;
  if (relFile.endsWith('.ts') || relFile.endsWith('.mts') || relFile.endsWith('.cts')) return ts.ScriptKind.TS;
  return ts.ScriptKind.JS;
}

function lineTextAt(source, index) {
  const start = source.lastIndexOf('\n', index - 1) + 1;
  const end = source.indexOf('\n', index);
  return source.slice(start, end === -1 ? source.length : end).trim();
}

function sourceLine(sourceFile, pos) {
  return sourceFile.getLineAndCharacterOfPosition(pos).line + 1;
}

function addViolation(violations, sourceFile, source, relFile, node, kind) {
  violations.push({
    file: relFile,
    kind,
    line: sourceLine(sourceFile, node.getStart(sourceFile)),
    snippet: lineTextAt(source, node.getStart(sourceFile)),
  });
}

function isEmptyArrayOrObjectOrString(node) {
  return (ts.isArrayLiteralExpression(node) && node.elements.length === 0)
    || (ts.isObjectLiteralExpression(node) && node.properties.length === 0)
    || (ts.isStringLiteral(node) && node.text === '');
}

function isOptionalPropertyChain(node) {
  if (ts.isPropertyAccessExpression(node)) {
    return node.questionDotToken || isOptionalPropertyChain(node.expression);
  }
  if (ts.isElementAccessExpression(node)) {
    return node.questionDotToken || isOptionalPropertyChain(node.expression);
  }
  return false;
}

function isCompatFallbackExpression(node) {
  return ts.isBinaryExpression(node)
    && (node.operatorToken.kind === ts.SyntaxKind.BarBarToken || node.operatorToken.kind === ts.SyntaxKind.QuestionQuestionToken)
    && isOptionalPropertyChain(node.left)
    && isOptionalPropertyChain(node.right);
}

function isDefaultValueFallbackExpression(node) {
  return ts.isBinaryExpression(node)
    && (node.operatorToken.kind === ts.SyntaxKind.BarBarToken || node.operatorToken.kind === ts.SyntaxKind.QuestionQuestionToken)
    && isEmptyArrayOrObjectOrString(node.right);
}

function isDateNowOrParseCall(node, sourceFile) {
  return ts.isCallExpression(node)
    && ts.isPropertyAccessExpression(node.expression)
    && node.expression.expression.getText(sourceFile) === 'Date'
    && (node.expression.name.text === 'now' || node.expression.name.text === 'parse');
}

function isNewDateExpression(node, sourceFile) {
  return ts.isNewExpression(node) && node.expression.getText(sourceFile) === 'Date';
}

function isJsonParseCall(node, sourceFile) {
  return ts.isCallExpression(node)
    && ts.isPropertyAccessExpression(node.expression)
    && node.expression.expression.getText(sourceFile) === 'JSON'
    && node.expression.name.text === 'parse';
}

function isSortWithoutComparator(node) {
  return ts.isCallExpression(node)
    && ts.isPropertyAccessExpression(node.expression)
    && node.expression.name.text === 'sort'
    && node.arguments.length === 0;
}

function isMutableBrowserStorageAccess(node) {
  return ts.isPropertyAccessExpression(node)
    && (node.name.text === 'localStorage' || node.name.text === 'sessionStorage');
}

function isUseClientStoreImport(node) {
  return ts.isImportDeclaration(node)
    && ts.isStringLiteral(node.moduleSpecifier)
    && node.moduleSpecifier.text.includes('useClientStore');
}

export function contractStoreGuardViolationsInSource(relFile, source) {
  const sourceFile = ts.createSourceFile(
    relFile,
    source,
    ts.ScriptTarget.Latest,
    true,
    scriptKindForPath(relFile),
  );
  const violations = [];

  function visit(node) {
    if (isUseClientStoreImport(node) && !allowedStoreHookImportFiles.has(relFile)) {
      addViolation(violations, sourceFile, source, relFile, node, 'store-hook-import');
    }
    if (isMutableBrowserStorageAccess(node)) {
      addViolation(violations, sourceFile, source, relFile, node, 'mutable-browser-storage');
    }
    if (isJsonParseCall(node, sourceFile)) {
      addViolation(violations, sourceFile, source, relFile, node, 'json-parse');
    }
    if (isSortWithoutComparator(node)) {
      addViolation(violations, sourceFile, source, relFile, node, 'sort-without-comparator');
    }
    if (isDateNowOrParseCall(node, sourceFile) || isNewDateExpression(node, sourceFile)) {
      addViolation(violations, sourceFile, source, relFile, node, 'date-parse-order');
    }
    if (isDefaultValueFallbackExpression(node)) {
      addViolation(violations, sourceFile, source, relFile, node, 'default-value-fallback');
    }
    if (isCompatFallbackExpression(node)) {
      addViolation(violations, sourceFile, source, relFile, node, 'compat-field-fallback');
    }
    ts.forEachChild(node, visit);
  }

  visit(sourceFile);
  return violations;
}

export function contractStoreGuardViolationsFromSources(sources) {
  const violations = [];
  for (const [relFile, source] of sources.entries()) {
    violations.push(...contractStoreGuardViolationsInSource(relFile, source));
  }
  return violations;
}

export function collectContractStoreGuardViolations({ root = appRoot, roots = guardedSourceRoots } = {}) {
  const sources = new Map();
  for (const sourceRootName of roots) {
    const sourceRoot = path.join(root, sourceRootName);
    for (const file of walkSourceFiles(sourceRoot)) {
      const relFile = path.relative(root, file).split(path.sep).join('/');
      sources.set(relFile, fs.readFileSync(file, 'utf8'));
    }
  }
  return contractStoreGuardViolationsFromSources(sources);
}

export function summarizeContractStoreGuardViolations(violations) {
  const counts = new Map();
  for (const violation of violations) {
    counts.set(violation.kind, (counts.get(violation.kind) || 0) + 1);
  }
  return counts;
}

export function contractStoreGuardRatchetFailures(violations, limits = ratchetLimits) {
  const counts = summarizeContractStoreGuardViolations(violations);
  return Object.entries(limits)
    .map(([kind, limit]) => ({ kind, count: counts.get(kind) || 0, limit }))
    .filter((entry) => entry.count > entry.limit);
}

if (process.argv[1] === fileURLToPath(import.meta.url)) {
  const violations = collectContractStoreGuardViolations();
  const failures = contractStoreGuardRatchetFailures(violations);
  if (failures.length > 0) {
    console.error('frontend contract/store guard failed:');
    for (const failure of failures) {
      console.error(`- ${failure.kind}: ${failure.count} exceeds ratchet limit ${failure.limit}`);
      for (const violation of violations.filter((entry) => entry.kind === failure.kind).slice(0, 20)) {
        console.error(`  ${violation.file}:${violation.line} ${violation.snippet}`);
      }
    }
    process.exit(1);
  }

  const counts = summarizeContractStoreGuardViolations(violations);
  const summary = Object.entries(ratchetLimits)
    .map(([kind, limit]) => `${kind}=${counts.get(kind) || 0}/${limit}`)
    .join(', ');
  console.log(`frontend contract/store guard passed: ${summary}`);
}
