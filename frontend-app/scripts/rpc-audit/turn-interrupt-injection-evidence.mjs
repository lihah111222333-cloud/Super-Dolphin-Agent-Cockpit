import * as audit from "../rpc-contract-audit.mjs";
import {
  isExactTurnInterruptPolicy,
  isFunctionNode,
  walkAstWithAncestors,
} from "./facade-binding-provenance.mjs";

async function provesTurnInterruptInjection(auditContext, entry) {
  if (!isExactTurnInterruptPolicy(entry)) return false;
  const ast = await audit.readAuditAst(auditContext, audit.TURN_INTERRUPT_INJECTION_PATH);
  const consumerSymbol = audit.findModuleLevelSymbol(ast, "createActiveThreadActions");
  if (!consumerSymbol?.body || consumerSymbol.body.type !== "BlockStatement") return false;
  const facadeAliases = new Set();
  for (const statement of ast.program.body) {
    if (
      statement.type !== "ImportDeclaration" ||
      !audit.moduleSpecifierResolvesTo(
        audit.TURN_INTERRUPT_INJECTION_PATH,
        statement.source.value,
        audit.RPC_FACADE_PATH,
      )
    )
      continue;
    for (const specifier of statement.specifiers) {
      if (
        specifier.type === "ImportSpecifier" &&
        audit.moduleExportName(specifier.imported) === entry.facade
      )
        facadeAliases.add(specifier.local.name);
    }
  }
  if (facadeAliases.size !== 1) return false;
  const statements = consumerSymbol.body.body;
  if (
    statements.length !== 1 ||
    statements[0].type !== "ReturnStatement" ||
    statements[0].argument?.type !== "ObjectExpression"
  )
    return false;
  const properties = statements[0].argument.properties;
  if (!properties.every((property) => property.type === "ObjectProperty" && !property.computed))
    return false;
  const actions = properties.filter(
    (property) => audit.staticPropertyKeyName(property) === "interruptActiveThread",
  );
  if (actions.length !== 1) return false;
  const arrow = actions[0].value;
  const call = arrow?.type === "ArrowFunctionExpression" ? arrow.body : null;
  return (
    call?.type === "CallExpression" &&
    call.callee.type === "MemberExpression" &&
    !call.callee.computed &&
    call.callee.object.type === "Identifier" &&
    call.callee.object.name === "runtime" &&
    call.callee.property.type === "Identifier" &&
    call.callee.property.name === "activeThreadRPC" &&
    call.arguments.length === 2 &&
    call.arguments[0].type === "StringLiteral" &&
    call.arguments[0].value === "thread.interrupt" &&
    call.arguments[1].type === "Identifier" &&
    facadeAliases.has(call.arguments[1].name)
  );
}

