import * as audit from "../rpc-contract-audit.mjs";
import { hasPublishedCallbackRegressionEvidence } from "./published-callback-regression-evidence.mjs";

function hasRegressionTestEvidence(
  ast,
  testPath,
  symbol,
  consumerLocator,
  policyKind,
  entry,
  consumerOutcomeProof = null,
) {
  const consumerAliases = new Set();
  for (const statement of ast.program.body) {
    if (
      statement.type !== "ImportDeclaration" ||
      !audit.moduleSpecifierResolvesTo(testPath, statement.source.value, consumerLocator.path)
    ) {
      continue;
    }
    for (const specifier of statement.specifiers) {
      if (
        specifier.type === "ImportSpecifier" &&
        audit.moduleExportName(specifier.imported) === consumerLocator.symbol
      ) {
        consumerAliases.add(specifier.local.name);
      }
    }
  }
  if (policyKind === "result-handled") {
    return audit.hasResultHandledRegressionEvidence(ast, testPath, symbol, consumerAliases, entry);
  }
  if (policyKind === "ignored-result" && entry.responsePolicy?.outcome) {
    return hasPublishedCallbackRegressionEvidence(
      ast,
      symbol,
      consumerAliases,
      consumerOutcomeProof,
    );
  }
  if (
    policyKind === "ignored-result" &&
    hasDirectFacadeIgnoredResultRegressionEvidence(ast, symbol, entry)
  )
    return true;
  if (
    policyKind === "ignored-result" &&
    consumerAliases.size === 0 &&
    audit.hasPageIgnoredResultRegressionEvidence(ast, symbol, entry, consumerOutcomeProof)
  )
    return true;
  if (consumerAliases.size === 0) return false;
  const malformedFacadeMocked =
    policyKind !== "consumer-validated" || audit.hasMalformedFacadeMock(ast, testPath, entry);
  let found = false;
  audit.traverseAst(ast, (node) => {
    if (
      found ||
      node.type !== "CallExpression" ||
      node.callee.type !== "Identifier" ||
      (node.callee.name !== "it" && node.callee.name !== "test") ||
      node.arguments[0]?.type !== "StringLiteral" ||
      node.arguments[0].value !== symbol
    ) {
      return;
    }
    const callback = node.arguments[1];
    if (
      (callback?.type !== "ArrowFunctionExpression" && callback?.type !== "FunctionExpression") ||
      callback.body.type !== "BlockStatement" ||
      audit.hasNonTestRunnerBinding(ast, node.callee.name)
    ) {
      return;
    }
    const consumerCalls = new Set();
    const consumerResultNames = new Set();
    audit.walkAstWithAncestors(callback.body, (candidate, ancestors) => {
      if (
        candidate.type === "CallExpression" &&
        candidate.callee.type === "Identifier" &&
        consumerAliases.has(candidate.callee.name) &&
        !audit.bindingShadowsNameAt([callback, ...ancestors], candidate.callee.name)
      ) {
        consumerCalls.add(candidate);
        const parent = ancestors.at(-1);
        const declarator = parent?.type === "AwaitExpression" ? ancestors.at(-2) : parent;
        if (declarator?.type === "VariableDeclarator" && declarator.id.type === "Identifier") {
          consumerResultNames.add(declarator.id.name);
        }
      }
    });
    audit.walkAstWithAncestors(callback.body, (candidate, ancestors) => {
      if (
        found ||
        candidate.type !== "CallExpression" ||
        candidate.callee.type !== "Identifier" ||
        candidate.callee.name !== "expect"
      ) {
        return;
      }
      let tiedToConsumer = false;
      for (const argument of candidate.arguments) {
        audit.traverseAst(argument, (assertedNode) => {
          if (
            consumerCalls.has(assertedNode) ||
            (assertedNode.type === "Identifier" && consumerResultNames.has(assertedNode.name))
          ) {
            tiedToConsumer = true;
          }
        });
      }
      if (!tiedToConsumer) return;
      const matcherCall = ancestors.findLast(
        (ancestor) =>
          ancestor.type === "CallExpression" &&
          ancestor.callee.type === "MemberExpression" &&
          audit.nodeContainsNode(ancestor.callee.object, candidate),
      );
      if (!matcherCall || matcherCall.callee.computed) return;
      const matcherName =
        matcherCall.callee.property.type === "Identifier" ? matcherCall.callee.property.name : "";
      if (policyKind === "ignored-result" && matcherName === "toBeUndefined") found = true;
      if (
        policyKind === "consumer-validated" &&
        malformedFacadeMocked &&
        (matcherName === "toThrow" || matcherName === "toThrowError") &&
        audit.memberChainContainsName(matcherCall.callee.object, "rejects") &&
        matcherCall.arguments.length === 1 &&
        audit.isSpecificShapeFailureMatcher(matcherCall.arguments[0])
      ) {
        found = true;
      }
    });
  });
  return found;
}

