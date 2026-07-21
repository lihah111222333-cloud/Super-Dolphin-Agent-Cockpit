import * as audit from "../rpc-contract-audit.mjs";
import {
  isExactHandlerFailureGate,
  isExactRuntimeOutcomeReturn,
} from "./turn-interrupt-runtime-checks.mjs";

function runtimePassesStrictInterruptResultToHandler(ast, handlerName, consumerName) {
  const consumer = audit.findModuleLevelSymbol(ast, consumerName);
  if (!consumer || consumer.body?.type !== "BlockStatement") return false;
  const helperDeclarations = consumer.body.body.filter(
    (statement) =>
      statement.type === "VariableDeclaration" &&
      statement.kind === "const" &&
      statement.declarations.length === 1 &&
      statement.declarations[0].id.type === "Identifier" &&
      statement.declarations[0].id.name === "runActiveThreadRPC" &&
      statement.declarations[0].init?.type === "ArrowFunctionExpression" &&
      statement.declarations[0].init.async,
  );
  if (helperDeclarations.length !== 1) return false;
  const helper = helperDeclarations[0].declarations[0].init;
  const tryStatements = helper.body.body.filter((statement) => statement.type === "TryStatement");
  if (tryStatements.length !== 1 || tryStatements[0].finalizer !== null) return false;
  const statements = tryStatements[0].block.body;
  if (statements.length !== 9) return false;

  const request =
    statements[3]?.type === "VariableDeclaration" && statements[3].declarations.length === 1
      ? statements[3].declarations[0]
      : null;
  const requestCall = request?.init;
  if (
    request?.id.type !== "Identifier" ||
    request.id.name !== "request" ||
    requestCall?.type !== "CallExpression" ||
    requestCall.callee.type !== "Identifier" ||
    requestCall.callee.name !== "cleanObject" ||
    requestCall.arguments.length !== 1 ||
    requestCall.arguments[0].type !== "Identifier" ||
    requestCall.arguments[0].name !== "payload"
  )
    return false;

  const result =
    statements[5]?.type === "VariableDeclaration" && statements[5].declarations.length === 1
      ? statements[5].declarations[0]
      : null;
  const conditional = result?.init;
  const interruptCall =
    conditional?.consequent?.type === "AwaitExpression" ? conditional.consequent.argument : null;
  const directCall =
    conditional?.alternate?.type === "AwaitExpression" ? conditional.alternate.argument : null;
  if (
    result?.id.type !== "Identifier" ||
    result.id.name !== "result" ||
    conditional?.type !== "ConditionalExpression" ||
    !isExactActionLiteralMatch(conditional.test, "thread.interrupt") ||
    !isExactNamedCall(interruptCall, "interruptWithinTimeout", ["rpc", "request"]) ||
    !isExactNamedCall(directCall, "rpc", ["request"])
  )
    return false;

  const validation = statements[6];
  const validationCall =
    validation?.consequent?.type === "ExpressionStatement"
      ? validation.consequent.expression
      : null;
  if (
    validation?.type !== "IfStatement" ||
    validation.alternate ||
    !isExactActionLiteralMatch(validation.test, "thread.interrupt") ||
    !isExactNamedCall(validationCall, "validateInterruptResponse", ["result", "request"]) ||
    !isExactHandlerFailureGate(statements[7], handlerName) ||
    !isExactRuntimeOutcomeReturn(statements[8], true)
  )
    return false;

  const pending = statements[4];
  const pendingCall =
    pending?.consequent?.type === "ExpressionStatement" ? pending.consequent.expression : null;
  return (
    pending?.type === "IfStatement" &&
    !pending.alternate &&
    isExactActionLiteralMatch(pending.test, "thread.interrupt") &&
    pendingCall?.type === "CallExpression" &&
    pendingCall.callee.type === "Identifier" &&
    pendingCall.callee.name === "notifyAction"
  );
}

function isExactActionLiteralMatch(node, action) {
  if (node?.type !== "BinaryExpression" || node.operator !== "===") return false;
  return (
    (node.left.type === "Identifier" &&
      node.left.name === "action" &&
      node.right.type === "StringLiteral" &&
      node.right.value === action) ||
    (node.right.type === "Identifier" &&
      node.right.name === "action" &&
      node.left.type === "StringLiteral" &&
      node.left.value === action)
  );
}

function isExactNamedCall(node, name, argumentNames) {
  return (
    node?.type === "CallExpression" &&
    node.callee.type === "Identifier" &&
    node.callee.name === name &&
    node.arguments.length === argumentNames.length &&
    node.arguments.every(
      (argument, index) => argument.type === "Identifier" && argument.name === argumentNames[index],
    )
  );
}

