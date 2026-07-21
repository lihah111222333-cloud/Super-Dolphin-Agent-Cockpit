import { parse } from '@babel/parser';

export function parseModule(source, filePath) {
  try {
    return parse(source, { sourceType: 'module', plugins: ['jsx', 'typescript', 'dynamicImport'] });
  } catch (error) {
    throw new Error(`parse ${filePath}: ${error.message}`);
  }
}

export function parseModuleCached(source, filePath, cache) {
  const cached = cache.get(filePath);
  if (cached?.source === source) return cached.ast;
  const ast = parseModule(source, filePath);
  cache.set(filePath, { source, ast });
  return ast;
}

export function walkNode(node, ancestors, visitor) {
  if (!node || typeof node !== 'object') return;
  visitor(node, ancestors);
  const nextAncestors = [...ancestors, node];
  for (const [key, value] of Object.entries(node)) {
    if (['loc', 'start', 'end', 'extra'].includes(key)) continue;
    if (Array.isArray(value)) value.forEach((child) => walkNode(child, nextAncestors, visitor));
    else if (value && typeof value === 'object' && typeof value.type === 'string') {
      walkNode(value, nextAncestors, visitor);
    }
  }
}

export function writtenProperty(node, stringBindings) {
  if (node.type === 'ObjectProperty') {
    const name = staticPropertyName(node.key, node.computed, stringBindings);
    return name ? {
      name,
      operation: 'object-property',
      value: expressionShape(node.value, stringBindings),
    } : null;
  }
  if ((node.type === 'AssignmentExpression' || node.type === 'UpdateExpression')
      && isMemberExpression(node.type === 'AssignmentExpression' ? node.left : node.argument)) {
    const member = node.type === 'AssignmentExpression' ? node.left : node.argument;
    const name = memberPropertyName(member, stringBindings);
    const value = node.type === 'AssignmentExpression'
      ? expressionShape(node.right, stringBindings)
      : `${node.operator}update`;
    return name ? { name, operation: 'member-mutation', value } : null;
  }
  if (node.type === 'UnaryExpression' && node.operator === 'delete' && isMemberExpression(node.argument)) {
    const name = memberPropertyName(node.argument, stringBindings);
    return name ? { name, operation: 'member-mutation', value: 'delete' } : null;
  }
  if (node.type !== 'CallExpression') return null;
  const callee = dottedName(node.callee, stringBindings);
  if (callee === 'Object.defineProperty' || callee === 'Reflect.defineProperty') {
    const name = staticStringValue(node.arguments[1], stringBindings);
    const descriptor = node.arguments[2];
    const valueNode = descriptor?.type === 'ObjectExpression'
      ? descriptor.properties.find((property) => (
        property.type === 'ObjectProperty'
        && staticPropertyName(property.key, property.computed, stringBindings) === 'value'
      ))?.value
      : null;
    return name ? {
      name,
      operation: 'define-property',
      value: valueNode ? expressionShape(valueNode, stringBindings) : 'descriptor',
    } : null;
  }
  if (callee === 'Reflect.set') {
    const name = staticStringValue(node.arguments[1], stringBindings);
    return name ? {
      name,
      operation: 'reflect-set',
      value: expressionShape(node.arguments[2], stringBindings),
    } : null;
  }
  return null;
}

export function readProperty(node, ancestors, stringBindings) {
  if (isMemberExpression(node)) {
    if (isWriteTarget(node, ancestors)) return null;
    const name = memberPropertyName(node, stringBindings);
    return name ? { name, operation: 'member-read' } : null;
  }
  if (node.type !== 'ObjectProperty' || ancestors.at(-1)?.type !== 'ObjectPattern') return null;
  const name = staticPropertyName(node.key, node.computed, stringBindings);
  return name ? { name, operation: 'destructure-read' } : null;
}

