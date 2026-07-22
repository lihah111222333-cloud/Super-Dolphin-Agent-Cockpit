import fs from 'node:fs';
import path from 'node:path';
import process from 'node:process';
import { fileURLToPath } from 'node:url';
import ts from 'typescript';

const appRoot = path.resolve(new URL('..', import.meta.url).pathname);

export const CRITICAL_SKIP_ROOTS = Object.freeze(['src', 'scripts', 'tests']);
export const criticalPattern = /\b(provider|thread|turn|workflow|rpc|contract|desktop|smoke)\b/i;

const TEST_API_MODULES = new Map([
  ['vitest', new Set(['describe', 'it', 'test'])],
  ['@playwright/test', new Set(['test'])],
]);

const LEGACY_TEST_API_BINDINGS = new Map(
  ['describe', 'it', 'test'].map((apiName) => [apiName, { moduleName: null, apiName }]),
);

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

function staticStringValue(node) {
  return ts.isStringLiteralLike(node) ? node.text : null;
}

function isStaticSkipProperty(node) {
  return staticStringValue(node) === 'skip';
}

function staticPropertyName(node) {
  if (ts.isPropertyAccessExpression(node)) {
    return node.name.text;
  }
  if (ts.isElementAccessExpression(node)) {
    return staticStringValue(node.argumentExpression);
  }
  return null;
}

function collectTestApiBindings(sourceFile) {
  const identifiers = new Map(LEGACY_TEST_API_BINDINGS);
  const namespaces = new Map();

  for (const statement of sourceFile.statements) {
    if (!ts.isImportDeclaration(statement) || !ts.isStringLiteralLike(statement.moduleSpecifier)) {
      continue;
    }

    const moduleName = statement.moduleSpecifier.text;
    const supportedApis = TEST_API_MODULES.get(moduleName);
    const importClause = statement.importClause;
    if (!supportedApis || !importClause
      || importClause.phaseModifier === ts.SyntaxKind.TypeKeyword || !importClause.namedBindings) {
      continue;
    }

    if (ts.isNamespaceImport(importClause.namedBindings)) {
      namespaces.set(importClause.namedBindings.name.text, { moduleName });
      continue;
    }

    if (!ts.isNamedImports(importClause.namedBindings)) {
      continue;
    }

    for (const specifier of importClause.namedBindings.elements) {
      if (specifier.isTypeOnly) continue;
      const apiName = (specifier.propertyName ?? specifier.name).text;
      if (supportedApis.has(apiName)) {
        identifiers.set(specifier.name.text, { moduleName, apiName });
      }
    }
  }

  return { identifiers, namespaces };
}

function testApiBindingForExpression(node, bindings) {
  if (ts.isIdentifier(node)) {
    return bindings.identifiers.get(node.text) ?? null;
  }

  const propertyName = staticPropertyName(node);
  if (propertyName === null || (!ts.isPropertyAccessExpression(node) && !ts.isElementAccessExpression(node))) {
    return null;
  }

  if (ts.isIdentifier(node.expression)) {
    const namespace = bindings.namespaces.get(node.expression.text);
    const supportedApis = namespace && TEST_API_MODULES.get(namespace.moduleName);
    if (supportedApis?.has(propertyName)) {
      return { moduleName: namespace.moduleName, apiName: propertyName };
    }
  }

  const parentBinding = testApiBindingForExpression(node.expression, bindings);
  if (
    parentBinding?.moduleName === '@playwright/test'
    && parentBinding.apiName === 'test'
    && propertyName === 'describe'
  ) {
    return { moduleName: parentBinding.moduleName, apiName: propertyName };
  }

  return null;
}

function isTestApiExpression(node, bindings) {
  return testApiBindingForExpression(node, bindings) !== null;
}

function isDynamicTestApiModuleLoad(node) {
  if (!ts.isCallExpression(node) || node.arguments.length === 0) return false;
  const [moduleSpecifier] = node.arguments;
  if (!ts.isStringLiteralLike(moduleSpecifier) || !TEST_API_MODULES.has(moduleSpecifier.text)) {
    return false;
  }

  return (
    node.expression.kind === ts.SyntaxKind.ImportKeyword
    || (ts.isIdentifier(node.expression) && node.expression.text === 'require')
  );
}

function assertNoDynamicTestApiBindings(sourceFile, relFile) {
  function visit(node) {
    if (isDynamicTestApiModuleLoad(node)) {
      throw new Error(`critical skip source dynamic test API binding: ${relFile}:${lineNumberForNode(sourceFile, node)}`);
    }
    ts.forEachChild(node, visit);
  }

  visit(sourceFile);
}

function isSkipMemberAccess(node, bindings) {
  if (ts.isPropertyAccessExpression(node)) {
    return node.name.text === 'skip' && isTestApiExpression(node.expression, bindings);
  }
  if (ts.isElementAccessExpression(node)) {
    return isStaticSkipProperty(node.argumentExpression) && isTestApiExpression(node.expression, bindings);
  }
  return false;
}

function isSkipEachCallExpression(node, bindings) {
  if (!ts.isCallExpression(node)) return false;
  const callee = node.expression;
  return (
    ts.isPropertyAccessExpression(callee)
    && callee.name.text === 'each'
    && isSkipMemberAccess(callee.expression, bindings)
  );
}

function isSkippedTestCall(node, bindings) {
  const expression = node.expression;
  return isSkipMemberAccess(expression, bindings) || isSkipEachCallExpression(expression, bindings);
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

  const testApiBindings = collectTestApiBindings(sourceFile);
  assertNoDynamicTestApiBindings(sourceFile, relFile);

  const skips = [];

  function visit(node) {
    if (ts.isCallExpression(node) && isSkippedTestCall(node, testApiBindings)) {
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
