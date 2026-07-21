import * as audit from "../rpc-contract-audit.mjs";
import {
  safeParseFailureDominates,
  safeParseImplementationIsProven,
} from "./source-index-shape.mjs";

function resolveLocalSchemaMethod(ast, schemaName, methodName) {
  for (const statement of ast.program.body) {
    const declaration =
      statement.type === "ExportNamedDeclaration" ? statement.declaration : statement;
    if (declaration?.type !== "VariableDeclaration") continue;
    for (const item of declaration.declarations) {
      if (
        item.id.type !== "Identifier" ||
        item.id.name !== schemaName ||
        item.init?.type !== "ObjectExpression"
      )
        continue;
      return (
        item.init.properties.find(
          (property) =>
            property.type === "ObjectMethod" &&
            audit.staticPropertyKeyName(property) === methodName,
        ) ?? null
      );
    }
  }
  return null;
}

function hasExactGenericInterruptFallback(consequent) {
  if (consequent?.type !== "BlockStatement") return false;
  const [notice, warning, returned] = consequent.body.slice(-3);
  const noticeCall = notice?.type === "ExpressionStatement" ? notice.expression : null;
  const warningCall = warning?.type === "ExpressionStatement" ? warning.expression : null;
  return (
    noticeCall?.type === "CallExpression" &&
    noticeCall.callee.type === "Identifier" &&
    noticeCall.callee.name === "notifyAction" &&
    noticeCall.arguments.length === 3 &&
    noticeCall.arguments[0].type === "StringLiteral" &&
    noticeCall.arguments[0].value === "中断当前执行失败，请重试。" &&
    noticeCall.arguments[1].type === "StringLiteral" &&
    noticeCall.arguments[1].value === "warning" &&
    audit.isExactThreadIdObject(noticeCall.arguments[2], false) &&
    warningCall?.type === "CallExpression" &&
    warningCall.callee.type === "Identifier" &&
    warningCall.callee.name === "addWarning" &&
    warningCall.arguments.length === 3 &&
    warningCall.arguments[0].type === "StringLiteral" &&
    warningCall.arguments[0].value === "warn" &&
    isExactActionFailedTemplate(warningCall.arguments[1]) &&
    isExactGenericWarningFields(warningCall.arguments[2]) &&
    returned?.type === "ReturnStatement" &&
    returned.argument?.type === "BooleanLiteral" &&
    returned.argument.value === true
  );
}

function isExactActionFailedTemplate(node) {
  return (
    node?.type === "TemplateLiteral" &&
    node.expressions.length === 1 &&
    node.expressions[0].type === "Identifier" &&
    node.expressions[0].name === "action" &&
    node.quasis.length === 2 &&
    node.quasis[0].value.cooked === "" &&
    node.quasis[1].value.cooked === ".failed"
  );
}

function isExactGenericWarningFields(node) {
  if (node?.type !== "ObjectExpression" || node.properties.length !== 2) return false;
  const threadId = node.properties.find(
    (property) =>
      property.type === "ObjectProperty" && audit.staticPropertyKeyName(property) === "threadId",
  );
  const error = node.properties.find(
    (property) =>
      property.type === "ObjectProperty" && audit.staticPropertyKeyName(property) === "error",
  );
  return (
    threadId?.value.type === "Identifier" &&
    threadId.value.name === "threadId" &&
    error?.value.type === "StringLiteral" &&
    error.value.value === "action failure; see Health diagnostic ID"
  );
}

