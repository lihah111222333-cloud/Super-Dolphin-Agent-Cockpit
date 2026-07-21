import * as audit from "../rpc-contract-audit.mjs";
import { moduleSpecifierResolvesTo } from "./facade-call-provenance.mjs";

function hasRuntimeResultHandledRegressionEvidence(ast, testPath, symbol, entry) {
  const handlerPath = entry.responsePolicy?.handler?.path;
  let exactRuntimeAttachImported = false;
  for (const statement of ast.program.body) {
    if (
      statement.type !== "ImportDeclaration" ||
      !moduleSpecifierResolvesTo(testPath, statement.source.value, handlerPath)
    )
      continue;
    for (const specifier of statement.specifiers) {
      if (
        specifier.type === "ImportSpecifier" &&
        specifier.imported.type === "Identifier" &&
        specifier.imported.name === "attachActiveThreadRpcRuntime" &&
        specifier.local.name === "attachActiveThreadRpcRuntime"
      )
        exactRuntimeAttachImported = true;
    }
  }
  if (!exactRuntimeAttachImported) return false;
  let proven = false;
  audit.traverseAst(ast, (node) => {
    if (
      proven ||
      node.type !== "CallExpression" ||
      node.callee.type !== "Identifier" ||
      (node.callee.name !== "it" && node.callee.name !== "test") ||
      node.arguments[0]?.type !== "StringLiteral" ||
      node.arguments[0].value !== symbol
    )
      return;
    const callback = node.arguments[1];
    if (
      (callback?.type !== "ArrowFunctionExpression" && callback?.type !== "FunctionExpression") ||
      !callback.async ||
      callback.body.type !== "BlockStatement"
    )
      return;
    const statements = callback.body.body;
    const preludeSafe =
      statements.length >= 8 &&
      isExactFactoryDeclaration(statements[0], "runtime", "createRuntime") &&
      isExactFactoryDeclaration(statements[1], "deps", "createDeps") &&
      isExactFailureRpcDeclaration(statements[2]) &&
      isExactRuntimeAttachSetup(statements[3]);
    const awaitedSafe = isExactInterruptFailureAwait(
      statements[4],
      audit.responsePolicyRpcMethod(entry),
    );
    const assertionTail = statements.slice(5);
    const tailSafe = assertionTail.length > 0 && assertionTail.every(isExpectAssertionStatement);
    const requestAssertion = assertionTail.some(isExactInterruptRequestAssertion);
    const warningAssertion = assertionTail.some(isExactInterruptWarningAssertion);
    const noPendingAssertion = assertionTail.some(isExactInterruptNoPendingAssertion);
    const noSuccessAssertion = assertionTail.some(isExactInterruptNoSuccessAssertion);
    const addWarningAssertion = assertionTail.some(isExactInterruptAddWarningAssertion);
    if (
      preludeSafe &&
      awaitedSafe &&
      tailSafe &&
      requestAssertion &&
      warningAssertion &&
      noPendingAssertion &&
      noSuccessAssertion &&
      addWarningAssertion
    )
      proven = true;
  });
  return proven;
}

function isExactFactoryDeclaration(statement, bindingName, factoryName) {
  if (
    statement?.type !== "VariableDeclaration" ||
    statement.kind !== "const" ||
    statement.declarations.length !== 1
  )
    return false;
  const declaration = statement.declarations[0];
  return (
    declaration.id.type === "Identifier" &&
    declaration.id.name === bindingName &&
    declaration.init?.type === "CallExpression" &&
    declaration.init.callee.type === "Identifier" &&
    declaration.init.callee.name === factoryName &&
    declaration.init.arguments.length === 0
  );
}

function isExactFailureRpcDeclaration(statement) {
  if (
    statement?.type !== "VariableDeclaration" ||
    statement.kind !== "const" ||
    statement.declarations.length !== 1
  )
    return false;
  const declaration = statement.declarations[0];
  if (declaration.id.type !== "Identifier" || declaration.id.name !== "rpc") return false;
  const resolvedCall = declaration.init;
  if (
    resolvedCall?.type !== "CallExpression" ||
    resolvedCall.arguments.length !== 1 ||
    !isStaticMemberNamed(resolvedCall.callee, "mockResolvedValue")
  )
    return false;
  const fnCall = resolvedCall.callee.object;
  if (
    fnCall.type !== "CallExpression" ||
    fnCall.arguments.length !== 0 ||
    !isStaticMemberNamed(fnCall.callee, "fn") ||
    fnCall.callee.object.type !== "Identifier" ||
    fnCall.callee.object.name !== "vi"
  )
    return false;
  const result = resolvedCall.arguments[0];
  return isExactInterruptTimeoutResult(result);
}

