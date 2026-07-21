import * as audit from "../rpc-contract-audit.mjs";
import {
  collectStateControlledUiDescriptors,
  functionReturnsStatePair,
  resolveStateOwnerExpression,
  stateSetterBindingsInOwner,
} from "./state-owner-evidence.mjs";

function collectExactResolvedMalformedMocks(body, facadeName) {
  const mocks = [];
  audit.walkAstWithAncestors(body, (candidate, ancestors) => {
    if (
      candidate.type !== "CallExpression" ||
      candidate.arguments.length !== 1 ||
      !audit.isStaticMemberNamed(candidate.callee, "mockResolvedValue") ||
      !audit.hasMalformedSentinel(candidate.arguments[0]) ||
      ancestors.some((ancestor) => audit.isFunctionNode(ancestor))
    )
      return;
    const property = ancestors.findLast((ancestor) => ancestor.type === "ObjectProperty");
    if (!property || audit.staticPropertyKeyName(property) !== facadeName) return;
    const declaration = ancestors.findLast(
      (ancestor) => ancestor.type === "VariableDeclarator" && ancestor.id.type === "Identifier",
    );
    if (!declaration) return;
    const propertyPath = ancestors
      .filter((ancestor) => ancestor.type === "ObjectProperty")
      .map(audit.staticPropertyKeyName)
      .filter(Boolean);
    mocks.push({ node: candidate, path: [declaration.id.name, ...propertyPath] });
  });
  return mocks;
}

function exactSpyMatcher(statement, expectedPath, matcherName, { requireArgument = false } = {}) {
  let matched = false;
  audit.traverseAst(statement, (candidate) => {
    if (
      matched ||
      candidate.type !== "CallExpression" ||
      !audit.isStaticMemberNamed(candidate.callee, matcherName) ||
      (requireArgument && candidate.arguments.length === 0)
    )
      return;
    const expectCall = candidate.callee.object;
    if (
      expectCall.type === "CallExpression" &&
      expectCall.callee.type === "Identifier" &&
      expectCall.callee.name === "expect" &&
      expectCall.arguments.length === 1 &&
      audit.pathsEqual(memberExpressionPath(expectCall.arguments[0]), expectedPath)
    )
      matched = true;
  });
  return matched;
}

function exactUndefinedResultAssertion(statement, resultName) {
  let matched = false;
  audit.traverseAst(statement, (candidate) => {
    if (
      matched ||
      candidate.type !== "CallExpression" ||
      !audit.isStaticMemberNamed(candidate.callee, "toBeUndefined")
    )
      return;
    const expectCall = candidate.callee.object;
    if (
      expectCall.type === "CallExpression" &&
      expectCall.callee.type === "Identifier" &&
      expectCall.callee.name === "expect" &&
      expectCall.arguments.length === 1 &&
      expectCall.arguments[0].type === "Identifier" &&
      expectCall.arguments[0].name === resultName
    )
      matched = true;
  });
  return matched;
}

async function collectIgnoredResultConsumerOutcomeProof(
  auditContext,
  ast,
  consumerSymbol,
  call,
  consumerPath,
) {
  const dismissals = [];
  audit.traverseAst(consumerSymbol, (node) => {
    if (node.type !== "BlockStatement") return;
    const index = node.body.findIndex((statement) => audit.nodeContainsNode(statement, call.node));
    if (index < 0) return;
    for (const statement of node.body.slice(index + 1)) {
      dismissals.push(...collectPostCallStateDismissals(statement));
    }
  });
  const controlledUi = await resolveDismissedStateUiDescriptors(auditContext, dismissals, {
    ast,
    path: consumerPath,
    symbol: consumerSymbol,
  });
  return { controlledUi };
}

function memberExpressionPath(node) {
  const path = [];
  let current = node;
  while (
    current?.type === "MemberExpression" &&
    !current.computed &&
    current.property.type === "Identifier"
  ) {
    path.unshift(current.property.name);
    current = current.object;
  }
  if (current?.type === "Identifier") path.unshift(current.name);
  return path;
}