function handlerDirectlyInspectsEnvelope(
  handlerSymbol,
  rpcMethod = "",
  ast = null,
  upstreamValidated = false,
) {
  const parameter =
    handlerSymbol.params?.length === 1 && handlerSymbol.params[0].type === "Identifier"
      ? handlerSymbol.params[0].name
      : "";
  const statements = handlerSymbol.body?.type === "BlockStatement" ? handlerSymbol.body.body : [];
  if (!parameter) return false;
  const destructured = new Set();
  for (const statement of statements) {
    if (statement.type !== "VariableDeclaration") continue;
    for (const item of statement.declarations) {
      if (
        item.init?.type !== "Identifier" ||
        item.init.name !== parameter ||
        item.id.type !== "ObjectPattern"
      )
        continue;
      for (const property of item.id.properties) {
        if (property.type === "ObjectProperty" && property.value.type === "Identifier")
          destructured.add(property.value.name);
      }
    }
  }
  if (destructured.has("action") && destructured.has("result")) {
    return statements.some((statement) => {
      if (statement.type !== "IfStatement" || isAlwaysFalseExpression(statement.test)) return false;
      if (!isExactInterruptFailurePredicate(statement.test, rpcMethod)) return false;
      let validatesRawEnvelope = false;
      audit.walkAstWithAncestors(statement.consequent, (node, ancestors) => {
        if (ancestors.some((ancestor) => audit.isFunctionNode(ancestor))) return;
        if (
          node.type === "CallExpression" &&
          node.callee.type === "Identifier" &&
          node.arguments.length === 1 &&
          node.arguments[0].type === "Identifier" &&
          node.arguments[0].name === "result" &&
          moduleHelperReturnsResultError(ast, node.callee.name)
        )
          validatesRawEnvelope = true;
      });
      return (
        (upstreamValidated || validatesRawEnvelope) &&
        hasExactGenericInterruptFallback(statement.consequent)
      );
    });
  }
  return statements.some((statement) => {
    if (statement.type !== "IfStatement" || isAlwaysFalseExpression(statement.test)) return false;
    let outcomeInspected = false;
    audit.traverseAst(statement.test, (node) => {
      if (
        node.type === "MemberExpression" &&
        !node.computed &&
        node.object.type === "Identifier" &&
        node.object.name === parameter &&
        node.property.type === "Identifier" &&
        (node.property.name === "ok" || node.property.name === "error")
      )
        outcomeInspected = true;
    });
    if (!outcomeInspected) return false;
    let behavior = false;
    audit.walkAstWithAncestors(statement.consequent, (node, ancestors) => {
      if (ancestors.some((ancestor) => audit.isFunctionNode(ancestor))) return;
      if (node.type === "ThrowStatement" || node.type === "ReturnStatement") behavior = true;
      if (
        node.type === "CallExpression" &&
        node.callee.type === "MemberExpression" &&
        !node.callee.computed &&
        node.callee.object.type === "Identifier" &&
        node.callee.object.name === "console" &&
        node.callee.property.type === "Identifier" &&
        node.callee.property.name === "warn"
      )
        behavior = true;
    });
    return behavior;
  });
}

function isExactInterruptFailurePredicate(node, rpcMethod) {
  if (node?.type !== "LogicalExpression" || node.operator !== "&&") return false;
  const isActionMatch = (candidate) =>
    candidate?.type === "BinaryExpression" &&
    candidate.operator === "===" &&
    ((candidate.left.type === "Identifier" &&
      candidate.left.name === "action" &&
      candidate.right.type === "StringLiteral" &&
      candidate.right.value === rpcMethod) ||
      (candidate.right.type === "Identifier" &&
        candidate.right.name === "action" &&
        candidate.left.type === "StringLiteral" &&
        candidate.left.value === rpcMethod));
  const isFailureMatch = (candidate) =>
    candidate?.type === "BinaryExpression" &&
    candidate.operator === "===" &&
    ((candidate.left.type === "OptionalMemberExpression" &&
      candidate.left.object.type === "Identifier" &&
      candidate.left.object.name === "result" &&
      candidate.left.property.type === "Identifier" &&
      candidate.left.property.name === "ok" &&
      candidate.right.type === "BooleanLiteral" &&
      candidate.right.value === false) ||
      (candidate.right.type === "OptionalMemberExpression" &&
        candidate.right.object.type === "Identifier" &&
        candidate.right.object.name === "result" &&
        candidate.right.property.type === "Identifier" &&
        candidate.right.property.name === "ok" &&
        candidate.left.type === "BooleanLiteral" &&
        candidate.left.value === false));
  return (
    (isActionMatch(node.left) && isFailureMatch(node.right)) ||
    (isFailureMatch(node.left) && isActionMatch(node.right))
  );
}