function runtimePassesAwaitedResultToHandler(ast, handlerSymbol, handlerName, consumerName) {
  const consumer = audit.findModuleLevelSymbol(ast, consumerName);
  if (!consumer || consumer === handlerSymbol || consumer.body?.type !== "BlockStatement")
    return false;
  const bindings = new Map([
    ["activeThreadRPC", []],
    ["runActiveThreadRPC", []],
  ]);
  for (const statement of consumer.body.body) {
    if (statement.type !== "VariableDeclaration" || statement.kind !== "const") continue;
    for (const item of statement.declarations) {
      if (
        item.id.type === "Identifier" &&
        bindings.has(item.id.name) &&
        item.init?.type === "ArrowFunctionExpression" &&
        item.init.async
      )
        bindings.get(item.id.name).push(item.init);
    }
  }
  const [wrapper] = bindings.get("activeThreadRPC");
  const [helper] = bindings.get("runActiveThreadRPC");
  if (
    bindings.get("activeThreadRPC").length !== 1 ||
    bindings.get("runActiveThreadRPC").length !== 1 ||
    audit.countRuntimeProofBindings(consumer.body, "activeThreadRPC") !== 1 ||
    audit.countRuntimeProofBindings(consumer.body, "runActiveThreadRPC") !== 1 ||
    audit.countRuntimeProofBindings(consumer.body, handlerName) !== 0 ||
    !audit.hasRuntimeProofParameters(wrapper, false) ||
    !audit.hasRuntimeProofParameters(helper, true) ||
    wrapper.body.type !== "BlockStatement" ||
    helper.body.type !== "BlockStatement"
  )
    return false;

  let invalidComputedCall = false;
  audit.traverseAst(consumer.body, (node) => {
    if (
      node.type === "CallExpression" &&
      (node.callee.type === "MemberExpression" ||
        node.callee.type === "OptionalMemberExpression") &&
      node.callee.computed
    )
      invalidComputedCall = true;
  });
  if (invalidComputedCall) return false;

  const wrapperStatements = wrapper.body.body;
  if (wrapperStatements.length !== 4) return false;
  const outcomeDeclaration = wrapperStatements[0];
  const outcome =
    outcomeDeclaration?.type === "VariableDeclaration" &&
    outcomeDeclaration.kind === "const" &&
    outcomeDeclaration.declarations.length === 1
      ? outcomeDeclaration.declarations[0]
      : null;
  const helperCall = outcome?.init?.type === "AwaitExpression" ? outcome.init.argument : null;
  const helperCalls = [];
  let wrapperNestedFunction = false;
  walkAstWithAncestors(wrapper.body, (node, ancestors) => {
    if (ancestors.some((ancestor) => isFunctionNode(ancestor))) wrapperNestedFunction = true;
    if (
      node.type === "CallExpression" &&
      node.callee.type === "Identifier" &&
      node.callee.name === "runActiveThreadRPC"
    )
      helperCalls.push(node);
  });
  if (
    wrapperNestedFunction ||
    outcome?.id.type !== "Identifier" ||
    outcome.id.name !== "outcome" ||
    helperCall?.type !== "CallExpression" ||
    helperCall.callee.type !== "Identifier" ||
    helperCall.callee.name !== "runActiveThreadRPC" ||
    helperCall.arguments.length !== 2 ||
    helperCall.arguments[0].type !== "Identifier" ||
    helperCall.arguments[0].name !== "action" ||
    helperCall.arguments[1].type !== "Identifier" ||
    helperCall.arguments[1].name !== "rpc" ||
    helperCalls.length !== 1 ||
    audit.countRuntimeProofBindings(wrapper, "action") !== 1 ||
    audit.countRuntimeProofBindings(wrapper, "rpc") !== 1 ||
    audit.countRuntimeProofBindings(wrapper, "outcome") !== 1 ||
    !audit.isExactOutcomeFailureGate(wrapperStatements[1]) ||
    !audit.isExactRuntimeSuccessStatement(wrapperStatements[2], wrapper) ||
    wrapperStatements[3].type !== "ReturnStatement" ||
    wrapperStatements[3].argument?.type !== "BooleanLiteral" ||
    wrapperStatements[3].argument.value !== true
  )
    return false;

  const rpcCalls = [];
  const handlerCalls = [];
  let helperNestedFunction = false;
  let resultDeclaration = null;
  let resultBlock = null;
  const helperStatements = helper.body.body;
  if (
    helperStatements.length !== 6 ||
    !audit.isExactRuntimeCurrentStateDeclaration(helperStatements[0]) ||
    !audit.isExactRuntimeRequiresActiveTurnDeclaration(helperStatements[1]) ||
    !audit.isExactRuntimeActiveTurnTargetDeclaration(helperStatements[2]) ||
    !audit.isExactRuntimeThreadIdDeclaration(helperStatements[3]) ||
    !audit.isExactRuntimeNoThreadGuard(helperStatements[4]) ||
    helperStatements[5].type !== "TryStatement"
  )
    return false;
  const directTryStatements = helper.body.body.filter(
    (statement) => statement.type === "TryStatement",
  );
  if (
    directTryStatements.length !== 1 ||
    directTryStatements[0] !== helperStatements[5] ||
    directTryStatements[0].finalizer !== null
  )
    return false;
  const [resultTry] = directTryStatements;
  walkAstWithAncestors(helper.body, (node, ancestors) => {
    if (ancestors.some((ancestor) => isFunctionNode(ancestor))) helperNestedFunction = true;
    if (node.type === "CallExpression" && node.callee.type === "Identifier") {
      if (node.callee.name === "rpc") rpcCalls.push(node);
      if (node.callee.name === handlerName) handlerCalls.push(node);
    }
    if (
      node.type !== "VariableDeclarator" ||
      node.id.type !== "Identifier" ||
      node.id.name !== "result"
    )
      return;
    const declaration = ancestors.at(-1);
    const block = ancestors.at(-2);
    const rpcCall = node.init?.type === "AwaitExpression" ? node.init.argument : null;
    if (
      declaration?.type === "VariableDeclaration" &&
      declaration.kind === "const" &&
      declaration.declarations.length === 1 &&
      block?.type === "BlockStatement" &&
      block === resultTry.block &&
      rpcCall?.type === "CallExpression" &&
      rpcCall.callee.type === "Identifier" &&
      rpcCall.callee.name === "rpc" &&
      rpcCall.arguments.length === 1
    ) {
      resultDeclaration = declaration;
      resultBlock = block;
    }
  });
  if (
    helperNestedFunction ||
    rpcCalls.length !== 1 ||
    handlerCalls.length !== 1 ||
    audit.countRuntimeProofBindings(helper, "action") !== 1 ||
    audit.countRuntimeProofBindings(helper, "rpc") !== 1 ||
    audit.countRuntimeProofBindings(helper, "result") !== 1 ||
    !resultDeclaration ||
    !resultBlock
  )
    return false;
  const resultIndex = resultBlock.body.indexOf(resultDeclaration);
  const failureGate = resultBlock.body[resultIndex + 1];
  const successReturn = resultBlock.body[resultIndex + 2];
  if (
    resultIndex !== 3 ||
    !audit.isExactRuntimeCwdDeclaration(resultBlock.body[0]) ||
    !audit.isExactRuntimePayloadDeclaration(resultBlock.body[1]) ||
    !audit.isExactRuntimePayloadFailureGuard(resultBlock.body[2]) ||
    !audit.isExactHandlerFailureGate(failureGate, handlerName) ||
    !audit.isExactRuntimeOutcomeReturn(successReturn, true) ||
    successReturn !== resultBlock.body.at(-1)
  )
    return false;

  const trueReturns = [];
  const handledFalseReturns = [];
  audit.traverseAst(helper.body, (node) => {
    if (node.type !== "ReturnStatement") return;
    if (audit.isExactRuntimeOutcomeReturn(node, true)) trueReturns.push(node);
    if (audit.isExactRuntimeOutcomeReturn(node, false)) handledFalseReturns.push(node);
  });
  if (
    trueReturns.length !== 1 ||
    trueReturns[0] !== successReturn ||
    handledFalseReturns.length !== 1
  )
    return false;

  let exposures = 0;
  for (const statement of consumer.body.body) {
    const call = statement.type === "ExpressionStatement" ? statement.expression : null;
    if (
      call?.type !== "CallExpression" ||
      call.callee.type !== "MemberExpression" ||
      call.callee.computed ||
      call.callee.object.type !== "Identifier" ||
      call.callee.object.name !== "Object" ||
      call.callee.property.type !== "Identifier" ||
      call.callee.property.name !== "assign" ||
      call.arguments[0]?.type !== "Identifier" ||
      call.arguments[0].name !== "runtime" ||
      call.arguments[1]?.type !== "ObjectExpression"
    )
      continue;
    for (const property of call.arguments[1].properties) {
      if (
        property.type === "ObjectProperty" &&
        !property.computed &&
        audit.staticPropertyKeyName(property) === "activeThreadRPC" &&
        property.value.type === "Identifier" &&
        property.value.name === "activeThreadRPC"
      )
        exposures += 1;
    }
  }
  return exposures === 1;
}

export { provesTurnInterruptInjection, runtimePassesAwaitedResultToHandler };
