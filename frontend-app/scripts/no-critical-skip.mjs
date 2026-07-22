import fs from 'node:fs';
import path from 'node:path';
import process from 'node:process';
import { fileURLToPath } from 'node:url';
import ts from 'typescript';

const appRoot = path.resolve(new URL('..', import.meta.url).pathname);

export const CRITICAL_SKIP_ROOTS = Object.freeze(['src', 'scripts']);
export const criticalPattern = /\b(provider|thread|turn|workflow|rpc|contract|desktop|smoke)\b/i;

function walkTestFiles(dir) {
  if (!fs.existsSync(dir)) {
    throw new Error(`critical skip root does not exist: ${dir}`);
  }
  if (!fs.statSync(dir).isDirectory()) {
    throw new Error(`critical skip root is not a directory: ${dir}`);
  }
  const entries = fs.readdirSync(dir, { withFileTypes: true });
  const files = [];
  for (const entry of entries) {
    if (entry.name === 'node_modules' || entry.name === 'dist') continue;
    const fullPath = path.join(dir, entry.name);
    if (entry.isDirectory()) {
      files.push(...walkTestFiles(fullPath));
      continue;
    }
    if (/\.(test|spec)\.[cm]?[jt]sx?$/.test(entry.name)) {
      files.push(fullPath);
    }
  }
  return files;
}

function scriptKindForFile(relFile) {
  const lowerFile = relFile.toLowerCase();
  if (lowerFile.endsWith('.tsx')) return ts.ScriptKind.TSX;
  if (lowerFile.endsWith('.jsx')) return ts.ScriptKind.JSX;
  if (/\.[cm]?ts$/.test(lowerFile)) return ts.ScriptKind.TS;
  return ts.ScriptKind.JS;
}

function lineNumberForNode(sourceFile, node) {
  return sourceFile.getLineAndCharacterOfPosition(node.getStart(sourceFile)).line + 1;
}

function isTestApiIdentifier(node) {
  return ts.isIdentifier(node) && ['describe', 'it', 'test'].includes(node.text);
}

function isStaticSkipProperty(node) {
  return ts.isStringLiteralLike(node) && node.text === 'skip';
}

function isSkipMemberAccess(node) {
  if (ts.isPropertyAccessExpression(node)) {
    return node.name.text === 'skip' && isTestApiIdentifier(node.expression);
  }
  if (ts.isElementAccessExpression(node)) {
    return isStaticSkipProperty(node.argumentExpression) && isTestApiIdentifier(node.expression);
  }
  return false;
}

function isSkipEachCallExpression(node) {
  if (!ts.isCallExpression(node)) return false;
  const callee = node.expression;
  return (
    ts.isPropertyAccessExpression(callee) &&
    callee.name.text === 'each' &&
    isSkipMemberAccess(callee.expression)
  );
}

function isSkippedTestCall(node) {
  const expression = node.expression;
  return isSkipMemberAccess(expression) || isSkipEachCallExpression(expression);
}

function skippedTestName(node) {
  const [nameNode] = node.arguments;
  if (nameNode && ts.isStringLiteralLike(nameNode)) {
    return { name: nameNode.text, parseError: false };
  }
  return { name: '<unparseable>', parseError: true };
}

export function skippedTestsInSource(relFile, source) {
  const sourceFile = ts.createSourceFile(
    relFile,
    source,
    ts.ScriptTarget.Latest,
    true,
    scriptKindForFile(relFile),
  );
  if (sourceFile.parseDiagnostics.length > 0) {
    const diagnostic = sourceFile.parseDiagnostics[0];
    const { line } = sourceFile.getLineAndCharacterOfPosition(diagnostic.start ?? 0);
    throw new Error(`critical skip source parse failed: ${relFile}:${line + 1}`);
  }

  const skips = [];

  function visit(node) {
    if (ts.isCallExpression(node) && isSkippedTestCall(node)) {
      const { name, parseError } = skippedTestName(node);
      skips.push({
        file: relFile,
        line: lineNumberForNode(sourceFile, node),
        name,
        parseError,
      });
    }
    ts.forEachChild(node, visit);
  }

  visit(sourceFile);
  return skips;
}

export function criticalSkipViolationsFromSources(sources) {
  const violations = [];
  for (const [relFile, source] of sources.entries()) {
    for (const skip of skippedTestsInSource(relFile, source)) {
      if (skip.parseError || criticalPattern.test(skip.name) || criticalPattern.test(skip.file)) {
        violations.push(skip);
      }
    }
  }
  return violations;
}

export function collectCriticalSkipViolations({ root = appRoot, roots = CRITICAL_SKIP_ROOTS } = {}) {
  const sources = new Map();
  for (const sourceRootName of roots) {
    const sourceRoot = path.join(root, sourceRootName);
    for (const file of walkTestFiles(sourceRoot)) {
      const relFile = path.relative(root, file).split(path.sep).join('/');
      sources.set(relFile, fs.readFileSync(file, 'utf8'));
    }
  }
  return criticalSkipViolationsFromSources(sources);
}

if (process.argv[1] === fileURLToPath(import.meta.url)) {
  const violations = collectCriticalSkipViolations();
  if (violations.length > 0) {
    console.error('critical .skip guard failed:');
    for (const violation of violations) {
      console.error(`- ${violation.file}:${violation.line} :: ${violation.name}`);
    }
    process.exit(1);
  }

  console.log('critical .skip guard passed: no critical skips (0 found)');
}