function countRuntimeProofBindings(node, name) {
  let count = 0;
  audit.traverseAst(node, (candidate) => {
    if (
      candidate.type === "VariableDeclarator" &&
      audit.bindingPatternContainsName(candidate.id, name)
    )
      count += 1;
    if (
      (candidate.type === "FunctionDeclaration" || candidate.type === "FunctionExpression") &&
      candidate.id?.name === name
    )
      count += 1;
    if (
      audit.isFunctionNode(candidate) &&
      candidate.params.some((parameter) => audit.bindingPatternContainsName(parameter, name))
    )
      count += 1;
    if (candidate.type === "CatchClause" && audit.bindingPatternContainsName(candidate.param, name))
      count += 1;
  });
  return count;
}

function hasRuntimeProofParameters(fn, allowOptions) {
  if (
    fn?.params[0]?.type !== "Identifier" ||
    fn.params[0].name !== "action" ||
    fn.params[1]?.type !== "Identifier" ||
    fn.params[1].name !== "rpc"
  )
    return false;
  if (fn.params.length === 2) return true;
  const options = fn.params[2];
  return (
    allowOptions &&
    fn.params.length === 3 &&
    options.type === "AssignmentPattern" &&
    options.left.type === "Identifier" &&
    options.left.name === "options" &&
    options.right.type === "ObjectExpression" &&
    options.right.properties.length === 0
  );
}

function isExactOutcomeFailureGate(statement) {
  if (statement?.type !== "IfStatement" || statement.alternate) return false;
  const returned =
    statement.consequent.type === "ReturnStatement"
      ? statement.consequent
      : statement.consequent.type === "BlockStatement" && statement.consequent.body.length === 1
        ? statement.consequent.body[0]
        : null;
  return (
    statement.test.type === "UnaryExpression" &&
    statement.test.operator === "!" &&
    statement.test.argument.type === "MemberExpression" &&
    !statement.test.argument.computed &&
    statement.test.argument.object.type === "Identifier" &&
    statement.test.argument.object.name === "outcome" &&
    statement.test.argument.property.type === "Identifier" &&
    statement.test.argument.property.name === "ok" &&
    returned?.type === "ReturnStatement" &&
    returned.argument?.type === "BooleanLiteral" &&
    returned.argument.value === false
  );
}

function isExactRuntimeCwdDeclaration(statement) {
  if (
    statement?.type !== "VariableDeclaration" ||
    statement.kind !== "const" ||
    statement.declarations.length !== 1
  )
    return false;
  const declaration = statement.declarations[0];
  const call = declaration.init;
  return (
    declaration.id.type === "Identifier" &&
    declaration.id.name === "cwd" &&
    call?.type === "CallExpression" &&
    call.callee.type === "Identifier" &&
    call.callee.name === "requireCwd" &&
    call.arguments.length === 1 &&
    call.arguments[0].type === "Identifier" &&
    call.arguments[0].name === "action"
  );
}

function isExactRuntimeCurrentStateDeclaration(statement) {
  if (
    statement?.type !== "VariableDeclaration" ||
    statement.kind !== "const" ||
    statement.declarations.length !== 1
  )
    return false;
  const declaration = statement.declarations[0];
  const call = declaration.init;
  return (
    declaration.id.type === "Identifier" &&
    declaration.id.name === "currentState" &&
    call?.type === "CallExpression" &&
    call.callee.type === "Identifier" &&
    call.callee.name === "get" &&
    call.arguments.length === 0
  );
}

function isExactRuntimeRequiresActiveTurnDeclaration(statement) {
  if (
    statement?.type !== "VariableDeclaration" ||
    statement.kind !== "const" ||
    statement.declarations.length !== 1
  )
    return false;
  const declaration = statement.declarations[0];
  const call = declaration.init;
  return (
    declaration.id.type === "Identifier" &&
    declaration.id.name === "requiresActiveTurn" &&
    call?.type === "CallExpression" &&
    call.callee.type === "Identifier" &&
    call.callee.name === "threadActionRequiresActiveTurn" &&
    call.arguments.length === 1 &&
    call.arguments[0].type === "Identifier" &&
    call.arguments[0].name === "action"
  );
}

function isExactRuntimeActiveTurnTargetDeclaration(statement) {
  if (
    statement?.type !== "VariableDeclaration" ||
    statement.kind !== "const" ||
    statement.declarations.length !== 1
  )
    return false;
  const declaration = statement.declarations[0];
  const conditional = declaration.init;
  const targetCall = conditional?.type === "ConditionalExpression" ? conditional.consequent : null;
  return (
    declaration.id.type === "Identifier" &&
    declaration.id.name === "activeTurnTarget" &&
    conditional?.type === "ConditionalExpression" &&
    conditional.test.type === "Identifier" &&
    conditional.test.name === "requiresActiveTurn" &&
    targetCall.type === "CallExpression" &&
    targetCall.callee.type === "Identifier" &&
    targetCall.callee.name === "activeThreadInterruptTarget" &&
    targetCall.arguments.length === 1 &&
    targetCall.arguments[0].type === "Identifier" &&
    targetCall.arguments[0].name === "currentState" &&
    conditional.alternate.type === "NullLiteral"
  );
}

