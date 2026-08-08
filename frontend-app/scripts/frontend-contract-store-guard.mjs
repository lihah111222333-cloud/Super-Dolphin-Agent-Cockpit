import fs from 'node:fs';
import path from 'node:path';
import process from 'node:process';
import { fileURLToPath } from 'node:url';
import ts from 'typescript';

const appRoot = path.resolve(new URL('..', import.meta.url).pathname);
const sourceExtensionPattern = /\.[cm]?[jt]sx?$/;
const guardedSourceRoots = Object.freeze(['src']);

const ratchetLimits = Object.freeze({
  'compat-field-fallback': 0,
  'date-parse-order': 0,
  'default-value-fallback': 0,
  'dynamic-code-execution': 0,
  'guard-bypass-wrapper': 0,
  'json-parse': 0,
  'mutable-browser-storage': 0,
  'sort-without-comparator': 0,
  'store-hook-import': 0,
});

const allowedStoreHookImportFiles = new Set([
  'src/App.jsx',
  'src/pages/settings/SettingsPage.jsx',
]);

const allowedJsonParseFunctionsByFile = Object.freeze({
  'src/entities/client/model/contractStoreModel.js': new Set(['parseRequiredJsonObject']),
  'src/pages/shared/pageShared.js': new Set(['parseStrictJsonValue']),
  'src/shared/api/safeDiagnosticPreview.js': new Set(['parseStrictDiagnosticPreviewJSON']),
});

const allowedDateFunctionsByFile = Object.freeze({
  'src/entities/client/model/contractStoreModel.js': new Set(['systemClockMillis']),
  'src/pages/chat/components/markdownMessageModel.js': new Set(['currentTimestampMs']),
  'src/pages/shared/pageShared.js': new Set(['systemClockNowMillis', 'requireTimestampMillis', 'optionalDateFromValue']),
  'src/shared/api/wailsBridge.js': new Set(['createFrontendTraceTimestamp']),
});

const allowedMutableBrowserStorageFunctionsByFile = Object.freeze({
  'src/shared/api/browser/browserStorage.js': new Set(['requiredAppStoragePort']),
  'src/shared/i18n/appI18n.js': new Set(['initialAppLocale']),
  'src/shared/api/wails/wailsBridgeLogRuntime.js': new Set(['isFrontendTraceDebugEnabled']),
  'src/shared/api/wails/wailsBridgeTraceEvents.js': new Set(['isFrontendTraceDebugEnabled']),
});