function moduleHelperReturnsResultError(ast, helperName) {
  if (!ast) return false;
  const helper = audit.findModuleLevelSymbol(ast, helperName);
  if (
    !helper ||
    helper.params?.length !== 1 ||
    helper.params[0].type !== "Identifier" ||
    helper.body?.type !== "BlockStatement" ||
    helper.body.body.length !== 2
  )
    return false;
  const parameter = helper.params[0].name;
  const [loop, failure] = helper.body.body;
  if (
    loop.type !== "ForOfStatement" ||
    loop.await ||
    loop.right.type !== "ArrayExpression" ||
    loop.left.type !== "VariableDeclaration" ||
    loop.left.kind !== "const" ||
    loop.left.declarations.length !== 1 ||
    loop.left.declarations[0].id.type !== "Identifier" ||
    loop.left.declarations[0].init !== null ||
    loop.body.type !== "BlockStatement" ||
    loop.body.body.length !== 2
  )
    return false;
  const allowedFields = ["error", "message", "reason", "status", "mode"];
  if (
    loop.right.elements.length !== allowedFields.length ||
    !loop.right.elements.every(
      (element, index) =>
        element?.type === "OptionalMemberExpression" &&
        element.object.type === "Identifier" &&
        element.object.name === parameter &&
        element.property.type === "Identifier" &&
        element.property.name === allowedFields[index],
    )
  )
    return false;
  const valueName = loop.left.declarations[0].id.name;
  const [messageDeclaration, messageReturn] = loop.body.body;
  if (
    messageDeclaration.type !== "VariableDeclaration" ||
    messageDeclaration.kind !== "const" ||
    messageDeclaration.declarations.length !== 1
  )
    return false;
  const messageItem = messageDeclaration.declarations[0];
  if (
    messageItem.id.type !== "Identifier" ||
    messageItem.init?.type !== "CallExpression" ||
    messageItem.init.callee.type !== "Identifier" ||
    messageItem.init.callee.name !== "normalizeOptionalTextField" ||
    messageItem.init.arguments.length !== 1 ||
    messageItem.init.arguments[0].type !== "Identifier" ||
    messageItem.init.arguments[0].name !== valueName
  )
    return false;
  const messageName = messageItem.id.name;
  if (
    messageReturn.type !== "IfStatement" ||
    messageReturn.alternate !== null ||
    messageReturn.test.type !== "Identifier" ||
    messageReturn.test.name !== messageName ||
    messageReturn.consequent.type !== "ReturnStatement" ||
    messageReturn.consequent.argument?.type !== "Identifier" ||
    messageReturn.consequent.argument.name !== messageName
  )
    return false;
  return (
    failure.type === "ThrowStatement" &&
    failure.argument.type === "NewExpression" &&
    failure.argument.callee.type === "Identifier" &&
    failure.argument.callee.name === "Error" &&
    failure.argument.arguments.length === 1 &&
    failure.argument.arguments[0].type === "StringLiteral" &&
    failure.argument.arguments[0].value === "thread.interrupt ok:false response message is required"
  );
}

function hasExecutableShapeNarrowing(symbolNode, ast = null) {
  const statements = symbolNode.body?.type === "BlockStatement" ? symbolNode.body.body : [];
  const taintedNames = new Set(
    (symbolNode.params ?? [])
      .filter((parameter) => parameter.type === "Identifier")
      .map((parameter) => parameter.name),
  );
  for (const statement of statements) {
    if (statement.type === "VariableDeclaration") {
      for (const declarator of statement.declarations) {
        if (
          declarator.id.type === "Identifier" &&
          declarator.init?.type === "Identifier" &&
          taintedNames.has(declarator.init.name)
        )
          taintedNames.add(declarator.id.name);
        const call = declarator.init;
        if (
          call?.type === "CallExpression" &&
          parserCallProvesNarrowing(
            call,
            [symbolNode, symbolNode.body, statement, declarator],
            taintedNames,
            ast,
            symbolNode,
          )
        )
          return true;
      }
    }
    if (
      statement.type === "IfStatement" &&
      !isAlwaysFalseExpression(statement.test) &&
      containsDirectThrow(statement.consequent) &&
      isSupportedInvalidPredicate(statement.test, taintedNames)
    )
      return true;
    const call = statement.type === "ExpressionStatement" ? statement.expression : null;
    if (
      call?.type === "CallExpression" &&
      parserCallProvesNarrowing(
        call,
        [symbolNode, symbolNode.body, statement],
        taintedNames,
        ast,
        symbolNode,
      )
    )
      return true;
  }
  return false;
}

