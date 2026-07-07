import { parse } from '@babel/parser';

export const NON_LITERAL_DYNAMIC_IMPORT = '__non_literal_dynamic_import__';
export const COMPUTED_VITEST_MODULE_MOCK = '__computed_vitest_module_mock__';

function parseModule(source) {
  return parse(source, {
    sourceType: 'module',
    createImportExpressions: true,
    plugins: ['jsx', 'typescript', 'dynamicImport', 'importAttributes'],
  });
}

function literalString(node) {
  if (!node) return '';
  if (node.type === 'StringLiteral') return node.value;
  if (node.type === 'Literal' && typeof node.value === 'string') return node.value;
  return '';
}

function memberPropertyName(node) {
  if (!node) return '';
  if (!node.computed && node.property?.type === 'Identifier') return node.property.name;
  return literalString(node.property);
}

function vitestMockMember(node) {
  if (
    node?.type !== 'MemberExpression'
    || node.object?.type !== 'Identifier'
    || node.object.name !== 'vi'
  ) {
    return { known: false, unknownComputed: false };
  }
  const name = memberPropertyName(node);
  return {
    known: ['mock', 'doMock', 'unstable_mockModule'].includes(name),
    unknownComputed: node.computed && !name,
  };
}

function unwrapVitestCallApply(node) {
  if (
    node?.type === 'MemberExpression'
    && ['call', 'apply'].includes(memberPropertyName(node))
    && node.object?.type === 'MemberExpression'
  ) {
    return node.object;
  }
  return null;
}

function reflectApplyTarget(node) {
  if (
    node?.type === 'CallExpression'
    && node.callee?.type === 'MemberExpression'
    && node.callee.object?.type === 'Identifier'
    && node.callee.object.name === 'Reflect'
    && memberPropertyName(node.callee) === 'apply'
  ) {
    return node.arguments?.[0];
  }
  return null;
}

function callApplySpecifier(callee, args) {
  if (
    callee?.type !== 'MemberExpression'
    || !['call', 'apply'].includes(memberPropertyName(callee))
  ) {
    return '';
  }
  if (memberPropertyName(callee) === 'call') return literalString(args?.[1]);
  const applyArgs = args?.[1];
  if (applyArgs?.type !== 'ArrayExpression') return COMPUTED_VITEST_MODULE_MOCK;
  return literalString(applyArgs.elements?.[0]) || COMPUTED_VITEST_MODULE_MOCK;
}

function visit(node, visitor) {
  if (!node || typeof node !== 'object') return;
  visitor(node);
  for (const value of Object.values(node)) {
    if (Array.isArray(value)) {
      for (const child of value) visit(child, visitor);
    } else if (value && typeof value === 'object' && typeof value.type === 'string') {
      visit(value, visitor);
    }
  }
}

function collectImportSpecifier(node, specifiers) {
  switch (node.type) {
    case 'ImportDeclaration':
    case 'ExportNamedDeclaration':
    case 'ExportAllDeclaration':
      if (literalString(node.source)) specifiers.push(literalString(node.source));
      break;
    case 'ImportExpression':
      specifiers.push(literalString(node.source) || NON_LITERAL_DYNAMIC_IMPORT);
      break;
    case 'CallExpression': {
      const callee = node.callee;
      const isRequire = callee?.type === 'Identifier' && callee.name === 'require';
      const directVitest = vitestMockMember(callee);
      const callApplyTarget = unwrapVitestCallApply(callee);
      const callApplyVitest = vitestMockMember(callApplyTarget);
      const reflectTarget = reflectApplyTarget(node);
      const reflectVitest = vitestMockMember(reflectTarget);
      const directArg = literalString(node.arguments?.[0]);
      const callApplyArg = callApplySpecifier(callee, node.arguments);
      const reflectApplyArg = callApplySpecifier(
        { type: 'MemberExpression', property: { type: 'Identifier', name: 'apply' } },
        [null, node.arguments?.[2]],
      );
      if (isRequire && directArg) {
        specifiers.push(directArg);
      } else if (directVitest.known && directArg) {
        specifiers.push(directArg);
      } else if (callApplyVitest.known && callApplyArg && callApplyArg !== COMPUTED_VITEST_MODULE_MOCK) {
        specifiers.push(callApplyArg);
      } else if (reflectVitest.known && reflectApplyArg && reflectApplyArg !== COMPUTED_VITEST_MODULE_MOCK) {
        specifiers.push(reflectApplyArg);
      } else if (
        directVitest.unknownComputed
        || callApplyVitest.unknownComputed
        || reflectVitest.unknownComputed
        || (callApplyVitest.known && callApplyArg === COMPUTED_VITEST_MODULE_MOCK)
        || (reflectVitest.known && reflectApplyArg === COMPUTED_VITEST_MODULE_MOCK)
      ) {
        specifiers.push(COMPUTED_VITEST_MODULE_MOCK);
      }
      break;
    }
    default:
      break;
  }
}

function collectStaticImportSpecifier(node, specifiers) {
  if (node.type !== 'ImportDeclaration') return;
  if (literalString(node.source)) specifiers.push(literalString(node.source));
}

export function importSpecifiers(source) {
  const specifiers = [];
  visit(parseModule(source), (node) => collectImportSpecifier(node, specifiers));
  return specifiers;
}

export function staticImportSpecifiers(source) {
  const specifiers = [];
  visit(parseModule(source), (node) => collectStaticImportSpecifier(node, specifiers));
  return specifiers;
}

export function namedImportsFrom(source, matchesSpecifier) {
  const importedNames = [];
  visit(parseModule(source), (node) => {
    if (node.type !== 'ImportDeclaration') return;
    const sourceValue = literalString(node.source);
    if (!matchesSpecifier(sourceValue)) return;
    for (const specifier of node.specifiers ?? []) {
      if (specifier.type !== 'ImportSpecifier') continue;
      const imported = specifier.imported;
      if (imported?.type === 'Identifier') importedNames.push(imported.name);
      if (imported?.type === 'StringLiteral') importedNames.push(imported.value);
    }
  });
  return importedNames;
}
