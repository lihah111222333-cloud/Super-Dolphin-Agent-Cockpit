import { parse as parseJavaScriptSource } from "@babel/parser";

function findFrozenObjectExport(source, exportName) {
  let found = null;
  traverseAst(parseFrontendAst(source), (node) => {
    if (
      found ||
      node.type !== "ExportNamedDeclaration" ||
      node.declaration?.type !== "VariableDeclaration"
    ) {
      return;
    }
    for (const declarator of node.declaration.declarations) {
      if (declarator.id.type !== "Identifier" || declarator.id.name !== exportName) {
        continue;
      }
      found = unwrapObjectFreezeObject(declarator.init);
      if (!found) {
        throw new Error(`${exportName} must be assigned Object.freeze({...})`);
      }
    }
  });
  return found;
}

function unwrapObjectFreezeObject(node) {
  if (
    node?.type === "CallExpression" &&
    node.callee.type === "MemberExpression" &&
    node.callee.object.type === "Identifier" &&
    node.callee.object.name === "Object" &&
    node.callee.property.type === "Identifier" &&
    node.callee.property.name === "freeze" &&
    node.arguments[0]?.type === "ObjectExpression"
  ) {
    return node.arguments[0];
  }
  return null;
}

/**
 * @param {string} source
 * @param {{ errorRecovery?: boolean }} [options]
 */
function parseFrontendAst(source, options = {}) {
  return parseJavaScriptSource(source, {
    sourceType: "module",
    plugins: ["jsx", "typescript"],
    errorRecovery: options.errorRecovery ?? false,
  });
}

function traverseAst(node, visit) {
  if (!node || typeof node.type !== "string") return;
  visit(node);
  for (const value of Object.values(node)) {
    if (!value) continue;
    if (Array.isArray(value)) {
      for (const child of value) {
        traverseAst(child, visit);
      }
    } else if (typeof value.type === "string") {
      traverseAst(value, visit);
    }
  }
}

function objectPropertiesOnly(objectExpression, label) {
  return objectExpression.properties.map((property) => {
    if (property.type !== "ObjectProperty") {
      throw new Error(`${label} entries must be object properties`);
    }
    return property;
  });
}

function propertyKeyName(property) {
  if (property.key.type === "Identifier" && !property.computed) return property.key.name;
  if (property.key.type === "StringLiteral") return property.key.value;
  throw new Error("Object property key must be an identifier or string literal");
}

function stringLiteralValue(node, label) {
  if (node?.type !== "StringLiteral") {
    throw new Error(`${label} must be a string literal`);
  }
  return node.value;
}

export {
  findFrozenObjectExport,
  objectPropertiesOnly,
  parseFrontendAst,
  propertyKeyName,
  stringLiteralValue,
  traverseAst,
  unwrapObjectFreezeObject,
};