export function enclosingSymbol(ancestors) {
  for (let index = ancestors.length - 1; index >= 0; index -= 1) {
    const node = ancestors[index];
    if (node.type === 'FunctionDeclaration' && node.id?.name) return node.id.name;
    if (node.type === 'FunctionExpression' && node.id?.name) return node.id.name;
    if (node.type === 'ObjectMethod' || node.type === 'ClassMethod') {
      return propertyName(node.key) || '<anonymous>';
    }
    if (node.type !== 'FunctionExpression' && node.type !== 'ArrowFunctionExpression') continue;
    const parent = ancestors[index - 1];
    if (parent?.type === 'VariableDeclarator') return propertyName(parent.id) || '<anonymous>';
    if (parent?.type === 'ObjectProperty' || parent?.type === 'ClassProperty') {
      return propertyName(parent.key) || '<anonymous>';
    }
    if (parent?.type === 'AssignmentExpression' && isMemberExpression(parent.left)) {
      return memberPropertyName(parent.left) || '<anonymous>';
    }
  }
  return '<module>';
}

export function dottedName(node, stringBindings = new Map()) {
  if (node?.type === 'Identifier') return node.name;
  if (!isMemberExpression(node)) return '';
  const object = dottedName(node.object, stringBindings);
  const property = memberPropertyName(node, stringBindings);
  return object && property ? `${object}.${property}` : '';
}

export function stringValue(node) {
  if (node?.type === 'StringLiteral' || node?.type === 'Literal') return node.value;
  if (node?.type === 'TemplateLiteral' && node.expressions.length === 0) return node.quasis[0]?.value?.cooked;
  return '';
}

export function staticStringBindings(ast) {
  const scopeByNode = new WeakMap();
  walkNode(ast, [], (node, ancestors) => {
    if (!isScopeNode(node)) return;
    scopeByNode.set(node, {
      bindings: new Map(),
      node,
      parent: nearestScope(ancestors, scopeByNode),
    });
  });

  walkNode(ast, [], (node, ancestors) => {
    if (node.type === 'VariableDeclarator') {
      const declaration = ancestors.at(-1);
      const scope = declaration?.kind === 'var'
        ? nearestFunctionScope(ancestors, scopeByNode)
        : nearestScope(ancestors, scopeByNode);
      const names = bindingNames(node.id);
      for (const name of names) {
        const value = names.length === 1 && node.id.type === 'Identifier'
          ? stringValue(node.init)
          : '';
        declareBinding(scope, name, { kind: declaration?.kind || 'unknown', value });
      }
      return;
    }
    if (isFunctionNode(node)) {
      const functionScope = scopeByNode.get(node);
      for (const parameter of node.params || []) {
        for (const name of bindingNames(parameter)) declareBinding(functionScope, name);
      }
      if (node.type === 'FunctionDeclaration' && node.id?.name) {
        declareBinding(nearestScope(ancestors, scopeByNode), node.id.name);
      } else if (node.id?.name) {
        declareBinding(functionScope, node.id.name);
      }
      return;
    }
    if (node.type === 'ClassDeclaration' && node.id?.name) {
      declareBinding(nearestScope(ancestors, scopeByNode), node.id.name);
      return;
    }
    if (node.type === 'CatchClause') {
      const catchScope = scopeByNode.get(node);
      for (const name of bindingNames(node.param)) declareBinding(catchScope, name);
      return;
    }
    if (node.type === 'ImportDeclaration') {
      const scope = nearestScope(ancestors, scopeByNode);
      for (const specifier of node.specifiers || []) {
        if (specifier.local?.name) declareBinding(scope, specifier.local.name);
      }
    }
  });

  walkNode(ast, [], (node, ancestors) => {
    let target = null;
    if (node.type === 'AssignmentExpression') target = node.left;
    else if (node.type === 'UpdateExpression') target = node.argument;
    else if (node.type === 'ForInStatement' || node.type === 'ForOfStatement') target = node.left;
    if (!target || target.type === 'VariableDeclaration') return;
    const scope = nearestScope(ancestors, scopeByNode);
    for (const name of bindingNames(target)) {
      const binding = resolveBinding(scope, name);
      if (binding) binding.mutated = true;
    }
  });

  const valuesByIdentifier = new WeakMap();
  walkNode(ast, [], (node, ancestors) => {
    if (node.type !== 'Identifier') return;
    const binding = resolveBinding(nearestScope(ancestors, scopeByNode), node.name);
    if (binding?.value && !binding.invalid && !binding.mutated) {
      valuesByIdentifier.set(node, binding.value);
    }
  });
  return valuesByIdentifier;
}

function isMemberExpression(node) {
  return node?.type === 'MemberExpression' || node?.type === 'OptionalMemberExpression';
}