function collectPostCallStateDismissals(statement) {
  const dismissals = [];
  audit.traverseAst(statement, (candidate) => {
    if (candidate.type !== "CallExpression") return;
    const callee = stateSetterCallee(candidate.callee);
    if (!callee) return;
    for (const argument of candidate.arguments) {
      if (argument.type === "ObjectExpression") {
        for (const property of argument.properties) {
          if (property.type !== "ObjectProperty" || !audit.isDismissingStateValue(property.value))
            continue;
          const key = audit.staticPropertyKeyName(property);
          if (key) dismissals.push({ ...callee, clearedKey: key });
        }
      } else if (audit.isDismissingStateValue(argument)) {
        dismissals.push({ ...callee, clearedKey: "" });
      }
    }
  });
  return dismissals;
}

function stateSetterCallee(callee) {
  if (callee.type === "Identifier" && /^set[A-Z]/.test(callee.name)) {
    return { kind: "local", setterName: callee.name };
  }
  if (
    callee.type === "MemberExpression" &&
    !callee.computed &&
    callee.object.type === "Identifier" &&
    callee.property.type === "Identifier" &&
    /^set[A-Z]/.test(callee.property.name)
  ) {
    return { kind: "member", objectName: callee.object.name, setterName: callee.property.name };
  }
  return null;
}

async function resolveDismissedStateUiDescriptors(auditContext, dismissals, consumerSource) {
  if (dismissals.length === 0) return [];
  const sources = await frontendProductionAstSources(auditContext);
  const descriptors = new Map();
  for (const dismissal of dismissals) {
    const bindings = findExactStateSetterBindings(sources, dismissal, consumerSource);
    if (bindings.length !== 1) continue;
    const binding = bindings[0];
    const stateAccess = {
      bindingPath: binding.source.path,
      stateName: binding.stateName,
      stateProperty: dismissal.clearedKey,
      returnedProperty: binding.returnedProperty,
      setterName: dismissal.setterName,
    };
    for (const descriptor of collectStateControlledUiDescriptors(sources, stateAccess)) {
      descriptors.set(`${descriptor.role}\u0000${descriptor.name}`, descriptor);
    }
  }
  return [...descriptors.values()];
}

async function frontendProductionAstSources(auditContext) {
  if (auditContext.productionAstSources) return auditContext.productionAstSources;
  const files = await audit.listJavaScriptSourceFiles(
    audit.join(auditContext.repoRoot, "frontend-app/src"),
  );
  const sources = [];
  for (const absolutePath of files) {
    const path = audit.relative(auditContext.repoRoot, absolutePath).replaceAll("\\", "/");
    if (audit.isExcludedProductionScanPath(path)) continue;
    sources.push({ path, ast: await audit.readAuditAst(auditContext, path) });
  }
  auditContext.productionAstSources = sources;
  return sources;
}

function findExactStateSetterBindings(sources, dismissal, consumerSource) {
  if (dismissal.kind === "member") {
    const owners = resolveMemberObjectStateOwners(
      sources,
      consumerSource.source ?? consumerSource,
      consumerSource.symbol,
      dismissal.objectName,
      new Set(),
    );
    const bindings = owners.flatMap(({ source, owner }) =>
      stateSetterBindingsInOwner(source, owner, dismissal.setterName, true),
    );
    return bindings.length === 1 ? bindings : [];
  }
  const bindings = [];
  for (const source of sources) {
    if (source.path !== consumerSource.path) continue;
    audit.walkAstWithAncestors(source.ast, (candidate, ancestors) => {
      if (
        candidate.type !== "VariableDeclarator" ||
        candidate.id.type !== "ArrayPattern" ||
        candidate.id.elements[0]?.type !== "Identifier" ||
        candidate.id.elements[1]?.type !== "Identifier" ||
        candidate.id.elements[1].name !== dismissal.setterName ||
        candidate.init?.type !== "CallExpression" ||
        candidate.init.callee.type !== "Identifier" ||
        candidate.init.callee.name !== "useState"
      )
        return;
      const owner = ancestors.findLast((ancestor) => audit.isFunctionNode(ancestor));
      if (!owner) return;
      const stateName = candidate.id.elements[0].name;
      const returnedProperty = functionReturnsStatePair(owner, stateName, dismissal.setterName);
      bindings.push({ stateName, returnedProperty, source, owner });
    });
  }
  return bindings;
}