function isExactInterruptTimeoutResult(result) {
  if (result?.type !== "ObjectExpression" || result.properties.length !== 13) return false;
  const expected = {
    ok: false,
    accepted: true,
    requestId: "stop-request-1",
    expectedTurnId: "turn-1",
    turnId: "turn-1",
    status: "running",
    confirmed: true,
    mode: "interrupt_timeout",
    interruptSent: true,
    stateBefore: "running",
    stateAfter: "running",
    waitedMs: 1,
    activeObserved: true,
  };
  return Object.entries(expected).every(([key, value]) => {
    const property = result.properties.find(
      (candidate) =>
        candidate.type === "ObjectProperty" && audit.staticPropertyKeyName(candidate) === key,
    );
    if (typeof value === "boolean")
      return property?.value.type === "BooleanLiteral" && property.value.value === value;
    if (typeof value === "number")
      return property?.value.type === "NumericLiteral" && property.value.value === value;
    return property?.value.type === "StringLiteral" && property.value.value === value;
  });
}

function isExactRuntimeAttachSetup(statement) {
  if (statement.type !== "ExpressionStatement" || statement.expression.type !== "CallExpression")
    return false;
  const call = statement.expression;
  return (
    call.callee.type === "Identifier" &&
    call.callee.name === "attachActiveThreadRpcRuntime" &&
    call.arguments.length === 2 &&
    call.arguments[0].type === "Identifier" &&
    call.arguments[0].name === "runtime" &&
    call.arguments[1].type === "Identifier" &&
    call.arguments[1].name === "deps"
  );
}

function isExpectAssertionStatement(statement) {
  return exactExpectMatcher(statement) !== null;
}

function isExactInterruptFailureAwait(statement, rpcMethod) {
  if (statement?.type !== "ExpressionStatement" || statement.expression.type !== "AwaitExpression")
    return false;
  const matcher = statement.expression.argument;
  if (
    matcher.type !== "CallExpression" ||
    matcher.arguments.length !== 1 ||
    matcher.arguments[0].type !== "BooleanLiteral" ||
    matcher.arguments[0].value !== false ||
    !isStaticMemberNamed(matcher.callee, "toBe")
  )
    return false;
  const resolves = matcher.callee.object;
  if (!isStaticMemberNamed(resolves, "resolves")) return false;
  const expectCall = resolves.object;
  if (
    expectCall.type !== "CallExpression" ||
    expectCall.arguments.length !== 1 ||
    expectCall.callee.type !== "Identifier" ||
    expectCall.callee.name !== "expect"
  )
    return false;
  const runtimeCall = expectCall.arguments[0];
  return (
    runtimeCall.type === "CallExpression" &&
    isStaticMemberNamed(runtimeCall.callee, "activeThreadRPC") &&
    runtimeCall.callee.object.type === "Identifier" &&
    runtimeCall.callee.object.name === "runtime" &&
    runtimeCall.arguments.length === 2 &&
    runtimeCall.arguments[0].type === "StringLiteral" &&
    runtimeCall.arguments[0].value === rpcMethod &&
    runtimeCall.arguments[1].type === "Identifier" &&
    runtimeCall.arguments[1].name === "rpc"
  );
}

function exactExpectMatcher(statement) {
  if (statement?.type !== "ExpressionStatement" || statement.expression.type !== "CallExpression")
    return null;
  const call = statement.expression;
  if (
    call.callee.type !== "MemberExpression" ||
    call.callee.computed ||
    call.callee.property.type !== "Identifier"
  )
    return null;
  let root = call.callee.object;
  const modifiers = [];
  while (root.type === "MemberExpression") {
    if (root.computed || root.property.type !== "Identifier") return null;
    modifiers.unshift(root.property.name);
    root = root.object;
  }
  if (
    root.type !== "CallExpression" ||
    root.arguments.length !== 1 ||
    root.callee.type !== "Identifier" ||
    root.callee.name !== "expect"
  )
    return null;
  return {
    arguments: call.arguments,
    expectArgument: root.arguments[0],
    matcher: call.callee.property.name,
    modifiers,
  };
}

