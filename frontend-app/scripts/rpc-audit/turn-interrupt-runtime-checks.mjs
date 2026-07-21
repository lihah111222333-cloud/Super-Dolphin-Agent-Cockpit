import * as audit from "../rpc-contract-audit.mjs";
import { countRuntimeProofBindings } from "./turn-interrupt-runtime-evidence.mjs";

function isExactRuntimePayloadDeclaration(statement) {
  if (
    statement?.type !== "VariableDeclaration" ||
    statement.kind !== "const" ||
    statement.declarations.length !== 1
  )
    return false;
  const declaration = statement.declarations[0];
  const call = declaration.init;
  const names = [
    "action",
    "activeThreadInterruptTarget",
    "activeTurnTarget",
    "cleanObject",
    "createRequestId",
    "currentState",
    "cwd",
    "notifyAction",
    "threadId",
  ];
  if (
    declaration.id.type !== "Identifier" ||
    declaration.id.name !== "payload" ||
    call?.type !== "CallExpression" ||
    call.callee.type !== "Identifier" ||
    call.callee.name !== "threadActionPayload" ||
    call.arguments.length !== 1 ||
    call.arguments[0].type !== "ObjectExpression" ||
    call.arguments[0].properties.length !== names.length
  )
    return false;
  return names.every((name) => {
    const property = call.arguments[0].properties.find(
      (candidate) =>
        candidate.type === "ObjectProperty" &&
        !candidate.computed &&
        audit.staticPropertyKeyName(candidate) === name,
    );
    return property?.value.type === "Identifier" && property.value.name === name;
  });
}

function isExactRuntimePayloadFailureGuard(statement) {
  if (
    statement?.type !== "IfStatement" ||
    statement.alternate ||
    statement.test.type !== "UnaryExpression" ||
    statement.test.operator !== "!" ||
    statement.test.argument.type !== "Identifier" ||
    statement.test.argument.name !== "payload" ||
    statement.consequent.type !== "ReturnStatement"
  )
    return false;
  const node = statement.consequent.argument;
  if (node?.type !== "ObjectExpression" || node.properties.length !== 3) return false;
  const okProperty = node.properties.find(
    (property) =>
      property.type === "ObjectProperty" &&
      !property.computed &&
      audit.staticPropertyKeyName(property) === "ok",
  );
  const threadProperty = node.properties.find(
    (property) =>
      property.type === "ObjectProperty" &&
      !property.computed &&
      audit.staticPropertyKeyName(property) === "threadId",
  );
  const resultProperty = node.properties.find(
    (property) =>
      property.type === "ObjectProperty" &&
      !property.computed &&
      audit.staticPropertyKeyName(property) === "result",
  );
  return (
    okProperty?.value.type === "BooleanLiteral" &&
    okProperty.value.value === false &&
    threadProperty?.value.type === "Identifier" &&
    threadProperty.value.name === "threadId" &&
    resultProperty?.value.type === "NullLiteral"
  );
}

function isExactRuntimeSuccessStatement(statement, wrapper) {
  if (countRuntimeProofBindings(wrapper.body, "notifyAction") !== 0) return false;
  const call = statement?.type === "ExpressionStatement" ? statement.expression : null;
  return (
    call?.type === "CallExpression" &&
    call.callee.type === "Identifier" &&
    call.callee.name === "notifyAction" &&
    call.arguments.length >= 2 &&
    call.arguments[1].type === "StringLiteral" &&
    call.arguments[1].value === "success"
  );
}

function isExactHandlerFailureGate(statement, handlerName) {
  if (
    statement?.type !== "IfStatement" ||
    statement.alternate ||
    statement.test.type !== "CallExpression" ||
    statement.test.callee.type !== "Identifier" ||
    statement.test.callee.name !== handlerName ||
    statement.test.arguments.length !== 1 ||
    !isExactRuntimeHandlerArgument(statement.test.arguments[0])
  )
    return false;
  const returned =
    statement.consequent.type === "ReturnStatement"
      ? statement.consequent
      : statement.consequent.type === "BlockStatement" && statement.consequent.body.length === 1
        ? statement.consequent.body[0]
        : null;
  return isExactRuntimeOutcomeReturn(returned, false);
}

