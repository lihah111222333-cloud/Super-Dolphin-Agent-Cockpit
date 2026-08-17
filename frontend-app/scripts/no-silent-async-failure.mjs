import fs from 'node:fs';
import path from 'node:path';
import process from 'node:process';
import ts from 'typescript';

// 公共门禁直接使用 Node 的本地模块目录，确保 Windows 与 POSIX 使用同一根目录语义。
const appRoot = path.resolve(import.meta.dirname, '..');

export const SILENT_ASYNC_FAILURE_ROOTS = Object.freeze(['src']);
const sourceExtensionPattern = /\.[cm]?[jt]sx?$/;

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
    if (sourceExtensionPattern.test(entry.name)) {
      files.push(fullPath);
    }
  }
  return files;
}

function lineTextAt(source, index) {
  const start = source.lastIndexOf('\n', index - 1) + 1;
  const end = source.indexOf('\n', index);
  return source.slice(start, end === -1 ? source.length : end).trim();
}

function scriptKindForPath(relFile) {
  if (relFile.endsWith('.tsx')) return ts.ScriptKind.TSX;
  if (relFile.endsWith('.jsx')) return ts.ScriptKind.JSX;
  if (relFile.endsWith('.ts') || relFile.endsWith('.mts') || relFile.endsWith('.cts')) return ts.ScriptKind.TS;
  return ts.ScriptKind.JS;
}

function sourceLine(sourceFile, pos) {
  return sourceFile.getLineAndCharacterOfPosition(pos).line + 1;
}

function blockContainsOnlyTriviaAndComment(source, block) {
  const body = source.slice(block.getStart() + 1, block.end - 1);
  return body.includes('//') || body.includes('/*');
}

function addViolation(violations, sourceFile, source, relFile, pos, kind) {
  violations.push({
    file: relFile,
    line: sourceLine(sourceFile, pos),
    kind,
    snippet: lineTextAt(source, pos),
  });
}

function isCatchPropertyAccess(expr) {
  return ts.isPropertyAccessExpression(expr) && expr.name.text === 'catch';
}

function isEmptyFunctionBodyWithNoComment(source, fn) {
  return fn.body && ts.isBlock(fn.body) && fn.body.statements.length === 0 && !blockContainsOnlyTriviaAndComment(source, fn.body);
}

function functionParameterNames(fn) {
  return new Set(fn.parameters
    .map((parameter) => parameter.name)
    .filter((name) => ts.isIdentifier(name))
    .map((name) => name.text));
}

function catchParameterNames(catchClause) {
  const name = catchClause.variableDeclaration?.name;
  if (!name || !ts.isIdentifier(name)) return new Set();
  return new Set([name.text]);
}

function isVoidDiscardExpression(expr, parameterNames) {
  return ts.isVoidExpression(expr)
    && ts.isIdentifier(expr.expression)
    && parameterNames.has(expr.expression.text);
}

function isVoidDiscardStatement(statement, parameterNames) {
  return ts.isExpressionStatement(statement)
    && isVoidDiscardExpression(statement.expression, parameterNames);
}

function isDiscardOnlyBlock(block, parameterNames) {
  return parameterNames.size > 0
    && block.statements.length > 0
    && block.statements.every((statement) => isVoidDiscardStatement(statement, parameterNames));
}

function isDiscardOnlyFunctionBody(fn) {
  const parameterNames = functionParameterNames(fn);
  if (parameterNames.size === 0 || !fn.body) return false;
  if (ts.isBlock(fn.body)) return isDiscardOnlyBlock(fn.body, parameterNames);
  return isVoidDiscardExpression(fn.body, parameterNames);
}

export function silentAsyncFailureViolationsInSource(relFile, source) {
  const sourceFile = ts.createSourceFile(
    relFile,
    source,
    ts.ScriptTarget.Latest,
    true,
    scriptKindForPath(relFile),
  );
  const violations = [];

  function visit(node) {
    if (ts.isCallExpression(node) && isCatchPropertyAccess(node.expression)) {
      const handler = node.arguments[0];
      if (handler && (ts.isArrowFunction(handler) || ts.isFunctionExpression(handler)) && isEmptyFunctionBodyWithNoComment(source, handler)) {
        addViolation(violations, sourceFile, source, relFile, node.expression.name.getStart(sourceFile), 'empty-promise-catch');
      }
      else if (handler && (ts.isArrowFunction(handler) || ts.isFunctionExpression(handler)) && isDiscardOnlyFunctionBody(handler)) {
        addViolation(violations, sourceFile, source, relFile, node.expression.name.getStart(sourceFile), 'discarded-promise-catch-error');
      }
    }
    if (ts.isTryStatement(node) && node.catchClause) {
      const body = node.catchClause.block;
      if (body.statements.length === 0 && !blockContainsOnlyTriviaAndComment(source, body)) {
        addViolation(violations, sourceFile, source, relFile, node.catchClause.getStart(sourceFile), 'empty-catch-block');
      }
      else if (isDiscardOnlyBlock(body, catchParameterNames(node.catchClause))) {
        addViolation(violations, sourceFile, source, relFile, node.catchClause.getStart(sourceFile), 'discarded-catch-error');
      }
    }
    ts.forEachChild(node, visit);
  }

  visit(sourceFile);
  return violations;
}

export function silentAsyncFailureViolationsFromSources(sources) {
  const violations = [];
  for (const [relFile, source] of sources.entries()) {
    violations.push(...silentAsyncFailureViolationsInSource(relFile, source));
  }
  return violations;
}

export function collectSilentAsyncFailureViolations({ root = appRoot, roots = SILENT_ASYNC_FAILURE_ROOTS } = {}) {
  const sources = new Map();
  for (const sourceRootName of roots) {
    const sourceRoot = path.join(root, sourceRootName);
    for (const file of walkSourceFiles(sourceRoot)) {
      const relFile = path.relative(root, file).split(path.sep).join('/');
      sources.set(relFile, fs.readFileSync(file, 'utf8'));
    }
  }
  return silentAsyncFailureViolationsFromSources(sources);
}

if (path.resolve(process.argv[1] || '') === import.meta.filename) {
  const violations = collectSilentAsyncFailureViolations();
  if (violations.length > 0) {
    console.error('silent async failure guard failed:');
    for (const violation of violations) {
      console.error(`- ${violation.file}:${violation.line} [${violation.kind}] ${violation.snippet}`);
    }
    process.exit(1);
  }

  console.log('silent async failure guard passed: no silent catch handlers (0 found)');
}