function isAlwaysFalseExpression(node) {
  if (node?.type === "BooleanLiteral") return node.value === false;
  return (
    node?.type === "LogicalExpression" &&
    node.operator === "&&" &&
    isAlwaysFalseExpression(node.left)
  );
}

function containsDirectThrow(node) {
  return (
    node?.type === "ThrowStatement" ||
    (node?.type === "BlockStatement" &&
      node.body.some((statement) => statement.type === "ThrowStatement"))
  );
}

function isSupportedInvalidPredicate(node, taintedNames) {
  if (node.type === "LogicalExpression" && node.operator === "||") {
    return (
      isSupportedInvalidPredicate(node.left, taintedNames) &&
      isSupportedInvalidPredicate(node.right, taintedNames)
    );
  }
  if (node.type === "UnaryExpression" && node.operator === "!") {
    return expressionRootsInTaint(node.argument, taintedNames);
  }
  if (node.type !== "BinaryExpression" || (node.operator !== "!==" && node.operator !== "!="))
    return false;
  const typeOfSide =
    node.left.type === "UnaryExpression" && node.left.operator === "typeof" ? node.left : null;
  const literalSide = node.right.type === "StringLiteral" ? node.right : null;
  return Boolean(
    typeOfSide &&
      literalSide &&
      ["object", "string", "number", "boolean"].includes(literalSide.value) &&
      expressionRootsInTaint(typeOfSide.argument, taintedNames),
  );
}

function expressionRootsInTaint(node, taintedNames) {
  let current = node;
  while (current?.type === "MemberExpression" && !current.computed) current = current.object;
  return current?.type === "Identifier" && taintedNames.has(current.name);
}

function parserCallProvesNarrowing(call, ancestors, taintedNames, ast, shapeSymbol) {
  if (!ast || call.callee.type !== "MemberExpression" || call.callee.computed) return false;
  if (call.callee.object.type !== "Identifier" || call.callee.property.type !== "Identifier")
    return false;
  if (
    !call.arguments.some(
      (argument) => argument.type === "Identifier" && taintedNames.has(argument.name),
    )
  )
    return false;
  if (audit.bindingShadowsNameAt(ancestors, call.callee.object.name)) return false;
  const method = resolveLocalSchemaMethod(ast, call.callee.object.name, call.callee.property.name);
  if (!method) return false;
  if (call.callee.property.name === "parse") return hasExecutableShapeNarrowing(method);
  if (call.callee.property.name !== "safeParse" || !safeParseImplementationIsProven(method))
    return false;
  const parent = ancestors.at(-1);
  if (parent?.type !== "VariableDeclarator" || parent.id.type !== "Identifier") return false;
  return safeParseFailureDominates(shapeSymbol, parent.id.name, parent);
}

export {
  hasExactGenericInterruptFallback,
  isExactActionFailedTemplate,
  isExactGenericWarningFields,
  handlerDirectlyInspectsEnvelope,
  isExactInterruptFailurePredicate,
  moduleHelperReturnsResultError,
  hasExecutableShapeNarrowing,
  isAlwaysFalseExpression,
  containsDirectThrow,
  isSupportedInvalidPredicate,
  expressionRootsInTaint,
  parserCallProvesNarrowing,
  resolveLocalSchemaMethod,
};
export {
  safeParseImplementationIsProven,
  objectBooleanProperty,
  safeParseFailureDominates,
  nodeContainsIdentifier,
  shapeDominatesConsumerUse,
  collectUnusedPolicyFindings,
} from "./source-index-shape.mjs";