function memberPropertyName(node, stringBindings = new Map()) {
  return staticPropertyName(node.property, node.computed, stringBindings);
}

function propertyName(node) {
  if (node?.type === 'Identifier') return node.name;
  return stringValue(node);
}

function staticStringValue(node, bindings) {
  const direct = stringValue(node);
  if (direct) return direct;
  if (node?.type === 'Identifier') return bindings.get(node) || '';
  return '';
}

function staticPropertyName(node, computed, bindings) {
  if (!computed && node?.type === 'Identifier') return node.name;
  return staticStringValue(node, bindings);
}

function isWriteTarget(node, ancestors) {
  const parent = ancestors.at(-1);
  if (!parent) return false;
  if (parent.type === 'AssignmentExpression' && parent.left === node) return true;
  if (parent.type === 'UpdateExpression' && parent.argument === node) return true;
  return parent.type === 'UnaryExpression' && parent.operator === 'delete' && parent.argument === node;
}

function isFunctionNode(node) {
  return node?.type === 'FunctionDeclaration'
    || node?.type === 'FunctionExpression'
    || node?.type === 'ArrowFunctionExpression'
    || node?.type === 'ObjectMethod'
    || node?.type === 'ClassMethod'
    || node?.type === 'ClassPrivateMethod';
}

function isScopeNode(node) {
  return node?.type === 'Program'
    || node?.type === 'BlockStatement'
    || node?.type === 'CatchClause'
    || node?.type === 'ForStatement'
    || node?.type === 'ForInStatement'
    || node?.type === 'ForOfStatement'
    || node?.type === 'SwitchStatement'
    || node?.type === 'StaticBlock'
    || isFunctionNode(node);
}

function nearestScope(ancestors, scopeByNode) {
  for (let index = ancestors.length - 1; index >= 0; index -= 1) {
    const scope = scopeByNode.get(ancestors[index]);
    if (scope) return scope;
  }
  return null;
}

function nearestFunctionScope(ancestors, scopeByNode) {
  for (let index = ancestors.length - 1; index >= 0; index -= 1) {
    const node = ancestors[index];
    if (node.type !== 'Program' && !isFunctionNode(node)) continue;
    const scope = scopeByNode.get(node);
    if (scope) return scope;
  }
  return null;
}

function bindingNames(pattern) {
  if (!pattern) return [];
  if (pattern.type === 'Identifier') return [pattern.name];
  if (pattern.type === 'RestElement') return bindingNames(pattern.argument);
  if (pattern.type === 'AssignmentPattern') return bindingNames(pattern.left);
  if (pattern.type === 'ArrayPattern') return pattern.elements.flatMap(bindingNames);
  if (pattern.type === 'ObjectPattern') {
    return pattern.properties.flatMap((property) => (
      property.type === 'RestElement' ? bindingNames(property.argument) : bindingNames(property.value)
    ));
  }
  return [];
}

function declareBinding(scope, name, { kind = 'unknown', value = '' } = {}) {
  if (!scope || !name) return;
  const existing = scope.bindings.get(name);
  if (existing) {
    existing.invalid = true;
    return;
  }
  scope.bindings.set(name, { invalid: false, kind, mutated: false, value });
}

function resolveBinding(scope, name) {
  for (let current = scope; current; current = current.parent) {
    const binding = current.bindings.get(name);
    if (binding) return binding;
  }
  return null;
}

function expressionShape(node, stringBindings) {
  if (!node) return 'missing';
  if (node.type === 'Identifier') return node.name;
  if (isMemberExpression(node)) return dottedName(node, stringBindings) || 'computed-member';
  if (node.type === 'CallExpression') {
    const callee = dottedName(node.callee, stringBindings) || node.callee?.type || 'call';
    return `${callee}(${node.arguments.map((argument) => expressionShape(argument, stringBindings)).join(',')})`;
  }
  if (node.type === 'ArrayExpression') return 'array';
  if (node.type === 'ObjectExpression') return 'object';
  if (node.type === 'StringLiteral' || node.type === 'NumericLiteral' || node.type === 'BooleanLiteral') {
    return JSON.stringify(node.value);
  }
  if (node.type === 'NullLiteral') return 'null';
  if (node.type === 'UnaryExpression') return `${node.operator}${expressionShape(node.argument, stringBindings)}`;
  return node.type;
}
