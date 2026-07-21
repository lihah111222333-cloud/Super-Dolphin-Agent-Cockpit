import { parse as parseJavaScriptSource } from "@babel/parser";

const NESTED_PAYLOAD_SCOPE_NODE_TYPES = new Set([
  "ArrowFunctionExpression",
  "ClassMethod",
  "ClassPrivateMethod",
  "FunctionDeclaration",
  "FunctionExpression",
  "ObjectMethod",
]);

const CLASS_FIELD_NODE_TYPES = new Set([
  "ClassAccessorProperty",
  "ClassPrivateProperty",
  "ClassProperty",
]);

/**
 * Derive React payload builders from the executable callBackend call sites.
 * A method with more than one builder is ambiguous by definition: accepting it
 * would turn the audit into a guess, so the caller receives a hard failure.
 */
export function collectFrontendPayloadKeysFromSource(
  source,
  { sourcePath = "frontend payload source", methodValues = new Map(), requiredMethods = null } = {},
) {
  const ast = parseJavaScriptSource(source, {
    sourceType: "module",
    plugins: ["jsx"],
    errorRecovery: true,
  });
  const [parseError] = ast.errors ?? [];
  if (parseError) throw parseError;

  const declarations = collectTopLevelFunctions(ast);
  const buildersByMethod = collectCallBackendBuilders(
    ast,
    sourcePath,
    methodValues,
    requiredMethods,
  );
  const result = new Map();
  for (const [method, builderNames] of buildersByMethod) {
    if (builderNames.size !== 1) {
      throw new Error(
        `${sourcePath}: RPC_METHODS.${method} has ambiguous payload builders: ${[...builderNames].sort().join(", ")}`,
      );
    }
    const [builderName] = builderNames;
    const candidates = declarations.get(builderName) ?? [];
    if (candidates.length !== 1) {
      throw new Error(
        `${sourcePath}: ${builderName} must have exactly one top-level FunctionDeclaration; found ${candidates.length}`,
      );
    }
    result.set(methodValues.get(method) ?? method, extractConsumedPayloadKeys(candidates[0]));
  }
  assertRequiredMethodsDiscovered(result, requiredMethods, sourcePath);
  return result;
}

function collectTopLevelFunctions(ast) {
  const declarations = new Map();
  for (const statement of ast.program.body) {
    if (statement.type !== "FunctionDeclaration" || !statement.id?.name) continue;
    const existing = declarations.get(statement.id.name) ?? [];
    existing.push(statement);
    declarations.set(statement.id.name, existing);
  }
  return declarations;
}

function collectCallBackendBuilders(ast, sourcePath, methodValues, requiredMethods) {
  const buildersByMethod = new Map();
  traverseAst(ast, (node) => {
    if (
      node.type !== "CallExpression" ||
      node.callee.type !== "Identifier" ||
      node.callee.name !== "callBackend"
    )
      return;
    const [methodReference, payloadBuilder] = node.arguments;
    if (
      methodReference?.type !== "MemberExpression" ||
      methodReference.computed ||
      methodReference.object.type !== "Identifier" ||
      methodReference.object.name !== "RPC_METHODS" ||
      methodReference.property.type !== "Identifier"
    ) {
      return;
    }
    const method = methodReference.property.name;
    const resolvedMethod = methodValues.get(method) ?? method;
    if (requiredMethods && !requiredMethods.has(resolvedMethod)) return;
    if (payloadBuilder?.type !== "CallExpression" || payloadBuilder.callee.type !== "Identifier") {
      throw new Error(
        `${sourcePath}: RPC_METHODS.${method} must pass an IdentifierBuilder(...) payload to callBackend`,
      );
    }
    const builder = payloadBuilder.callee.name;
    const existing = buildersByMethod.get(method) ?? new Set();
    existing.add(builder);
    buildersByMethod.set(method, existing);
  });
  return buildersByMethod;
}

function assertRequiredMethodsDiscovered(discovered, requiredMethods, sourcePath) {
  if (!requiredMethods) return;
  const missing = [...requiredMethods].filter((method) => !discovered.has(method)).sort();
  if (missing.length > 0) {
    throw new Error(
      `${sourcePath}: required RPC payload builders were not discovered: ${missing.join(", ")}`,
    );
  }
}

function extractConsumedPayloadKeys(functionDeclaration) {
  const keys = [];
  traversePayloadBuilderRootScope(functionDeclaration, (node) => {
    if (node.type !== "CallExpression" || node.callee.type !== "Identifier") return;
    const [payload, keySource] = node.arguments;
    if (payload?.type !== "Identifier" || payload.name !== "unused") return;
    if (node.callee.name === "takePayloadField") {
      if (keySource?.type === "StringLiteral") keys.push(keySource.value);
      return;
    }
    if (node.callee.name !== "takePayloadFields" || keySource?.type !== "ArrayExpression") return;
    for (const element of keySource.elements) {
      if (element?.type === "StringLiteral") keys.push(element.value);
    }
  });
  return uniqueSorted(keys);
}

function traversePayloadBuilderRootScope(node, visit, isRootFunction = true) {
  if (!node || typeof node.type !== "string") return;
  if (!isRootFunction && NESTED_PAYLOAD_SCOPE_NODE_TYPES.has(node.type)) return;
  if (CLASS_FIELD_NODE_TYPES.has(node.type)) {
    visit(node);
    if (node.computed) traversePayloadBuilderRootScope(node.key, visit, false);
    if (node.static) traversePayloadBuilderRootScope(node.value, visit, false);
    return;
  }
  visit(node);
  for (const value of Object.values(node)) {
    if (!value) continue;
    if (Array.isArray(value)) {
      for (const child of value) traversePayloadBuilderRootScope(child, visit, false);
    } else if (typeof value.type === "string") {
      traversePayloadBuilderRootScope(value, visit, false);
    }
  }
}

function traverseAst(node, visit) {
  if (!node || typeof node.type !== "string") return;
  visit(node);
  for (const value of Object.values(node)) {
    if (Array.isArray(value)) {
      for (const child of value) traverseAst(child, visit);
    } else if (value && typeof value.type === "string") {
      traverseAst(value, visit);
    }
  }
}

function uniqueSorted(values) {
  return [...new Set(values)].sort((a, b) => a.localeCompare(b));
}
