import * as audit from "../rpc-contract-audit.mjs";
import { resolveMemberObjectStateOwners } from "./state-dismissal-evidence.mjs";

function resolveStateOwnerExpression(sources, source, owner, expression, visited) {
  if (expression?.type === "Identifier") {
    return resolveMemberObjectStateOwners(sources, source, owner, expression.name, visited);
  }
  if (expression?.type !== "CallExpression" || expression.callee.type !== "Identifier") return [];
  const definition = audit.findUniqueFunctionDefinition(sources, expression.callee.name);
  return definition ? [{ source: definition.source, owner: definition.node }] : [];
}

function stateSetterBindingsInOwner(source, owner, setterName, requireReturnedPair) {
  const bindings = [];
  audit.walkAstWithAncestors(owner.body, (candidate, ancestors) => {
    if (
      ancestors.some((ancestor) => ancestor !== owner.body && audit.isFunctionNode(ancestor)) ||
      candidate.type !== "VariableDeclarator" ||
      candidate.id.type !== "ArrayPattern" ||
      candidate.id.elements[0]?.type !== "Identifier" ||
      candidate.id.elements[1]?.type !== "Identifier" ||
      candidate.id.elements[1].name !== setterName ||
      candidate.init?.type !== "CallExpression" ||
      candidate.init.callee.type !== "Identifier" ||
      candidate.init.callee.name !== "useState"
    )
      return;
    const stateName = candidate.id.elements[0].name;
    const returnedProperty = functionReturnsStatePair(owner, stateName, setterName);
    if (requireReturnedPair && !returnedProperty) return;
    bindings.push({ stateName, returnedProperty, source, owner });
  });
  return bindings;
}

function functionReturnsStatePair(owner, stateName, setterName) {
  let stateProperty = "";
  let setterReturned = false;
  audit.traverseAst(owner.body, (candidate) => {
    if (candidate.type !== "ReturnStatement" || candidate.argument?.type !== "ObjectExpression")
      return;
    for (const property of candidate.argument.properties) {
      if (property.type !== "ObjectProperty") continue;
      const key = audit.staticPropertyKeyName(property);
      if (property.value.type === "Identifier" && property.value.name === stateName)
        stateProperty = key;
      if (property.value.type === "Identifier" && property.value.name === setterName)
        setterReturned = true;
    }
  });
  return setterReturned ? stateProperty : "";
}

function collectStateControlledUiDescriptors(sources, stateAccess) {
  const descriptors = [];
  const returnedObjectNames = returnedStateObjectNamesByPath(sources, stateAccess);
  const aliasesByPath = new Map();
  const controlledCandidates = [];
  for (const source of sources) {
    audit.walkAstWithAncestors(source.ast, (candidate, ancestors) => {
      if (candidate.type === "JSXAttribute" && candidate.name.type === "JSXIdentifier") {
        const expression =
          candidate.value?.type === "JSXExpressionContainer" ? candidate.value.expression : null;
        if (
          expression &&
          audit.nodeContainsStateAccess(expression, stateAccess, source.path, returnedObjectNames)
        ) {
          const opening = ancestors.findLast((ancestor) => ancestor.type === "JSXOpeningElement");
          const componentName = opening?.name?.type === "JSXIdentifier" ? opening.name.name : "";
          const definition = /^[A-Z]/.test(componentName)
            ? audit.findUniqueFunctionDefinition(sources, componentName)
            : null;
          if (definition && functionAcceptsProperty(definition.node, candidate.name.name)) {
            const aliases = aliasesByPath.get(definition.source.path) ?? new Set();
            aliases.add(candidate.name.name);
            aliasesByPath.set(definition.source.path, aliases);
          }
        }
      }
      const test =
        candidate.type === "ConditionalExpression"
          ? candidate.test
          : candidate.type === "LogicalExpression" && candidate.operator === "&&"
            ? candidate.left
            : null;
      if (!test) return;
      controlledCandidates.push({
        candidate,
        enclosingElement: ancestors.findLast((ancestor) => ancestor.type === "JSXElement"),
        source,
        test,
      });
    });
  }
  for (const { candidate, enclosingElement, source, test } of controlledCandidates) {
    if (
      !audit.nodeContainsStateAccess(test, stateAccess, source.path, returnedObjectNames) &&
      !audit.nodeContainsAlias(test, aliasesByPath.get(source.path) ?? new Set())
    )
      continue;
    const branch =
      candidate.type === "ConditionalExpression" ? candidate.consequent : candidate.right;
    const controlled = audit.nodeContainsJsxElement(branch) ? branch : enclosingElement;
    if (!controlled) continue;
    descriptors.push(
      ...audit.uiDescriptorsFromControlledNode(controlled, source.ast, sources, new Set()),
    );
  }
  return descriptors;
}

function returnedStateObjectNamesByPath(sources, access) {
  const names = new Map();
  if (!access.returnedProperty) return names;
  for (const source of sources) {
    audit.walkAstWithAncestors(source.ast, (candidate, ancestors) => {
      if (
        candidate.type !== "MemberExpression" ||
        candidate.computed ||
        candidate.object.type !== "Identifier" ||
        candidate.property.type !== "Identifier" ||
        candidate.property.name !== access.returnedProperty
      )
        return;
      const owner = ancestors.findLast((ancestor) => audit.isFunctionNode(ancestor));
      if (!owner || !functionContainsMemberCall(owner, candidate.object.name, access.setterName))
        return;
      const sourceNames = names.get(source.path) ?? new Set();
      sourceNames.add(candidate.object.name);
      names.set(source.path, sourceNames);
    });
  }
  return names;
}

function functionContainsMemberCall(owner, objectName, memberName) {
  let found = false;
  audit.traverseAst(owner.body, (candidate) => {
    if (
      candidate.type === "MemberExpression" &&
      !candidate.computed &&
      candidate.object.type === "Identifier" &&
      candidate.object.name === objectName &&
      candidate.property.type === "Identifier" &&
      candidate.property.name === memberName
    )
      found = true;
  });
  return found;
}

function functionAcceptsProperty(owner, propertyName) {
  return owner.params.some((parameter) => {
    if (parameter.type === "ObjectPattern") {
      return parameter.properties.some(
        (property) =>
          property.type === "ObjectProperty" &&
          audit.staticPropertyKeyName(property) === propertyName,
      );
    }
    if (parameter.type !== "Identifier") return false;
    let accepted = false;
    audit.traverseAst(owner.body, (candidate) => {
      if (
        candidate.type === "VariableDeclarator" &&
        candidate.id.type === "ObjectPattern" &&
        candidate.init?.type === "Identifier" &&
        candidate.init.name === parameter.name &&
        candidate.id.properties.some(
          (property) =>
            property.type === "ObjectProperty" &&
            audit.staticPropertyKeyName(property) === propertyName,
        )
      )
        accepted = true;
    });
    return accepted;
  });
}

export {
  resolveStateOwnerExpression,
  stateSetterBindingsInOwner,
  functionReturnsStatePair,
  collectStateControlledUiDescriptors,
  returnedStateObjectNamesByPath,
  functionContainsMemberCall,
  functionAcceptsProperty,
};
