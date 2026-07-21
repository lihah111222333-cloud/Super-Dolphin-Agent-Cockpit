import ts from 'typescript';

export const ratchetLimits = Object.freeze({
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

export const allowedStoreHookImportFiles = new Set([
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

export const wrapperFunctionNamePattern = /^(?:empty|fallback|default|parseJsonValue$|parseJsonPayload$|now|current.*Time$|dateFrom|timestamp)/i;

export function scriptKindForPath(relFile) {
  if (relFile.endsWith('.tsx')) return ts.ScriptKind.TSX;
  if (relFile.endsWith('.jsx')) return ts.ScriptKind.JSX;
  if (relFile.endsWith('.ts') || relFile.endsWith('.mts') || relFile.endsWith('.cts')) return ts.ScriptKind.TS;
  return ts.ScriptKind.JS;
}

export function lineTextAt(source, index) {
  const start = source.lastIndexOf('\n', index - 1) + 1;
  const end = source.indexOf('\n', index);
  return source.slice(start, end === -1 ? source.length : end).trim();
}

export function addViolation(violations, sourceFile, source, relFile, node, kind) {
  violations.push({
    file: relFile,
    kind,
    line: sourceFile.getLineAndCharacterOfPosition(node.getStart(sourceFile)).line + 1,
    snippet: lineTextAt(source, node.getStart(sourceFile)),
  });
}

function isEmptyArrayOrObjectOrString(node) {
  return (ts.isArrayLiteralExpression(node) && node.elements.length === 0)
    || (ts.isObjectLiteralExpression(node) && node.properties.length === 0)
    || (ts.isStringLiteral(node) && node.text === '');
}

export function isEmptyFallbackExpression(node, sourceFile) {
  if (ts.isParenthesizedExpression(node)) return isEmptyFallbackExpression(node.expression, sourceFile);
  const empty = isEmptyArrayOrObjectOrString(node)
    || (ts.isCallExpression(node) && node.expression.getText(sourceFile) === 'String');
  return empty || (ts.isConditionalExpression(node)
    && (isEmptyFallbackExpression(node.whenTrue, sourceFile) || isEmptyFallbackExpression(node.whenFalse, sourceFile)));
}

export function isOptionalPropertyChain(node) {
  if (ts.isPropertyAccessExpression(node) || ts.isElementAccessExpression(node)) {
    return node.questionDotToken || isOptionalPropertyChain(node.expression);
  }
  return false;
}

export function isCompatFallbackExpression(node) {
  return ts.isBinaryExpression(node)
    && (node.operatorToken.kind === ts.SyntaxKind.BarBarToken || node.operatorToken.kind === ts.SyntaxKind.QuestionQuestionToken)
    && isOptionalPropertyChain(node.left) && isOptionalPropertyChain(node.right);
}

export function isDefaultValueFallbackExpression(node) {
  return ts.isBinaryExpression(node)
    && (node.operatorToken.kind === ts.SyntaxKind.BarBarToken || node.operatorToken.kind === ts.SyntaxKind.QuestionQuestionToken)
    && isEmptyArrayOrObjectOrString(node.right);
}

export function enclosingFunctionName(node) {
  let current = node.parent;
  while (current) {
    if (ts.isFunctionDeclaration(current)) return current.name?.text || '';
    if (ts.isFunctionExpression(current) || ts.isArrowFunction(current)) {
      if (ts.isVariableDeclaration(current.parent) && ts.isIdentifier(current.parent.name)) return current.parent.name.text;
      if (ts.isPropertyAssignment(current.parent) && ts.isIdentifier(current.parent.name)) return current.parent.name.text;
    }
    if (ts.isMethodDeclaration(current) && ts.isIdentifier(current.name)) return current.name.text;
    current = current.parent;
  }
  return '';
}

function isAllowedFunctionCall(node, relFile, allowlist) {
  return Boolean(allowlist[relFile]?.has(enclosingFunctionName(node)));
}

export const isAllowedJsonParseCall = (node, relFile) => isAllowedFunctionCall(node, relFile, allowedJsonParseFunctionsByFile);
export const isAllowedDateCall = (node, relFile) => isAllowedFunctionCall(node, relFile, allowedDateFunctionsByFile);
export const isAllowedMutableBrowserStorageAccess = (node, relFile) => isAllowedFunctionCall(node, relFile, allowedMutableBrowserStorageFunctionsByFile);
