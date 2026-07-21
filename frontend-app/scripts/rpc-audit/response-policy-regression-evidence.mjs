import * as audit from "../rpc-contract-audit.mjs";

function hasPageIgnoredResultRegressionEvidence(ast, symbol, entry, consumerOutcomeProof) {
  if (!consumerOutcomeProof) return false;
  const facadeName = entry.facade.split(".").at(-1);
  let proven = false;
  audit.traverseAst(ast, (node) => {
    if (
      proven ||
      node.type !== "CallExpression" ||
      node.callee.type !== "Identifier" ||
      (node.callee.name !== "it" && node.callee.name !== "test") ||
      node.arguments[0]?.type !== "StringLiteral" ||
      node.arguments[0].value !== symbol ||
      audit.hasNonTestRunnerBinding(ast, node.callee.name)
    )
      return;
    const callback = node.arguments[1];
    if (
      (callback?.type !== "ArrowFunctionExpression" && callback?.type !== "FunctionExpression") ||
      callback.body.type !== "BlockStatement"
    )
      return;
    const statements = callback.body.body;
    let mockReceiver = "";
    const mockIndex = statements.findIndex((statement) => {
      mockReceiver = malformedFacadeMockReceiver(statement, facadeName);
      return Boolean(mockReceiver);
    });
    if (mockIndex < 0) return;
    const triggerIndex = statements.findIndex(
      (statement, index) => index > mockIndex && statementContainsPageTrigger(statement),
    );
    if (triggerIndex < 0) return;
    const invocationIndex = statements.findIndex(
      (statement, index) =>
        index > triggerIndex &&
        statementContainsExactFacadeInvocationAssertion(statement, mockReceiver, facadeName),
    );
    if (invocationIndex < 0) return;
    proven = statements.some(
      (statement, index) =>
        index > invocationIndex &&
        statementContainsMatchedUiOutcomeAssertion(statement, consumerOutcomeProof),
    );
  });
  return proven;
}

function malformedFacadeMockReceiver(statement, facadeName) {
  let receiver = "";
  audit.traverseAst(statement, (candidate) => {
    if (
      receiver ||
      candidate.type !== "CallExpression" ||
      candidate.arguments.length !== 1 ||
      candidate.callee.type !== "MemberExpression" ||
      candidate.callee.computed ||
      candidate.callee.property.type !== "Identifier" ||
      (candidate.callee.property.name !== "mockResolvedValue" &&
        candidate.callee.property.name !== "mockResolvedValueOnce")
    )
      return;
    const mockedFacade = candidate.callee.object;
    if (
      mockedFacade.type !== "MemberExpression" ||
      mockedFacade.computed ||
      mockedFacade.property.type !== "Identifier" ||
      mockedFacade.property.name !== facadeName
    )
      return;
    if (mockedFacade.object.type === "Identifier" && hasMalformedSentinel(candidate.arguments[0])) {
      receiver = mockedFacade.object.name;
    }
  });
  return receiver;
}

function hasMalformedSentinel(node) {
  if (node?.type === "StringLiteral") return /malformed|unexpected|sentinel/i.test(node.value);
  if (node?.type === "ArrayExpression") return node.elements.some(hasMalformedSentinel);
  if (node?.type !== "ObjectExpression") return false;
  return node.properties.some((property) => {
    if (property.type !== "ObjectProperty") return false;
    return (
      /malformed|unexpected|sentinel/i.test(audit.staticPropertyKeyName(property) ?? "") ||
      hasMalformedSentinel(property.value)
    );
  });
}

function statementContainsPageTrigger(statement) {
  let found = false;
  audit.traverseAst(statement, (candidate) => {
    if (
      candidate.type === "CallExpression" &&
      candidate.callee.type === "MemberExpression" &&
      !candidate.callee.computed &&
      candidate.callee.object.type === "Identifier" &&
      (candidate.callee.object.name === "fireEvent" || candidate.callee.object.name === "userEvent")
    )
      found = true;
  });
  return found;
}

function statementContainsExactFacadeInvocationAssertion(statement, receiver, facadeName) {
  let found = false;
  audit.traverseAst(statement, (candidate) => {
    if (
      found ||
      candidate.type !== "CallExpression" ||
      candidate.callee.type !== "MemberExpression" ||
      candidate.callee.computed ||
      candidate.callee.property.type !== "Identifier" ||
      !/^toHaveBeenCalled/.test(candidate.callee.property.name)
    )
      return;
    audit.traverseAst(candidate.callee.object, (asserted) => {
      if (
        asserted.type === "CallExpression" &&
        asserted.callee.type === "Identifier" &&
        asserted.callee.name === "expect" &&
        asserted.arguments.length === 1 &&
        asserted.arguments[0].type === "MemberExpression" &&
        !asserted.arguments[0].computed &&
        asserted.arguments[0].object.type === "Identifier" &&
        asserted.arguments[0].object.name === receiver &&
        asserted.arguments[0].property.type === "Identifier" &&
        asserted.arguments[0].property.name === facadeName
      )
        found = true;
    });
  });
  return found;
}