function hasDirectFacadeIgnoredResultRegressionEvidence(ast, symbol, entry) {
  const locator = audit.DIRECT_FACADE_RPC_LOCATORS.get(entry.key);
  if (
    !locator ||
    entry.responsePolicy?.consumer?.path !== locator.implementationPath ||
    entry.responsePolicy?.consumer?.symbol !== locator.facade
  )
    return false;
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
    const rejectedMocks = collectRejectedMockBindings(callback.body);
    if (rejectedMocks.size === 0) return;
    let rejectsFacade = false;
    let assertsMethod = false;
    audit.traverseAst(callback.body, (candidate) => {
      if (
        candidate.type !== "CallExpression" ||
        candidate.callee.type !== "MemberExpression" ||
        candidate.callee.computed ||
        candidate.callee.property.type !== "Identifier"
      )
        return;
      if (
        (candidate.callee.property.name === "toThrow" ||
          candidate.callee.property.name === "toThrowError") &&
        audit.memberChainContainsName(candidate.callee.object, "rejects")
      ) {
        audit.traverseAst(candidate.callee.object, (chainNode) => {
          if (
            chainNode.type === "CallExpression" &&
            chainNode.callee.type === "Identifier" &&
            chainNode.callee.name === "expect" &&
            chainNode.arguments.some((argument) => nodeCallsIdentifier(argument, locator.facade))
          )
            rejectsFacade = true;
        });
      }
      if (
        candidate.callee.property.name === "toHaveBeenCalledWith" &&
        candidate.arguments.some(
          (argument) => argument.type === "StringLiteral" && argument.value === locator.method,
        )
      ) {
        audit.traverseAst(candidate.callee.object, (chainNode) => {
          if (
            chainNode.type === "CallExpression" &&
            chainNode.callee.type === "Identifier" &&
            chainNode.callee.name === "expect" &&
            chainNode.arguments.some(
              (argument) => argument.type === "Identifier" && rejectedMocks.has(argument.name),
            )
          )
            assertsMethod = true;
        });
      }
    });
    proven = rejectsFacade && assertsMethod;
  });
  return proven;
}

function collectRejectedMockBindings(node) {
  const bindings = new Set();
  audit.traverseAst(node, (candidate) => {
    if (candidate.type !== "VariableDeclarator" || candidate.id.type !== "Identifier") return;
    const init = candidate.init;
    if (
      init?.type === "CallExpression" &&
      init.callee.type === "MemberExpression" &&
      !init.callee.computed &&
      init.callee.property.type === "Identifier" &&
      init.callee.property.name === "mockRejectedValue" &&
      init.arguments.length === 1
    )
      bindings.add(candidate.id.name);
  });
  return bindings;
}

function nodeCallsIdentifier(node, name) {
  let found = false;
  audit.traverseAst(node, (candidate) => {
    if (
      candidate.type === "CallExpression" &&
      candidate.callee.type === "Identifier" &&
      candidate.callee.name === name
    )
      found = true;
  });
  return found;
}

export {
  hasRegressionTestEvidence,
  hasDirectFacadeIgnoredResultRegressionEvidence,
  collectRejectedMockBindings,
  nodeCallsIdentifier,
};
export {
  publishedCallbackProductionProof,
  nestedFunctionBetween,
  mapLocalPathToConsumerParameter,
  pathsEqual,
  callOccursLaterInSameSuccessBlock,
  hasPublishedCallbackRegressionEvidence,
  callbackStatementIndex,
  equivalentConsumerArgumentPaths,
  bodyHasStaticRootDeclaration,
} from "./published-callback-regression-evidence.mjs";