function isExactInterruptWarningAssertion(statement) {
  const matcher = exactExpectMatcher(statement);
  return (
    matcher?.matcher === "toHaveBeenCalledWith" &&
    matcher.modifiers.length === 0 &&
    isRuntimeMember(matcher.expectArgument, "notifyAction") &&
    hasExactStringArguments(matcher.arguments, ["停止未确认，任务可能仍在运行", "warning"]) &&
    isExactStringObject(matcher.arguments[2], { threadId: "thread-1" })
  );
}

function isExactInterruptNoSuccessAssertion(statement) {
  const matcher = exactExpectMatcher(statement);
  return (
    matcher?.matcher === "toHaveBeenCalledWith" &&
    matcher.modifiers.length === 1 &&
    matcher.modifiers[0] === "not" &&
    isRuntimeMember(matcher.expectArgument, "notifyAction") &&
    hasExactStringArguments(matcher.arguments, ["已发送中断请求", "success"]) &&
    isExactStringObject(matcher.arguments[2], { threadId: "thread-1" })
  );
}

function isExactInterruptNoPendingAssertion(statement) {
  const matcher = exactExpectMatcher(statement);
  return (
    matcher?.matcher === "toHaveBeenCalledWith" &&
    matcher.modifiers.length === 1 &&
    matcher.modifiers[0] === "not" &&
    isRuntimeMember(matcher.expectArgument, "notifyAction") &&
    hasExactStringArguments(matcher.arguments, [
      "正在请求停止，尚未确认，任务可能仍在运行",
      "info",
    ]) &&
    isExactStringObject(matcher.arguments[2], { threadId: "thread-1" })
  );
}

function isExactInterruptAddWarningAssertion(statement) {
  const matcher = exactExpectMatcher(statement);
  return (
    matcher?.matcher === "toHaveBeenCalledWith" &&
    matcher.modifiers.length === 0 &&
    isRuntimeMember(matcher.expectArgument, "addWarning") &&
    hasExactStringArguments(matcher.arguments, ["warn", "thread.interrupt.unconfirmed"]) &&
    isExactStringObject(matcher.arguments[2], {
      threadId: "thread-1",
      error: "stop confirmation timed out; see Health diagnostic ID",
    })
  );
}

function isExactInterruptRequestAssertion(statement) {
  const matcher = exactExpectMatcher(statement);
  return (
    matcher?.matcher === "toHaveBeenCalledWith" &&
    matcher.modifiers.length === 0 &&
    matcher.expectArgument.type === "Identifier" &&
    matcher.expectArgument.name === "rpc" &&
    matcher.arguments.length === 1 &&
    isExactStringObject(matcher.arguments[0], {
      cwd: "/repo/app",
      threadId: "thread-1",
      expectedTurnId: "turn-1",
      requestId: "stop-request-1",
      source: "ui_stop",
    })
  );
}

function isStaticMemberNamed(node, name) {
  return (
    node?.type === "MemberExpression" &&
    !node.computed &&
    node.property.type === "Identifier" &&
    node.property.name === name
  );
}

function isRuntimeMember(node, name) {
  return (
    isStaticMemberNamed(node, name) &&
    node.object.type === "Identifier" &&
    node.object.name === "runtime"
  );
}

function hasExactStringArguments(args, values) {
  return (
    args.length === values.length + 1 &&
    values.every(
      (value, index) => args[index]?.type === "StringLiteral" && args[index].value === value,
    )
  );
}

function isExactStringObject(node, expected) {
  if (node?.type !== "ObjectExpression" || node.properties.length !== Object.keys(expected).length)
    return false;
  return Object.entries(expected).every(([key, value]) => {
    const property = node.properties.find(
      (candidate) =>
        candidate.type === "ObjectProperty" && audit.staticPropertyKeyName(candidate) === key,
    );
    return property?.value.type === "StringLiteral" && property.value.value === value;
  });
}