const wrapperFunctionNamePattern = /^(?:empty|fallback|default|parseJsonValue$|parseJsonPayload$|now|current.*Time$|dateFrom|timestamp)/i;

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
    if (sourceExtensionPattern.test(entry.name) && !/\.test(?:-helper)?\.[cm]?[jt]sx?$/.test(entry.name)) {
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

function isStringConstructorCall(node, sourceFile) {
  return ts.isCallExpression(node)
    && node.expression.getText(sourceFile) === 'String';
}

function isEmptyFallbackExpression(node, sourceFile) {
  if (ts.isParenthesizedExpression(node)) return isEmptyFallbackExpression(node.expression, sourceFile);
  return isEmptyArrayOrObjectOrString(node)
    || isStringConstructorCall(node, sourceFile)
    || (
      ts.isConditionalExpression(node)
      && (
        isEmptyFallbackExpression(node.whenTrue, sourceFile)
        || isEmptyFallbackExpression(node.whenFalse, sourceFile)
      )
    );
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

function enclosingFunctionName(node) {
  let current = node.parent;
  while (current) {
    if (ts.isFunctionDeclaration(current)) return current.name?.text || '';
    if (ts.isFunctionExpression(current) || ts.isArrowFunction(current)) {
      if (ts.isVariableDeclaration(current.parent) && ts.isIdentifier(current.parent.name)) {
        return current.parent.name.text;
      }
      if (ts.isPropertyAssignment(current.parent) && ts.isIdentifier(current.parent.name)) {
        return current.parent.name.text;
      }
    }
    if (ts.isMethodDeclaration(current) && ts.isIdentifier(current.name)) return current.name.text;
    current = current.parent;
  }
  return '';
}

function isAllowedFunctionCall(node, relFile, allowlist) {
  return Boolean(allowlist[relFile]?.has(enclosingFunctionName(node)));
}

function isAllowedJsonParseCall(node, relFile) {
  return isAllowedFunctionCall(node, relFile, allowedJsonParseFunctionsByFile);
}

function isAllowedDateCall(node, relFile) {
  return isAllowedFunctionCall(node, relFile, allowedDateFunctionsByFile);
}

function isAllowedMutableBrowserStorageAccess(node, relFile) {
  return isAllowedFunctionCall(node, relFile, allowedMutableBrowserStorageFunctionsByFile);
}

function isDateNowOrParseCall(node, sourceFile) {
  return ts.isCallExpression(node)
    && isMemberAccessExpression(node.expression)
    && isGlobalObjectMember(memberAccessObject(node.expression), sourceFile, 'Date')
    && (memberAccessName(node.expression) === 'now' || memberAccessName(node.expression) === 'parse');
}

function isNewDateExpression(node, sourceFile) {
  return ts.isNewExpression(node) && isGlobalObjectMember(node.expression, sourceFile, 'Date');
}

function isJsonParseCall(node, sourceFile) {
  return ts.isCallExpression(node)
    && isMemberAccessExpression(node.expression)
    && isGlobalObjectMember(memberAccessObject(node.expression), sourceFile, 'JSON')
    && memberAccessName(node.expression) === 'parse';
}

function isDynamicCodeExecution(node, sourceFile) {
  if (ts.isCallExpression(node) && ts.isIdentifier(node.expression)) {
    return node.expression.text === 'eval' || node.expression.text === 'Function';
  }
  if (ts.isCallExpression(node) && isMemberAccessExpression(node.expression)) {
    return isGlobalObjectMember(memberAccessObject(node.expression), sourceFile, 'globalThis')
      && (memberAccessName(node.expression) === 'eval' || memberAccessName(node.expression) === 'Function');
  }
  return ts.isNewExpression(node) && isGlobalObjectMember(node.expression, sourceFile, 'Function');
}

function isGlobalObjectMember(node, sourceFile, memberName) {
  if (memberName === 'globalThis') return ts.isIdentifier(node) && node.text === 'globalThis';
  if (ts.isIdentifier(node) && node.text === memberName) return true;
  return isMemberAccessExpression(node)
    && node.expression.getText(sourceFile) === 'globalThis'
    && memberAccessName(node) === memberName;
}

function isMemberAccessExpression(node) {
  return ts.isPropertyAccessExpression(node) || ts.isElementAccessExpression(node);
}

function memberAccessObject(node) {
  return node.expression;
}

function memberAccessName(node) {
  if (ts.isPropertyAccessExpression(node)) return node.name.text;
  if (ts.isStringLiteral(node.argumentExpression) || ts.isNoSubstitutionTemplateLiteral(node.argumentExpression)) {
    return node.argumentExpression.text;
  }
  return '';
}

function isGlobalJsonParseBindExpression(node, sourceFile) {
  return ts.isCallExpression(node)
    && isMemberAccessExpression(node.expression)
    && memberAccessName(node.expression) === 'bind'
    && isMemberAccessExpression(node.expression.expression)
    && isGlobalObjectMember(memberAccessObject(node.expression.expression), sourceFile, 'JSON')
    && memberAccessName(node.expression.expression) === 'parse';
}

function isNewFunctionDateOrJsonBypass(node) {
  return ts.isNewExpression(node)
    && ts.isIdentifier(node.expression)
    && node.expression.text === 'Function'
    && node.arguments?.some((argument) => {
      const text = literalText(argument);
      return text !== null && (/\bDate\b/.test(text) || /\bJSON\s*\.\s*parse\b/.test(text));
    });
}

function literalText(node) {
  if (ts.isStringLiteral(node) || ts.isNoSubstitutionTemplateLiteral(node)) return node.text;
  return null;
}

function variableNamesFromBindingName(name) {
  if (ts.isIdentifier(name)) return [name.text];
  if (ts.isObjectBindingPattern(name) || ts.isArrayBindingPattern(name)) {
    return name.elements.flatMap((element) => {
      if (ts.isBindingElement(element)) return variableNamesFromBindingName(element.name);
      return [];
    });
  }
  return [];
}

function boundGlobalJsonParseAliases(node, sourceFile) {
  if (!ts.isVariableDeclaration(node) || !node.initializer) return [];
  if (!isGlobalJsonParseBindExpression(node.initializer, sourceFile)) return [];
  return variableNamesFromBindingName(node.name);
}

function destructuredGlobalJsonParseAliases(node, sourceFile) {
  if (!ts.isVariableDeclaration(node) || !node.initializer || !ts.isObjectBindingPattern(node.name)) return [];
  if (!isGlobalObjectMember(node.initializer, sourceFile, 'JSON')) return [];
  return node.name.elements.flatMap((element) => {
    if (!ts.isBindingElement(element)) return [];
    const propertyName = element.propertyName || element.name;
    if (!ts.isIdentifier(propertyName) || propertyName.text !== 'parse') return [];
    return variableNamesFromBindingName(element.name);
  });
}

function isBoundGlobalJsonParseAliasCall(node, aliases) {
  return ts.isCallExpression(node)
    && ts.isIdentifier(node.expression)
    && aliases.has(node.expression.text);
}

function isHelperFunctionEmptyFallback(node, sourceFile) {
  const functionBoundary = ts.isArrowFunction(node) ? node : findFunctionBoundary(node);
  if (!hasWrapperFunctionName(functionBoundary)) return false;
  if (ts.isArrowFunction(node) && node.body && !ts.isBlock(node.body)) {
    return isEmptyFallbackExpression(node.body, sourceFile);
  }
  if (!ts.isReturnStatement(node) || !node.expression) return false;
  if (!isEmptyFallbackExpression(node.expression, sourceFile)) return false;
  return true;
}

function isWrapperFunctionDangerousBypass(node, sourceFile, aliases) {
  const functionBoundary = findFunctionBoundary(node);
  if (!hasWrapperFunctionName(functionBoundary)) return false;
  return isNewFunctionDateOrJsonBypass(node)
    || isGlobalJsonParseBindExpression(node, sourceFile)
    || isBoundGlobalJsonParseAliasCall(node, aliases)
    || isDateNowOrParseCall(node, sourceFile)
    || isNewDateExpression(node, sourceFile)
    || isJsonParseCall(node, sourceFile);
}

function isStorageMissingEmptyReturn(node, sourceFile) {
  return ts.isIfStatement(node)
    && isStorageMissingCondition(node.expression, sourceFile)
    && containsEmptyReturn(node.thenStatement, sourceFile);
}

function isStorageMissingCondition(node, sourceFile) {
  if (ts.isPrefixUnaryExpression(node) && node.operator === ts.SyntaxKind.ExclamationToken) {
    return containsStorageIdentifier(node.operand, sourceFile);
  }
  if (ts.isBinaryExpression(node)) {
    const nilOperators = new Set([
      ts.SyntaxKind.EqualsEqualsEqualsToken,
      ts.SyntaxKind.EqualsEqualsToken,
      ts.SyntaxKind.ExclamationEqualsEqualsToken,
      ts.SyntaxKind.ExclamationEqualsToken,
    ]);
    return nilOperators.has(node.operatorToken.kind)
      && (containsStorageIdentifier(node.left, sourceFile) || containsStorageIdentifier(node.right, sourceFile));
  }
  return false;
}

function containsStorageIdentifier(node, sourceFile) {
  if (ts.isIdentifier(node) && /storage/i.test(node.text)) return true;
  if (isMutableBrowserStorageAccess(node)) return true;
  let found = false;
  ts.forEachChild(node, (child) => {
    if (!found && containsStorageIdentifier(child, sourceFile)) found = true;
  });
  return found;
}

function containsEmptyReturn(node, sourceFile) {
  if (ts.isReturnStatement(node)) {
    return Boolean(node.expression && isEmptyFallbackExpression(node.expression, sourceFile));
  }
  if (ts.isBlock(node)) {
    return node.statements.some((statement) => containsEmptyReturn(statement, sourceFile));
  }
  return false;
}

function findFunctionBoundary(node) {
  let current = node.parent;
  while (current) {
    if (
      ts.isFunctionDeclaration(current)
      || ts.isFunctionExpression(current)
      || ts.isArrowFunction(current)
      || ts.isMethodDeclaration(current)
    ) {
      return current;
    }
    current = current.parent;
  }
  return null;
}

function hasWrapperFunctionName(functionNode) {
  const name = functionName(functionNode);
  return Boolean(name && wrapperFunctionNamePattern.test(name));
}

function functionName(functionNode) {
  if (!functionNode) return '';
  if ((ts.isFunctionDeclaration(functionNode) || ts.isFunctionExpression(functionNode) || ts.isMethodDeclaration(functionNode))
    && functionNode.name) {
    return functionNode.name.getText();
  }
  if (ts.isArrowFunction(functionNode) || ts.isFunctionExpression(functionNode)) {
    const parent = functionNode.parent;
    if (ts.isVariableDeclaration(parent) && ts.isIdentifier(parent.name)) return parent.name.text;
    if (ts.isPropertyAssignment(parent) && ts.isIdentifier(parent.name)) return parent.name.text;
  }
  return '';
}

function isSortWithoutComparator(node) {
  return ts.isCallExpression(node)
    && ts.isPropertyAccessExpression(node.expression)
    && node.expression.name.text === 'sort'
    && node.arguments.length === 0;
}

function isMutableBrowserStorageAccess(node) {
  if (ts.isPropertyAccessExpression(node)) {
    return node.name.text === 'localStorage' || node.name.text === 'sessionStorage';
  }
  if (ts.isElementAccessExpression(node)) {
    const arg = node.argumentExpression;
    if (ts.isStringLiteral(arg) || ts.isNoSubstitutionTemplateLiteral(arg)) {
      return arg.text === 'localStorage' || arg.text === 'sessionStorage';
    }
  }
  return false;
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
  const boundJsonParseAliases = new Set();

  function visit(node) {
    for (const alias of [
      ...boundGlobalJsonParseAliases(node, sourceFile),
      ...destructuredGlobalJsonParseAliases(node, sourceFile),
    ]) {
      boundJsonParseAliases.add(alias);
    }
    if (isUseClientStoreImport(node) && !allowedStoreHookImportFiles.has(relFile)) {
      addViolation(violations, sourceFile, source, relFile, node, 'store-hook-import');
    }
    if (isMutableBrowserStorageAccess(node) && !isAllowedMutableBrowserStorageAccess(node, relFile)) {
      addViolation(violations, sourceFile, source, relFile, node, 'mutable-browser-storage');
    }
    if (isDynamicCodeExecution(node, sourceFile)) {
      addViolation(violations, sourceFile, source, relFile, node, 'dynamic-code-execution');
    }
    if (isJsonParseCall(node, sourceFile) && !isAllowedJsonParseCall(node, relFile)) {
      addViolation(violations, sourceFile, source, relFile, node, 'json-parse');
    }
    if (isBoundGlobalJsonParseAliasCall(node, boundJsonParseAliases)) {
      addViolation(violations, sourceFile, source, relFile, node, 'json-parse');
    }
    if (isSortWithoutComparator(node)) {
      addViolation(violations, sourceFile, source, relFile, node, 'sort-without-comparator');
    }
    if ((isDateNowOrParseCall(node, sourceFile) || isNewDateExpression(node, sourceFile)) && !isAllowedDateCall(node, relFile)) {
      addViolation(violations, sourceFile, source, relFile, node, 'date-parse-order');
    }
    if (isDefaultValueFallbackExpression(node)) {
      addViolation(violations, sourceFile, source, relFile, node, 'default-value-fallback');
    }
    if (isCompatFallbackExpression(node)) {
      addViolation(violations, sourceFile, source, relFile, node, 'compat-field-fallback');
    }
    if (
      isHelperFunctionEmptyFallback(node, sourceFile)
      || isWrapperFunctionDangerousBypass(node, sourceFile, boundJsonParseAliases)
      || isNewFunctionDateOrJsonBypass(node)
      || isStorageMissingEmptyReturn(node, sourceFile)
    ) {
      addViolation(violations, sourceFile, source, relFile, node, 'guard-bypass-wrapper');
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