function resolveMemberObjectStateOwners(sources, source, owner, objectName, visited) {
  if (!owner || !objectName) return [];
  const visitKey = `${source.path}:${owner.start ?? ""}:${objectName}`;
  if (visited.has(visitKey)) return [];
  const nextVisited = new Set(visited);
  nextVisited.add(visitKey);

  for (let index = 0; index < owner.params.length; index += 1) {
    const parameter = owner.params[index];
    if (parameter.type === "ObjectPattern") {
      const property = parameter.properties.find(
        (candidate) =>
          candidate.type === "ObjectProperty" &&
          candidate.value.type === "Identifier" &&
          candidate.value.name === objectName,
      );
      if (property) {
        return resolveUniqueFunctionCallArgument(
          sources,
          owner,
          index,
          audit.staticPropertyKeyName(property),
          nextVisited,
        );
      }
    }
    if (parameter.type === "Identifier") {
      let propertyName = "";
      audit.traverseAst(owner.body, (candidate) => {
        if (
          candidate.type !== "VariableDeclarator" ||
          candidate.id.type !== "ObjectPattern" ||
          candidate.init?.type !== "Identifier" ||
          candidate.init.name !== parameter.name
        )
          return;
        const property = candidate.id.properties.find(
          (item) =>
            item.type === "ObjectProperty" &&
            item.value.type === "Identifier" &&
            item.value.name === objectName,
        );
        if (property) propertyName = audit.staticPropertyKeyName(property);
      });
      if (propertyName) {
        return resolveUniqueFunctionCallArgument(sources, owner, index, propertyName, nextVisited);
      }
    }
  }

  let initializer = null;
  audit.traverseAst(owner.body, (candidate) => {
    if (
      !initializer &&
      candidate.type === "VariableDeclarator" &&
      candidate.id.type === "Identifier" &&
      candidate.id.name === objectName
    )
      initializer = candidate.init;
  });
  return resolveStateOwnerExpression(sources, source, owner, initializer, nextVisited);
}

function resolveUniqueFunctionCallArgument(sources, owner, parameterIndex, propertyName, visited) {
  const functionName = owner.id?.type === "Identifier" ? owner.id.name : "";
  if (!functionName) return [];
  const values = [];
  for (const source of sources) {
    audit.walkAstWithAncestors(source.ast, (candidate, ancestors) => {
      if (
        candidate.type !== "CallExpression" ||
        candidate.callee.type !== "Identifier" ||
        candidate.callee.name !== functionName
      )
        return;
      let value = candidate.arguments[parameterIndex];
      if (propertyName) {
        if (value?.type !== "ObjectExpression") return;
        const property = value.properties.find(
          (item) =>
            item.type === "ObjectProperty" && audit.staticPropertyKeyName(item) === propertyName,
        );
        value = property?.type === "ObjectProperty" ? property.value : null;
      }
      const callOwner = ancestors.findLast((ancestor) => audit.isFunctionNode(ancestor));
      if (value && callOwner) values.push({ source, owner: callOwner, value });
    });
  }
  if (values.length !== 1) return [];
  const value = values[0];
  return resolveStateOwnerExpression(sources, value.source, value.owner, value.value, visited);
}

export {
  collectExactResolvedMalformedMocks,
  exactSpyMatcher,
  exactUndefinedResultAssertion,
  collectIgnoredResultConsumerOutcomeProof,
  memberExpressionPath,
  collectPostCallStateDismissals,
  stateSetterCallee,
  resolveDismissedStateUiDescriptors,
  frontendProductionAstSources,
  findExactStateSetterBindings,
  resolveMemberObjectStateOwners,
  resolveUniqueFunctionCallArgument,
};
export {
  resolveStateOwnerExpression,
  stateSetterBindingsInOwner,
  functionReturnsStatePair,
  collectStateControlledUiDescriptors,
  returnedStateObjectNamesByPath,
  functionContainsMemberCall,
  functionAcceptsProperty,
} from "./state-owner-evidence.mjs";
