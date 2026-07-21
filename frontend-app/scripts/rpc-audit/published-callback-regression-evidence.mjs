import * as audit from "../rpc-contract-audit.mjs";

async function publishedCallbackProductionProof(
  auditContext,
  ast,
  consumerPath,
  consumerSymbol,
  outcome,
  entry,
) {
  if (!consumerSymbol?.body || !Array.isArray(outcome?.target) || outcome.target.length < 2)
    return null;
  const facadeName = entry.facade.split(".").at(-1);
  const facadeCandidates = [];
  const publisherCandidates = [];
  audit.walkAstWithAncestors(consumerSymbol.body, (node, ancestors) => {
    if (node.type !== "CallExpression" || nestedFunctionBetween(consumerSymbol, ancestors)) return;
    const calleePath = audit.memberExpressionPath(node.callee);
    if (calleePath.length === 0) return;
    if (calleePath.at(-1) === facadeName) {
      const mapped = mapLocalPathToConsumerParameter(consumerSymbol, calleePath, ancestors);
      if (mapped && mapped.path.at(-2) === "facade")
        facadeCandidates.push({ node, ancestors, mapped });
    }
    if (pathsEqual(calleePath, outcome.target)) {
      const mapped = mapLocalPathToConsumerParameter(consumerSymbol, calleePath, ancestors);
      if (mapped && node.arguments.length > 0)
        publisherCandidates.push({ node, ancestors, mapped });
    }
  });
  if (facadeCandidates.length !== 1) return null;
  const facade = facadeCandidates[0];
  const effectiveFacade = await audit.promoteTransparentPromiseWrapperCall(
    auditContext,
    ast,
    consumerPath,
    facade,
  );
  const postPublishers = publisherCandidates.filter((publisher) =>
    callOccursLaterInSameSuccessBlock(effectiveFacade, publisher),
  );
  if (postPublishers.length !== 1 || !audit.isIgnoredCallResult(effectiveFacade.ancestors))
    return null;
  const publisher = postPublishers[0];
  return {
    kind: "published-callback",
    call: effectiveFacade,
    facadeTarget: facade.mapped,
    publisherTarget: publisher.mapped,
  };
}

function nestedFunctionBetween(owner, ancestors) {
  return ancestors.some((ancestor) => audit.isFunctionNode(ancestor) && ancestor !== owner);
}

function mapLocalPathToConsumerParameter(consumerSymbol, path, ancestors) {
  const root = path[0];
  if (audit.bindingShadowsNameAt(ancestors, root)) return null;
  for (let parameterIndex = 0; parameterIndex < consumerSymbol.params.length; parameterIndex += 1) {
    const parameter = consumerSymbol.params[parameterIndex];
    if (parameter.type === "Identifier" && parameter.name === root) {
      return { parameterIndex, path: path.slice(1) };
    }
    if (parameter.type !== "ObjectPattern") continue;
    for (const property of parameter.properties) {
      if (
        property.type === "ObjectProperty" &&
        property.value.type === "Identifier" &&
        property.value.name === root
      ) {
        const externalName = audit.staticPropertyKeyName(property);
        if (externalName) return { parameterIndex, path: [externalName, ...path.slice(1)] };
      }
    }
  }
  return null;
}

function pathsEqual(left, right) {
  return left.length === right.length && left.every((part, index) => part === right[index]);
}

function callOccursLaterInSameSuccessBlock(earlier, later) {
  for (let index = 0; index < earlier.ancestors.length; index += 1) {
    const block = earlier.ancestors[index];
    if (block.type !== "BlockStatement") continue;
    const earlierStatement = earlier.ancestors[index + 1];
    const earlierIndex = block.body.indexOf(earlierStatement);
    if (earlierIndex < 0) continue;
    const laterBlockIndex = later.ancestors.indexOf(block);
    if (laterBlockIndex < 0) continue;
    const laterStatement = later.ancestors[laterBlockIndex + 1];
    const laterIndex = block.body.indexOf(laterStatement);
    if (laterIndex > earlierIndex) return true;
  }
  return false;
}