function isExactRuntimeHandlerArgument(node) {
  const names = ["action", "addWarning", "notifyAction", "result", "threadId"];
  if (node?.type !== "ObjectExpression" || node.properties.length !== names.length) return false;
  return names.every((name) => {
    const property = node.properties.find(
      (candidate) =>
        candidate.type === "ObjectProperty" &&
        !candidate.computed &&
        audit.staticPropertyKeyName(candidate) === name,
    );
    return property?.value.type === "Identifier" && property.value.name === name;
  });
}

function isExactRuntimeOutcomeReturn(statement, ok) {
  const node = statement?.type === "ReturnStatement" ? statement.argument : null;
  if (node?.type !== "ObjectExpression" || node.properties.length !== 3) return false;
  const okProperty = node.properties.find(
    (property) =>
      property.type === "ObjectProperty" &&
      !property.computed &&
      audit.staticPropertyKeyName(property) === "ok",
  );
  const threadProperty = node.properties.find(
    (property) =>
      property.type === "ObjectProperty" &&
      !property.computed &&
      audit.staticPropertyKeyName(property) === "threadId",
  );
  const resultProperty = node.properties.find(
    (property) =>
      property.type === "ObjectProperty" &&
      !property.computed &&
      audit.staticPropertyKeyName(property) === "result",
  );
  return (
    okProperty?.value.type === "BooleanLiteral" &&
    okProperty.value.value === ok &&
    threadProperty?.value.type === "Identifier" &&
    threadProperty.value.name === "threadId" &&
    resultProperty?.value.type === "Identifier" &&
    resultProperty.value.name === "result"
  );
}

function consumerPassesFacadeResultToHandler(
  ast,
  consumerSymbol,
  facadeCall,
  consumerPath,
  handlerLocator,
) {
  if (!facadeCall) return false;
  if (consumerSymbol.body?.type !== "BlockStatement") return false;
  const body = consumerSymbol.body;
  let resultBinding = null;
  let resultStatementIndex = -1;
  audit.walkAstWithAncestors(body, (node, ancestors) => {
    if (node !== facadeCall) return;
    const awaited = ancestors.at(-1);
    const declarator = ancestors.at(-2);
    const declaration = ancestors.at(-3);
    const statementParent = ancestors.at(-4);
    if (
      awaited?.type === "AwaitExpression" &&
      declarator?.type === "VariableDeclarator" &&
      declarator.id.type === "Identifier" &&
      declaration?.type === "VariableDeclaration" &&
      declaration.kind === "const" &&
      statementParent === body
    ) {
      resultBinding = declarator;
      resultStatementIndex = body.body.indexOf(declaration);
    }
  });
  if (!resultBinding || resultStatementIndex < 0) return false;
  const handlerAliases = new Set();
  for (const statement of ast.program.body) {
    if (
      statement.type !== "ImportDeclaration" ||
      !audit.moduleSpecifierResolvesTo(consumerPath, statement.source.value, handlerLocator.path)
    )
      continue;
    for (const specifier of statement.specifiers) {
      if (
        specifier.type === "ImportSpecifier" &&
        audit.moduleExportName(specifier.imported) === handlerLocator.symbol
      )
        handlerAliases.add(specifier.local.name);
    }
  }
  if (handlerAliases.size !== 1) return false;
  let matched = false;
  audit.walkAstWithAncestors(body, (node, ancestors) => {
    if (
      node.type !== "CallExpression" ||
      node.start <= facadeCall.end ||
      node.callee.type !== "Identifier" ||
      !handlerAliases.has(node.callee.name) ||
      audit.bindingShadowsNameAt([consumerSymbol, ...ancestors], node.callee.name) ||
      node.arguments.length !== 1 ||
      node.arguments[0].type !== "Identifier" ||
      node.arguments[0].name !== resultBinding.id.name
    )
      return;
    const parent = ancestors.at(-1);
    const statementParent = ancestors.at(-2);
    const handlerStatementIndex =
      parent?.type === "ReturnStatement" && statementParent === body
        ? body.body.indexOf(parent)
        : -1;
    if (handlerStatementIndex > resultStatementIndex) {
      matched = true;
    }
  });
  return matched;
}

export {
  isExactRuntimePayloadDeclaration,
  isExactRuntimePayloadFailureGuard,
  isExactRuntimeSuccessStatement,
  isExactHandlerFailureGate,
  isExactRuntimeHandlerArgument,
  isExactRuntimeOutcomeReturn,
  consumerPassesFacadeResultToHandler,
};