function hasMalformedFacadeMock(ast, testPath, entry) {
  const facadeName = entry.facade.split(".").at(-1);
  for (const statement of ast.program.body) {
    if (statement.type !== "ExpressionStatement") continue;
    const call = statement.expression;
    if (
      call.type !== "CallExpression" ||
      call.callee.type !== "MemberExpression" ||
      call.callee.computed ||
      call.callee.object.type !== "Identifier" ||
      call.callee.object.name !== "vi" ||
      call.callee.property.type !== "Identifier" ||
      call.callee.property.name !== "mock" ||
      call.arguments[0]?.type !== "StringLiteral" ||
      !moduleSpecifierResolvesTo(testPath, call.arguments[0].value, audit.RPC_FACADE_PATH)
    )
      continue;
    const factory = call.arguments[1];
    if (factory?.type !== "ArrowFunctionExpression" && factory?.type !== "FunctionExpression")
      continue;
    const mockedExports = functionReturnedObject(factory);
    if (!mockedExports) continue;
    const facade = mockedExports.properties.find(
      (property) =>
        property.type === "ObjectProperty" && audit.staticPropertyKeyName(property) === facadeName,
    );
    if (facade?.type !== "ObjectProperty") continue;
    const resolved = findMockResolvedValueArgument(facade.value);
    if (resolved && isMalformedResponseLiteral(resolved)) return true;
  }
  return false;
}

function isSpecificShapeFailureMatcher(node) {
  if (node?.type === "Identifier") return node.name === "TypeError";
  return node?.type === "StringLiteral" && /invalid|malformed|shape/i.test(node.value);
}

function functionReturnedObject(fn) {
  if (fn.body.type === "ObjectExpression") return fn.body;
  if (fn.body.type !== "BlockStatement") return null;
  const returns = fn.body.body.filter((statement) => statement.type === "ReturnStatement");
  return returns.length === 1 && returns[0].argument?.type === "ObjectExpression"
    ? returns[0].argument
    : null;
}

function findMockResolvedValueArgument(node) {
  let found = null;
  audit.traverseAst(node, (candidate) => {
    if (
      !found &&
      candidate.type === "CallExpression" &&
      candidate.callee.type === "MemberExpression" &&
      !candidate.callee.computed &&
      candidate.callee.property.type === "Identifier" &&
      candidate.callee.property.name === "mockResolvedValue" &&
      candidate.arguments.length === 1
    )
      found = candidate.arguments[0];
  });
  return found;
}

function isMalformedResponseLiteral(node) {
  return (
    node.type === "NullLiteral" ||
    node.type === "BooleanLiteral" ||
    node.type === "NumericLiteral" ||
    node.type === "StringLiteral" ||
    node.type === "ArrayExpression" ||
    node.type === "ObjectExpression"
  );
}

function memberChainContainsName(node, name) {
  let current = node;
  while (current?.type === "MemberExpression" && !current.computed) {
    if (current.property.type === "Identifier" && current.property.name === name) return true;
    current = current.object;
  }
  return false;
}

function hasNonTestRunnerBinding(ast, name) {
  for (const statement of ast.program.body) {
    if (statement.type === "ImportDeclaration") {
      if (
        statement.source.value !== "vitest" &&
        statement.specifiers.some((specifier) => specifier.local?.name === name)
      ) {
        return true;
      }
      continue;
    }
    if (
      audit.declarationBindsName(statement, name) ||
      (statement.type === "ExportNamedDeclaration" &&
        audit.declarationBindsName(statement.declaration, name))
    ) {
      return true;
    }
  }
  return false;
}

export {
  hasRuntimeResultHandledRegressionEvidence,
  isExactFactoryDeclaration,
  isExactFailureRpcDeclaration,
  isExactInterruptTimeoutResult,
  isExactRuntimeAttachSetup,
  isExpectAssertionStatement,
  isExactInterruptFailureAwait,
  exactExpectMatcher,
  isExactInterruptWarningAssertion,
  isExactInterruptNoSuccessAssertion,
  isExactInterruptNoPendingAssertion,
  isExactInterruptAddWarningAssertion,
  isExactInterruptRequestAssertion,
  isStaticMemberNamed,
  isRuntimeMember,
  hasExactStringArguments,
  isExactStringObject,
  hasMalformedFacadeMock,
  isSpecificShapeFailureMatcher,
  functionReturnedObject,
  findMockResolvedValueArgument,
  isMalformedResponseLiteral,
  memberChainContainsName,
  hasNonTestRunnerBinding,
};
export {
  moduleSpecifierResolvesTo,
  moduleSpecifierResolvedPath,
  findFacadeCalls,
  directFacadeCallProvenance,
  promoteTransparentPromiseWrapperCall,
  transparentPromiseWrapperAt,
  directFacadeRuntimeCallMatches,
  resolveImportedWrapperProvenance,
} from "./facade-call-provenance.mjs";