function hasPublishedCallbackRegressionEvidence(ast, symbol, consumerAliases, proof) {
  if (!proof || consumerAliases.size !== 1) return false;
  const [consumerAlias] = consumerAliases;
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
      !audit.isFunctionNode(callback) ||
      !callback.async ||
      callback.body.type !== "BlockStatement"
    )
      return;
    const calls = [];
    audit.walkAstWithAncestors(callback.body, (candidate, ancestors) => {
      if (
        candidate.type === "CallExpression" &&
        candidate.callee.type === "Identifier" &&
        candidate.callee.name === consumerAlias &&
        !nestedFunctionBetween(callback, ancestors) &&
        !audit.bindingShadowsNameAt([callback, ...ancestors], consumerAlias)
      )
        calls.push({ node: candidate, ancestors });
    });
    if (calls.length !== 1) return;
    const consumerCall = calls[0];
    const awaited = consumerCall.ancestors.at(-1);
    const declarator = consumerCall.ancestors.at(-2);
    if (
      awaited?.type !== "AwaitExpression" ||
      declarator?.type !== "VariableDeclarator" ||
      declarator.id.type !== "Identifier"
    )
      return;
    const callStatementIndex = callbackStatementIndex(callback.body, consumerCall.node);
    if (callStatementIndex < 0) return;
    const facadeRoot = consumerCall.node.arguments[proof.facadeTarget.parameterIndex];
    const publisherRoot = consumerCall.node.arguments[proof.publisherTarget.parameterIndex];
    if (facadeRoot?.type !== "Identifier" || publisherRoot?.type !== "Identifier") return;
    const facadePaths = equivalentConsumerArgumentPaths(
      callback.body,
      facadeRoot.name,
      proof.facadeTarget.path,
    );
    const publisherPaths = equivalentConsumerArgumentPaths(
      callback.body,
      publisherRoot.name,
      proof.publisherTarget.path,
    );
    const facadeName = proof.facadeTarget.path.at(-1);
    const malformedMocks = audit
      .collectExactResolvedMalformedMocks(callback.body, facadeName)
      .filter(
        (mock) =>
          callbackStatementIndex(callback.body, mock.node) < callStatementIndex &&
          facadePaths.some((path) => pathsEqual(path, mock.path)),
      );
    if (malformedMocks.length !== 1) return;
    const laterStatements = callback.body.body.slice(callStatementIndex + 1);
    const facadeAssertion = laterStatements.some((statement) =>
      facadePaths.some((path) =>
        audit.exactSpyMatcher(statement, path, "toHaveBeenCalledWith", { requireArgument: true }),
      ),
    );
    const publisherAssertion = laterStatements.some((statement) =>
      publisherPaths.some((path) =>
        audit.exactSpyMatcher(statement, path, "toHaveBeenLastCalledWith", {
          requireArgument: true,
        }),
      ),
    );
    const resultAssertion = laterStatements.some((statement) =>
      audit.exactUndefinedResultAssertion(statement, declarator.id.name),
    );
    if (facadeAssertion && publisherAssertion && resultAssertion) proven = true;
  });
  return proven;
}

function callbackStatementIndex(block, node) {
  return block.body.findIndex((statement) => audit.nodeContainsNode(statement, node));
}

function equivalentConsumerArgumentPaths(body, rootName, suffix, visited = new Set()) {
  const paths = [[rootName, ...suffix]];
  if (visited.has(rootName)) return paths;
  const nextVisited = new Set(visited);
  nextVisited.add(rootName);
  for (const statement of body.body) {
    if (statement.type !== "VariableDeclaration") continue;
    for (const declaration of statement.declarations) {
      if (declaration.id.type !== "Identifier" || declaration.id.name !== rootName) continue;
      const objectArguments =
        declaration.init?.type === "CallExpression"
          ? declaration.init.arguments.filter((argument) => argument.type === "ObjectExpression")
          : declaration.init?.type === "ObjectExpression"
            ? [declaration.init]
            : [];
      for (const object of objectArguments) {
        for (let index = object.properties.length - 1; index >= 0; index -= 1) {
          const property = object.properties[index];
          if (
            property.type === "ObjectProperty" &&
            audit.staticPropertyKeyName(property) === suffix[0]
          ) {
            if (property.value.type === "Identifier")
              paths.push([property.value.name, ...suffix.slice(1)]);
            break;
          }
          if (
            property.type === "SpreadElement" &&
            property.argument.type === "Identifier" &&
            bodyHasStaticRootDeclaration(body, property.argument.name)
          ) {
            paths.push(
              ...equivalentConsumerArgumentPaths(body, property.argument.name, suffix, nextVisited),
            );
            break;
          }
        }
      }
    }
  }
  return paths;
}

function bodyHasStaticRootDeclaration(body, name) {
  return body.body.some(
    (statement) =>
      statement.type === "VariableDeclaration" &&
      statement.declarations.some(
        (declaration) =>
          declaration.id.type === "Identifier" &&
          declaration.id.name === name &&
          (declaration.init?.type === "ObjectExpression" ||
            declaration.init?.type === "CallExpression"),
      ),
  );
}

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
};