function statementContainsMatchedUiOutcomeAssertion(statement, proof) {
  let matched = false;
  audit.traverseAst(statement, (candidate) => {
    if (
      matched ||
      candidate.type !== "CallExpression" ||
      candidate.callee.type !== "MemberExpression" ||
      candidate.callee.computed ||
      candidate.callee.property.type !== "Identifier"
    )
      return;
    const matcherName = candidate.callee.property.name;
    let expectCall = null;
    audit.traverseAst(candidate.callee.object, (chainNode) => {
      if (
        chainNode.type === "CallExpression" &&
        chainNode.callee.type === "Identifier" &&
        chainNode.callee.name === "expect" &&
        chainNode.arguments.length === 1
      )
        expectCall = chainNode;
    });
    if (!expectCall) return;
    let screenCall = null;
    audit.traverseAst(expectCall.arguments[0], (asserted) => {
      if (
        asserted.type === "CallExpression" &&
        asserted.callee.type === "MemberExpression" &&
        !asserted.callee.computed &&
        asserted.callee.object.type === "Identifier" &&
        asserted.callee.object.name === "screen" &&
        asserted.callee.property.type === "Identifier" &&
        /^(?:find|get|query)(?:All)?By/.test(asserted.callee.property.name)
      )
        screenCall = asserted;
    });
    if (!screenCall) return;
    const negative =
      audit.memberChainContainsName(candidate.callee.object, "not") || matcherName === "toBeNull";
    const query = exactScreenQueryDescriptor(screenCall);
    if (
      negative &&
      query &&
      proof.controlledUi.some(
        (control) => control.role === query.role && control.name === query.name,
      )
    )
      matched = true;
  });
  return matched;
}

function exactScreenQueryDescriptor(screenCall) {
  if (
    screenCall.callee.type !== "MemberExpression" ||
    screenCall.callee.property.type !== "Identifier" ||
    !/ByRole$/.test(screenCall.callee.property.name) ||
    screenCall.arguments[0]?.type !== "StringLiteral" ||
    screenCall.arguments[1]?.type !== "ObjectExpression"
  )
    return null;
  const nameProperty = screenCall.arguments[1].properties.find(
    (property) =>
      property.type === "ObjectProperty" && audit.staticPropertyKeyName(property) === "name",
  );
  if (nameProperty?.type !== "ObjectProperty" || nameProperty.value.type !== "StringLiteral")
    return null;
  return { role: screenCall.arguments[0].value, name: nameProperty.value.value };
}

function nodeContainsNode(node, target) {
  let found = false;
  audit.traverseAst(node, (candidate) => {
    if (candidate === target) found = true;
  });
  return found;
}

