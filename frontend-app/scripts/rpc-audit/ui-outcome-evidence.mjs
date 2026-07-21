import * as audit from "../rpc-contract-audit.mjs";

function nodeContainsStateAccess(node, access, sourcePath, returnedObjectNames) {
  let found = false;
  audit.traverseAst(node, (candidate) => {
    if (found) return;
    if (
      sourcePath === access.bindingPath &&
      !access.stateProperty &&
      candidate.type === "Identifier" &&
      candidate.name === access.stateName
    )
      found = true;
    if (
      candidate.type === "MemberExpression" &&
      !candidate.computed &&
      candidate.property.type === "Identifier" &&
      ((sourcePath === access.bindingPath &&
        access.stateProperty &&
        candidate.object.type === "Identifier" &&
        candidate.object.name === access.stateName &&
        candidate.property.name === access.stateProperty) ||
        (!access.stateProperty &&
          access.returnedProperty &&
          candidate.object.type === "Identifier" &&
          returnedObjectNames.get(sourcePath)?.has(candidate.object.name) &&
          candidate.property.name === access.returnedProperty))
    )
      found = true;
  });
  return found;
}

function nodeContainsJsxElement(node) {
  let found = false;
  audit.traverseAst(node, (candidate) => {
    if (candidate.type === "JSXElement") found = true;
  });
  return found;
}

function nodeContainsAlias(node, aliases) {
  let found = false;
  audit.traverseAst(node, (candidate) => {
    if (candidate.type === "Identifier" && aliases.has(candidate.name)) found = true;
  });
  return found;
}

function uiDescriptorsFromControlledNode(node, ast, sources, visited) {
  const descriptors = [];
  audit.traverseAst(node, (candidate) => {
    if (candidate.type !== "JSXElement") return;
    const opening = candidate.openingElement;
    const name = opening.name.type === "JSXIdentifier" ? opening.name.name : "";
    if (!name) return;
    const names = jsxStaticAttributeValues(opening, ["aria-label", "ariaLabel"], ast);
    const role =
      jsxStaticAttribute(opening, "role", ast) ||
      intrinsicJsxRole(name) ||
      (/Dialog$/.test(name) && names.length > 0 ? "dialog" : "");
    if (role) {
      const visibleNames = names.length > 0 ? names : collectJsxVisibleTextValues(candidate, ast);
      for (const visibleName of visibleNames) descriptors.push({ role, name: visibleName });
    }
    if (/^[A-Z]/.test(name)) {
      const visitKey = name;
      if (visited.has(visitKey)) return;
      const definition = findUniqueFunctionDefinition(sources, name);
      if (!definition) return;
      const nextVisited = new Set(visited);
      nextVisited.add(visitKey);
      descriptors.push(
        ...uiDescriptorsFromControlledNode(
          definition.node.body,
          definition.source.ast,
          sources,
          nextVisited,
        ),
      );
    }
  });
  return descriptors;
}

function collectJsxVisibleTextValues(element, ast) {
  const values = new Set();
  for (const child of element.children ?? []) {
    if (child.type === "JSXText" && child.value.trim()) values.add(child.value.trim());
    if (child.type === "JSXExpressionContainer") {
      for (const text of collectStaticTextValues(child.expression, ast)) values.add(text);
    }
    if (child.type === "JSXElement") {
      for (const text of collectJsxVisibleTextValues(child, ast)) values.add(text);
    }
  }
  return [...values];
}

function jsxStaticAttribute(opening, name, ast) {
  return jsxStaticAttributeValues(opening, [name], ast)[0] ?? "";
}

function jsxStaticAttributeValues(opening, names, ast) {
  const values = new Set();
  for (const attribute of opening.attributes) {
    if (
      attribute.type !== "JSXAttribute" ||
      attribute.name.type !== "JSXIdentifier" ||
      !names.includes(attribute.name.name)
    )
      continue;
    if (attribute.value?.type === "StringLiteral") values.add(attribute.value.value);
    if (attribute.value?.type === "JSXExpressionContainer") {
      for (const text of collectStaticTextValues(attribute.value.expression, ast)) values.add(text);
    }
  }
  return [...values];
}

function intrinsicJsxRole(name) {
  if (name === "button") return "button";
  if (name === "dialog") return "dialog";
  return "";
}

const functionDefinitionsBySources = new WeakMap();

