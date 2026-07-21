import ts from 'typescript';
import {
  addViolation, allowedStoreHookImportFiles, isAllowedDateCall, isAllowedJsonParseCall,
  isAllowedMutableBrowserStorageAccess, isCompatFallbackExpression, isDefaultValueFallbackExpression,
  isEmptyFallbackExpression, scriptKindForPath, wrapperFunctionNamePattern,
} from './frontend-contract-store-guard-rules.mjs';

function isMemberAccessExpression(node) { return ts.isPropertyAccessExpression(node) || ts.isElementAccessExpression(node); }
function memberAccessObject(node) { return node.expression; }
function memberAccessName(node) {
  if (ts.isPropertyAccessExpression(node)) return node.name.text;
  return ts.isStringLiteral(node.argumentExpression) || ts.isNoSubstitutionTemplateLiteral(node.argumentExpression) ? node.argumentExpression.text : '';
}
function isGlobalObjectMember(node, sourceFile, memberName) {
  if (memberName === 'globalThis') return ts.isIdentifier(node) && node.text === 'globalThis';
  if (ts.isIdentifier(node) && node.text === memberName) return true;
  return isMemberAccessExpression(node) && node.expression.getText(sourceFile) === 'globalThis' && memberAccessName(node) === memberName;
}
function isDateNowOrParseCall(node, sourceFile) {
  return ts.isCallExpression(node) && isMemberAccessExpression(node.expression)
    && isGlobalObjectMember(memberAccessObject(node.expression), sourceFile, 'Date')
    && (memberAccessName(node.expression) === 'now' || memberAccessName(node.expression) === 'parse');
}
function isNewDateExpression(node, sourceFile) { return ts.isNewExpression(node) && isGlobalObjectMember(node.expression, sourceFile, 'Date'); }
function isJsonParseCall(node, sourceFile) {
  return ts.isCallExpression(node) && isMemberAccessExpression(node.expression)
    && isGlobalObjectMember(memberAccessObject(node.expression), sourceFile, 'JSON') && memberAccessName(node.expression) === 'parse';
}
function isDynamicCodeExecution(node, sourceFile) {
  if (ts.isCallExpression(node) && ts.isIdentifier(node.expression)) return node.expression.text === 'eval' || node.expression.text === 'Function';
  if (ts.isCallExpression(node) && isMemberAccessExpression(node.expression)) {
    return isGlobalObjectMember(memberAccessObject(node.expression), sourceFile, 'globalThis') && ['eval', 'Function'].includes(memberAccessName(node.expression));
  }
  return ts.isNewExpression(node) && isGlobalObjectMember(node.expression, sourceFile, 'Function');
}
function isGlobalJsonParseBindExpression(node, sourceFile) {
  return ts.isCallExpression(node) && isMemberAccessExpression(node.expression) && memberAccessName(node.expression) === 'bind'
    && isMemberAccessExpression(node.expression.expression) && isGlobalObjectMember(memberAccessObject(node.expression.expression), sourceFile, 'JSON')
    && memberAccessName(node.expression.expression) === 'parse';
}
function variableNamesFromBindingName(name) {
  if (ts.isIdentifier(name)) return [name.text];
  if (ts.isObjectBindingPattern(name) || ts.isArrayBindingPattern(name)) {
    return name.elements.flatMap((item) => (
      ts.isBindingElement(item) ? variableNamesFromBindingName(item.name) : []
    ));
  }
  return [];
}
function jsonParseAliases(node, sourceFile) {
  if (!ts.isVariableDeclaration(node) || !node.initializer) return [];
  if (isGlobalJsonParseBindExpression(node.initializer, sourceFile)) return variableNamesFromBindingName(node.name);
  if (!ts.isObjectBindingPattern(node.name) || !isGlobalObjectMember(node.initializer, sourceFile, 'JSON')) return [];
  return node.name.elements.flatMap((item) => {
    const propertyName = ts.isBindingElement(item) ? item.propertyName || item.name : null;
    return ts.isIdentifier(propertyName) && propertyName.text === 'parse' ? variableNamesFromBindingName(item.name) : [];
  });
}
function isBoundGlobalJsonParseAliasCall(node, aliases) { return ts.isCallExpression(node) && ts.isIdentifier(node.expression) && aliases.has(node.expression.text); }
function findFunctionBoundary(node) {
  let current = node.parent;
  while (current) {
    if (ts.isFunctionDeclaration(current) || ts.isFunctionExpression(current) || ts.isArrowFunction(current) || ts.isMethodDeclaration(current)) return current;
    current = current.parent;
  }
  return null;
}
function functionName(node) {
  if (!node) return '';
  if ((ts.isFunctionDeclaration(node) || ts.isFunctionExpression(node) || ts.isMethodDeclaration(node)) && node.name) return node.name.getText();
  const parent = (ts.isArrowFunction(node) || ts.isFunctionExpression(node)) && node.parent;
  if (ts.isVariableDeclaration(parent) && ts.isIdentifier(parent.name)) return parent.name.text;
  if (ts.isPropertyAssignment(parent) && ts.isIdentifier(parent.name)) return parent.name.text;
  return '';
}
function isHelperFunctionEmptyFallback(node, sourceFile) {
  const boundary = ts.isArrowFunction(node) ? node : findFunctionBoundary(node);
  if (!wrapperFunctionNamePattern.test(functionName(boundary))) return false;
  return ts.isArrowFunction(node) && !ts.isBlock(node.body) ? isEmptyFallbackExpression(node.body, sourceFile)
    : ts.isReturnStatement(node) && Boolean(node.expression && isEmptyFallbackExpression(node.expression, sourceFile));
}
function isNewFunctionDateOrJsonBypass(node) {
  return ts.isNewExpression(node) && ts.isIdentifier(node.expression) && node.expression.text === 'Function'
    && node.arguments?.some((arg) => ts.isStringLiteral(arg) || ts.isNoSubstitutionTemplateLiteral(arg) ? /\bDate\b|\bJSON\s*\.\s*parse\b/.test(arg.text) : false);
}
function isWrapperFunctionDangerousBypass(node, sourceFile, aliases) {
  return wrapperFunctionNamePattern.test(functionName(findFunctionBoundary(node))) && (isNewFunctionDateOrJsonBypass(node) || isGlobalJsonParseBindExpression(node, sourceFile)
    || isBoundGlobalJsonParseAliasCall(node, aliases) || isDateNowOrParseCall(node, sourceFile) || isNewDateExpression(node, sourceFile) || isJsonParseCall(node, sourceFile));
}
function isMutableBrowserStorageAccess(node) {
  if (ts.isPropertyAccessExpression(node)) return ['localStorage', 'sessionStorage'].includes(node.name.text);
  const arg = ts.isElementAccessExpression(node) && node.argumentExpression;
  return ts.isStringLiteral(arg) || ts.isNoSubstitutionTemplateLiteral(arg) ? ['localStorage', 'sessionStorage'].includes(arg.text) : false;
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
  return ts.isBlock(node) && node.statements.some((item) => containsEmptyReturn(item, sourceFile));
}
function isStorageMissingEmptyReturn(node, sourceFile) {
  if (!ts.isIfStatement(node)) return false;
  const expression = node.expression;
  const nilOperators = new Set([
    ts.SyntaxKind.EqualsEqualsEqualsToken,
    ts.SyntaxKind.EqualsEqualsToken,
    ts.SyntaxKind.ExclamationEqualsEqualsToken,
    ts.SyntaxKind.ExclamationEqualsToken,
  ]);
  const missing = ts.isPrefixUnaryExpression(expression)
    && expression.operator === ts.SyntaxKind.ExclamationToken
    ? containsStorageIdentifier(expression.operand, sourceFile)
    : ts.isBinaryExpression(expression)
      && nilOperators.has(expression.operatorToken.kind)
      && (containsStorageIdentifier(expression.left, sourceFile)
        || containsStorageIdentifier(expression.right, sourceFile));
  return missing && containsEmptyReturn(node.thenStatement, sourceFile);
}
function isSortWithoutComparator(node) {
  return ts.isCallExpression(node)
    && ts.isPropertyAccessExpression(node.expression)
    && node.expression.name.text === 'sort'
    && node.arguments.length === 0;
}