function hasResultHandledRegressionEvidence(ast, testPath, symbol, consumerAliases, entry) {
  if (
    audit.isExactTurnInterruptPolicy(entry) &&
    audit.hasRuntimeResultHandledRegressionEvidence(ast, testPath, symbol, entry)
  )
    return true;
  const facadeName = entry.facade.split(".").at(-1);
  let mockedError = "";
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
      !audit.moduleSpecifierResolvesTo(testPath, call.arguments[0].value, audit.RPC_FACADE_PATH)
    )
      continue;
    const factory = call.arguments[1];
    if (factory?.type !== "ArrowFunctionExpression" && factory?.type !== "FunctionExpression")
      continue;
    const exports = audit.functionReturnedObject(factory);
    const facade = exports?.properties.find(
      (property) =>
        property.type === "ObjectProperty" && audit.staticPropertyKeyName(property) === facadeName,
    );
    const response =
      facade?.type === "ObjectProperty" ? audit.findMockResolvedValueArgument(facade.value) : null;
    if (response?.type !== "ObjectExpression") continue;
    const ok = response.properties.find(
      (property) =>
        property.type === "ObjectProperty" && audit.staticPropertyKeyName(property) === "ok",
    );
    const error = response.properties.find(
      (property) =>
        property.type === "ObjectProperty" && audit.staticPropertyKeyName(property) === "error",
    );
    if (
      ok?.value.type === "BooleanLiteral" &&
      ok.value.value === false &&
      error?.value.type === "StringLiteral"
    ) {
      mockedError = error.value.value;
    }
  }
  if (!mockedError) return false;
  const warningProducerAliases = new Set();
  const handlerLocator = entry.responsePolicy?.handler;
  for (const statement of ast.program.body) {
    if (
      !handlerLocator ||
      statement.type !== "ImportDeclaration" ||
      !audit.moduleSpecifierResolvesTo(testPath, statement.source.value, handlerLocator.path)
    )
      continue;
    for (const specifier of statement.specifiers) {
      if (
        specifier.type === "ImportSpecifier" &&
        audit.moduleExportName(specifier.imported) === handlerLocator.symbol
      )
        warningProducerAliases.add(specifier.local.name);
    }
  }
  let proven = false;
  audit.traverseAst(ast, (node) => {
    if (
      proven ||
      node.type !== "CallExpression" ||
      node.callee.type !== "Identifier" ||
      (node.callee.name !== "it" && node.callee.name !== "test") ||
      node.arguments[0]?.type !== "StringLiteral" ||
      node.arguments[0].value !== symbol ||
      audit.hasNonTestRunnerBinding(ast, node.callee.name)
    )
      return;
    const callback = node.arguments[1];
    if (
      (callback?.type !== "ArrowFunctionExpression" && callback?.type !== "FunctionExpression") ||
      callback.body.type !== "BlockStatement"
    )
      return;
    let callsConsumer = false;
    let consumerStatementIndex = -1;
    let callsWarningTarget = false;
    const warningSpies = new Set();
    audit.walkAstWithAncestors(callback.body, (candidate, ancestors) => {
      if (ancestors.some((ancestor) => audit.isFunctionNode(ancestor))) return;
      if (
        candidate.type === "CallExpression" &&
        candidate.callee.type === "Identifier" &&
        consumerAliases.has(candidate.callee.name) &&
        !audit.bindingShadowsNameAt([callback, ...ancestors], candidate.callee.name)
      ) {
        const awaited = ancestors.at(-1);
        const statement = ancestors.at(-2);
        const statementParent = ancestors.at(-3);
        if (
          awaited?.type === "AwaitExpression" &&
          statement?.type === "ExpressionStatement" &&
          statementParent === callback.body
        ) {
          callsConsumer = true;
          consumerStatementIndex = callback.body.body.indexOf(statement);
        }
      }
      if (
        candidate.type === "CallExpression" &&
        candidate.callee.type === "Identifier" &&
        warningProducerAliases.has(candidate.callee.name) &&
        !audit.bindingShadowsNameAt([callback, ...ancestors], candidate.callee.name)
      )
        callsWarningTarget = true;
      if (
        candidate.type === "CallExpression" &&
        candidate.callee.type === "MemberExpression" &&
        !candidate.callee.computed &&
        candidate.callee.object.type === "Identifier" &&
        candidate.callee.object.name === "console" &&
        candidate.callee.property.type === "Identifier" &&
        candidate.callee.property.name === "warn"
      )
        callsWarningTarget = true;
      if (candidate.type !== "VariableDeclarator" || candidate.id.type !== "Identifier") return;
      let init = candidate.init;
      while (init?.type === "CallExpression" && init.callee.type === "MemberExpression") {
        if (
          !init.callee.computed &&
          init.callee.object.type === "Identifier" &&
          init.callee.object.name === "vi" &&
          init.callee.property.type === "Identifier" &&
          init.callee.property.name === "spyOn" &&
          init.arguments[0]?.type === "Identifier" &&
          init.arguments[0].name === "console" &&
          init.arguments[1]?.type === "StringLiteral" &&
          init.arguments[1].value === "warn"
        )
          warningSpies.add(candidate.id.name);
        init = init.callee.object;
      }
    });
    if (
      !callsConsumer ||
      consumerStatementIndex < 0 ||
      callsWarningTarget ||
      warningSpies.size === 0
    )
      return;
    audit.walkAstWithAncestors(callback.body, (candidate, ancestors) => {
      if (ancestors.some((ancestor) => audit.isFunctionNode(ancestor))) return;
      if (
        candidate.type === "CallExpression" &&
        candidate.callee.type === "MemberExpression" &&
        !candidate.callee.computed &&
        candidate.callee.property.type === "Identifier" &&
        candidate.callee.property.name === "toHaveBeenCalledWith" &&
        candidate.arguments.length === 1 &&
        candidate.arguments[0].type === "StringLiteral" &&
        candidate.arguments[0].value === mockedError &&
        candidate.callee.object.type === "CallExpression" &&
        candidate.callee.object.callee.type === "Identifier" &&
        candidate.callee.object.callee.name === "expect" &&
        candidate.callee.object.arguments[0]?.type === "Identifier" &&
        warningSpies.has(candidate.callee.object.arguments[0].name)
      ) {
        const assertionStatement = ancestors.at(-1);
        if (
          assertionStatement?.type === "ExpressionStatement" &&
          ancestors.at(-2) === callback.body &&
          callback.body.body.indexOf(assertionStatement) > consumerStatementIndex
        )
          proven = true;
      }
    });
  });
  return proven;
}

export {
  hasPageIgnoredResultRegressionEvidence,
  malformedFacadeMockReceiver,
  hasMalformedSentinel,
  statementContainsPageTrigger,
  statementContainsExactFacadeInvocationAssertion,
  statementContainsMatchedUiOutcomeAssertion,
  exactScreenQueryDescriptor,
  nodeContainsNode,
  hasResultHandledRegressionEvidence,
};