function findUniqueFunctionDefinition(sources, name) {
  let definitionsByName = functionDefinitionsBySources.get(sources);
  if (!definitionsByName) {
    definitionsByName = new Map();
    for (const source of sources) {
      audit.traverseAst(source.ast, (candidate) => {
        const candidateName = candidate.type === "FunctionDeclaration" ? candidate.id?.name : "";
        if (!candidateName) return;
        const definitions = definitionsByName.get(candidateName) ?? [];
        definitions.push({ node: candidate, source });
        definitionsByName.set(candidateName, definitions);
      });
    }
    functionDefinitionsBySources.set(sources, definitionsByName);
  }
  const definitions = definitionsByName.get(name) ?? [];
  return definitions.length === 1 ? definitions[0] : null;
}

function collectStaticTextValues(node, ast, visited = new Set()) {
  const values = new Set();
  audit.traverseAst(node, (candidate) => {
    if (candidate.type === "StringLiteral" && candidate.value.trim())
      values.add(candidate.value.trim());
    if (candidate.type === "TemplateElement" && candidate.value.cooked?.trim())
      values.add(candidate.value.cooked.trim());
    if (candidate.type === "RegExpLiteral" && candidate.pattern.trim())
      values.add(candidate.pattern.trim());
    if (
      candidate.type !== "Identifier" &&
      candidate.type !== "MemberExpression" &&
      candidate.type !== "CallExpression"
    )
      return;
    const resolved = resolveStaticValueNode(ast, candidate, visited);
    if (!resolved || resolved === candidate) return;
    const key = `${resolved.start ?? ""}:${resolved.end ?? ""}`;
    if (visited.has(key)) return;
    const nextVisited = new Set(visited);
    nextVisited.add(key);
    for (const text of collectStaticTextValues(resolved, ast, nextVisited)) values.add(text);
  });
  return values;
}

function resolveStaticValueNode(ast, node) {
  if (node.type === "Identifier") return findModuleVariableInitializer(ast, node.name);
  if (node.type === "CallExpression" && node.callee.type === "Identifier") {
    return findModuleFunctionDeclaration(ast, node.callee.name)?.body ?? null;
  }
  if (
    node.type !== "MemberExpression" ||
    node.computed ||
    node.object.type !== "Identifier" ||
    node.property.type !== "Identifier"
  )
    return null;
  let object = findModuleVariableInitializer(ast, node.object.name);
  if (object?.type === "CallExpression" && object.arguments.length === 1)
    object = object.arguments[0];
  if (object?.type !== "ObjectExpression") return null;
  const property = object.properties.find(
    (candidate) => audit.staticPropertyKeyName(candidate) === node.property.name,
  );
  return property?.type === "ObjectProperty" ? property.value : null;
}

function findModuleFunctionDeclaration(ast, name) {
  for (const statement of ast.program.body) {
    const declaration =
      statement.type === "ExportNamedDeclaration" ? statement.declaration : statement;
    if (declaration?.type === "FunctionDeclaration" && declaration.id?.name === name)
      return declaration;
  }
  return null;
}

function findModuleVariableInitializer(ast, name) {
  for (const statement of ast.program.body) {
    const declaration =
      statement.type === "ExportNamedDeclaration" ? statement.declaration : statement;
    if (declaration?.type !== "VariableDeclaration") continue;
    for (const item of declaration.declarations) {
      if (item.id.type === "Identifier" && item.id.name === name) return item.init;
    }
  }
  return null;
}

function isDismissingStateValue(node) {
  if (node?.type === "NullLiteral") return true;
  if (node?.type === "BooleanLiteral") return node.value === false;
  if (node?.type === "StringLiteral") return node.value === "";
  if (node?.type !== "ObjectExpression") return false;
  return node.properties.some(
    (property) => property.type === "ObjectProperty" && isDismissingStateValue(property.value),
  );
}

export {
  nodeContainsStateAccess,
  nodeContainsJsxElement,
  nodeContainsAlias,
  uiDescriptorsFromControlledNode,
  collectJsxVisibleTextValues,
  jsxStaticAttribute,
  jsxStaticAttributeValues,
  intrinsicJsxRole,
  findUniqueFunctionDefinition,
  collectStaticTextValues,
  resolveStaticValueNode,
  findModuleFunctionDeclaration,
  findModuleVariableInitializer,
  isDismissingStateValue,
};
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
} from "./response-policy-regression-evidence.mjs";