function isUseClientStoreImport(node) {
  return ts.isImportDeclaration(node)
    && ts.isStringLiteral(node.moduleSpecifier)
    && node.moduleSpecifier.text.includes('useClientStore');
}

export function contractStoreGuardViolationsInSource(relFile, source) {
  const sourceFile = ts.createSourceFile(relFile, source, ts.ScriptTarget.Latest, true, scriptKindForPath(relFile));
  const violations = []; const aliases = new Set();
  function visit(node) {
    for (const alias of jsonParseAliases(node, sourceFile)) aliases.add(alias);
    if (isUseClientStoreImport(node) && !allowedStoreHookImportFiles.has(relFile)) {
      addViolation(violations, sourceFile, source, relFile, node, 'store-hook-import');
    }
    if (isMutableBrowserStorageAccess(node) && !isAllowedMutableBrowserStorageAccess(node, relFile)) {
      addViolation(violations, sourceFile, source, relFile, node, 'mutable-browser-storage');
    }
    if (isDynamicCodeExecution(node, sourceFile)) addViolation(violations, sourceFile, source, relFile, node, 'dynamic-code-execution');
    if (isJsonParseCall(node, sourceFile) && !isAllowedJsonParseCall(node, relFile)) addViolation(violations, sourceFile, source, relFile, node, 'json-parse');
    if (isBoundGlobalJsonParseAliasCall(node, aliases)) addViolation(violations, sourceFile, source, relFile, node, 'json-parse');
    if (isSortWithoutComparator(node)) addViolation(violations, sourceFile, source, relFile, node, 'sort-without-comparator');
    if ((isDateNowOrParseCall(node, sourceFile) || isNewDateExpression(node, sourceFile))
      && !isAllowedDateCall(node, relFile)) {
      addViolation(violations, sourceFile, source, relFile, node, 'date-parse-order');
    }
    if (isDefaultValueFallbackExpression(node)) addViolation(violations, sourceFile, source, relFile, node, 'default-value-fallback');
    if (isCompatFallbackExpression(node)) addViolation(violations, sourceFile, source, relFile, node, 'compat-field-fallback');
    if (
      isHelperFunctionEmptyFallback(node, sourceFile)
      || isWrapperFunctionDangerousBypass(node, sourceFile, aliases)
      || isNewFunctionDateOrJsonBypass(node)
      || isStorageMissingEmptyReturn(node, sourceFile)
    ) {
      addViolation(violations, sourceFile, source, relFile, node, 'guard-bypass-wrapper');
    }
    ts.forEachChild(node, visit);
  }
  visit(sourceFile); return violations;
}

export function contractStoreGuardViolationsFromSources(sources) {
  const violations = [];
  for (const [relFile, source] of sources.entries()) violations.push(...contractStoreGuardViolationsInSource(relFile, source));
  return violations;
}