function isExactRuntimeThreadIdDeclaration(statement) {
  if (
    statement?.type !== "VariableDeclaration" ||
    statement.kind !== "const" ||
    statement.declarations.length !== 1
  )
    return false;
  const declaration = statement.declarations[0];
  const outer = declaration.init;
  const inner = outer?.type === "LogicalExpression" ? outer.left : null;
  const optionsThreadId = inner?.type === "LogicalExpression" ? inner.left : null;
  const activeThreadId = inner?.type === "LogicalExpression" ? inner.right : null;
  const fallbackCall = outer?.type === "LogicalExpression" ? outer.right : null;
  const activeStateId = fallbackCall?.type === "CallExpression" ? fallbackCall.arguments[1] : null;
  return (
    declaration.id.type === "Identifier" &&
    declaration.id.name === "threadId" &&
    outer?.type === "LogicalExpression" &&
    outer.operator === "||" &&
    inner?.type === "LogicalExpression" &&
    inner.operator === "||" &&
    optionsThreadId?.type === "MemberExpression" &&
    !optionsThreadId.computed &&
    optionsThreadId.object.type === "Identifier" &&
    optionsThreadId.object.name === "options" &&
    optionsThreadId.property.type === "Identifier" &&
    optionsThreadId.property.name === "threadId" &&
    activeThreadId?.type === "OptionalMemberExpression" &&
    !activeThreadId.computed &&
    activeThreadId.optional &&
    activeThreadId.object.type === "Identifier" &&
    activeThreadId.object.name === "activeTurnTarget" &&
    activeThreadId.property.type === "Identifier" &&
    activeThreadId.property.name === "threadId" &&
    fallbackCall?.type === "CallExpression" &&
    fallbackCall.callee.type === "Identifier" &&
    fallbackCall.callee.name === "backendThreadIdForState" &&
    fallbackCall.arguments.length === 2 &&
    fallbackCall.arguments[0].type === "Identifier" &&
    fallbackCall.arguments[0].name === "currentState" &&
    activeStateId?.type === "MemberExpression" &&
    !activeStateId.computed &&
    activeStateId.object.type === "Identifier" &&
    activeStateId.object.name === "currentState" &&
    activeStateId.property.type === "Identifier" &&
    activeStateId.property.name === "activeThreadId"
  );
}

function isExactRuntimeNoThreadGuard(statement) {
  if (
    statement?.type !== "IfStatement" ||
    statement.alternate ||
    statement.test.type !== "UnaryExpression" ||
    statement.test.operator !== "!" ||
    statement.test.argument.type !== "Identifier" ||
    statement.test.argument.name !== "threadId" ||
    statement.consequent.type !== "BlockStatement" ||
    statement.consequent.body.length !== 2
  )
    return false;
  const notice = statement.consequent.body[0];
  const noticeCall = notice.type === "ExpressionStatement" ? notice.expression : null;
  if (
    noticeCall?.type !== "CallExpression" ||
    noticeCall.callee.type !== "Identifier" ||
    noticeCall.callee.name !== "notifyAction" ||
    noticeCall.arguments.length !== 2 ||
    noticeCall.arguments[0].type !== "StringLiteral" ||
    noticeCall.arguments[0].value !== "当前没有可操作的后端线程" ||
    noticeCall.arguments[1].type !== "StringLiteral" ||
    noticeCall.arguments[1].value !== "warning"
  )
    return false;
  const returned = statement.consequent.body[1];
  const node = returned.type === "ReturnStatement" ? returned.argument : null;
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
    threadProperty?.value.type === "StringLiteral" &&
    threadProperty.value.value === "" &&
    resultProperty?.value.type === "NullLiteral"
  );
}

export {
  runtimePassesStrictInterruptResultToHandler,
  isExactActionLiteralMatch,
  isExactNamedCall,
  countRuntimeProofBindings,
  hasRuntimeProofParameters,
  isExactOutcomeFailureGate,
  isExactRuntimeCwdDeclaration,
  isExactRuntimeCurrentStateDeclaration,
  isExactRuntimeRequiresActiveTurnDeclaration,
  isExactRuntimeActiveTurnTargetDeclaration,
  isExactRuntimeThreadIdDeclaration,
  isExactRuntimeNoThreadGuard,
};
export {
  isExactRuntimePayloadDeclaration,
  isExactRuntimePayloadFailureGuard,
  isExactRuntimeSuccessStatement,
  isExactHandlerFailureGate,
  isExactRuntimeHandlerArgument,
  isExactRuntimeOutcomeReturn,
  consumerPassesFacadeResultToHandler,
} from "./turn-interrupt-runtime-checks.mjs";
