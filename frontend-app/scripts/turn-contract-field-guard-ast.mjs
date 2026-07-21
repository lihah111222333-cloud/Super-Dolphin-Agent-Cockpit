import { parse } from "@babel/parser";

export function parseModule(source, filePath) {
  try {
    return parse(source, { sourceType: "module", plugins: ["jsx"] });
  } catch (error) {
    throw new Error(`parse ${filePath}: ${error.message}`);
  }
}

export function findFunction(ast, symbol, filePath) {
  const matches = namedProductionFunctions(ast).filter(
    (fn) => fn.symbol === symbol,
  );
  if (matches.length !== 1)
    throw new Error(
      `${filePath}:${symbol} resolved ${matches.length} production functions`,
    );
  return matches[0];
}

export function namedProductionFunctions(ast) {
  const functions = [];
  for (const statement of ast.program.body) {
    const declaration =
      statement.type === "ExportNamedDeclaration" ||
      statement.type === "ExportDefaultDeclaration"
        ? statement.declaration
        : statement;
    collectNamedDeclaration(declaration, functions);
  }
  return functions;
}

function collectNamedDeclaration(declaration, functions) {
  if (declaration?.type === "FunctionDeclaration" && declaration.id?.name) {
    functions.push({ symbol: declaration.id.name, body: declaration.body });
  } else if (declaration?.type === "VariableDeclaration") {
    for (const declarator of declaration.declarations) {
      if (declarator.id?.type === "Identifier")
        collectNamedValue(declarator.id.name, declarator.init, functions);
    }
  } else if (declaration?.type === "ClassDeclaration" && declaration.id?.name) {
    collectNamedClassMethods(declaration.id.name, declaration, functions);
  }
}

function collectNamedValue(owner, value, functions) {
  if (isFunctionNode(value))
    functions.push({ symbol: owner, body: value.body });
  else if (value?.type === "ObjectExpression")
    collectNamedObjectMethods(owner, value, functions);
  else if (value?.type === "CallExpression") {
    const functionArguments = value.arguments.filter(isFunctionNode);
    if (functionArguments.length === 1)
      functions.push({ symbol: owner, body: functionArguments[0].body });
  } else if (value?.type === "ClassExpression")
    collectNamedClassMethods(owner, value, functions);
}

function collectNamedObjectMethods(owner, object, functions) {
  for (const property of object.properties) {
    const name = staticPropertyName(property);
    if (!name) continue;
    const symbol = `${owner}.${name}`;
    if (property.type === "ObjectMethod")
      functions.push({ symbol, body: property.body });
    else if (property.type === "ObjectProperty")
      collectNamedValue(symbol, property.value, functions);
  }
}

function collectNamedClassMethods(owner, classNode, functions) {
  for (const member of classNode.body.body) {
    const name = staticPropertyName(member);
    if (!name) continue;
    const symbol = `${owner}.${name}`;
    if (member.type === "ClassMethod")
      functions.push({ symbol, body: member.body });
    else if (member.type === "ClassProperty")
      collectNamedValue(symbol, member.value, functions);
  }
}

export function staticPropertyName(property) {
  if (property.computed) return "";
  if (property.key?.type === "Identifier") return property.key.name;
  return stringLiteralValue(property.key);
}

export function assertUniqueProductionSymbols(functions, filePath) {
  const seen = new Set();
  for (const fn of functions) {
    if (seen.has(fn.symbol))
      throw new Error(
        `${filePath}:${fn.symbol} resolved multiple production functions`,
      );
    seen.add(fn.symbol);
  }
}

export function walkFunctionBody(fn, visitor) {
  walkNode(fn.body, visitor, true);
}

export function walkNode(
  node,
  visitor,
  skipNestedFunctions = false,
  root = true,
) {
  if (!node || typeof node !== "object") return;
  visitor(node);
  if (!root && skipNestedFunctions && isFunctionNode(node)) return;
  for (const [key, value] of Object.entries(node)) {
    if (key === "loc" || key === "start" || key === "end") continue;
    if (Array.isArray(value))
      value.forEach((item) =>
        walkNode(item, visitor, skipNestedFunctions, false),
      );
    else if (
      value &&
      typeof value === "object" &&
      typeof value.type === "string"
    )
      walkNode(value, visitor, skipNestedFunctions, false);
  }
}

export function walkNodeWithParent(node, visitor, parent = null) {
  if (!node || typeof node !== "object") return;
  visitor(node, parent);
  for (const [key, value] of Object.entries(node)) {
    if (key === "loc" || key === "start" || key === "end") continue;
    if (Array.isArray(value))
      value.forEach((item) => walkNodeWithParent(item, visitor, node));
    else if (
      value &&
      typeof value === "object" &&
      typeof value.type === "string"
    )
      walkNodeWithParent(value, visitor, node);
  }
}

export function isFunctionNode(node) {
  return (
    node?.type === "FunctionDeclaration" ||
    node?.type === "FunctionExpression" ||
    node?.type === "ArrowFunctionExpression"
  );
}

export function calleeName(callee) {
  if (callee?.type === "Identifier") return callee.name;
  if (
    callee?.type === "MemberExpression" &&
    !callee.computed &&
    callee.property?.type === "Identifier"
  )
    return callee.property.name;
  return "";
}

export function stringLiteralValue(node) {
  return node?.type === "StringLiteral" ? node.value : "";
}

export function memberPropertyName(member) {
  if (!member.computed && member.property?.type === "Identifier")
    return member.property.name;
  return stringLiteralValue(member.property);
}
