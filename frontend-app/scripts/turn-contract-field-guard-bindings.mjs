import { Linter } from "eslint";
import {
  memberPropertyName,
  walkNodeWithParent,
} from "./turn-contract-field-guard-ast.mjs";

const validatorRelativePath =
  "frontend-app/src/shared/contracts/turnContractValidators.js";

export function validatorBindingTarget(callee, bindings) {
  if (callee?.type === "Identifier") {
    const binding = bindings.lexical?.bindingFor(callee);
    return binding ? bindings.identifiers.get(binding) : undefined;
  }
  if (
    callee?.type !== "MemberExpression" ||
    callee.object?.type !== "Identifier"
  )
    return undefined;
  return validatorNamespace(callee.object, bindings)?.get(
    memberPropertyName(callee),
  );
}

export function validatorNamespace(identifier, bindings) {
  const binding =
    identifier?.type === "Identifier" &&
    bindings.lexical?.bindingFor(identifier);
  return binding ? bindings.namespaces.get(binding) : undefined;
}

export function assertValidatorBindingsSafe(ast, bindings, relativePath) {
  const parents = new WeakMap();
  walkNodeWithParent(ast.program, (node, parent) => {
    if (parent) parents.set(node, parent);
    if (node.type !== "Identifier" || isStaticPropertyName(node, parent))
      return;
    const binding = bindings.lexical.bindingFor(node);
    if (binding && bindings.identifiers.has(binding)) {
      if (isControlledValidatorIdentifierUse(node, parent, relativePath))
        return;
      throw new Error(
        `${relativePath} validator binding ${node.name} escapes direct calls or controlled explicit re-exports`,
      );
    }
    const namespace = binding && bindings.namespaces.get(binding);
    if (!namespace) return;
    if (parent?.type === "ImportNamespaceSpecifier" && parent.local === node)
      return;
    if (parent?.type === "MemberExpression" && parent.object === node) {
      const propertyName = memberPropertyName(parent);
      if (!propertyName)
        throw new Error(
          `${relativePath} validator namespace ${node.name} uses a dynamic computed member`,
        );
      if (!namespace.has(propertyName)) return;
      if (
        parents.get(parent)?.type === "CallExpression" &&
        parents.get(parent).callee === parent
      )
        return;
      throw new Error(
        `${relativePath} validator namespace member ${node.name}.${propertyName} escapes direct calls`,
      );
    }
    throw new Error(
      `${relativePath} validator namespace ${node.name} escapes direct calls or controlled explicit re-exports`,
    );
  });
}

function isControlledValidatorIdentifierUse(node, parent, relativePath) {
  if (parent?.type === "CallExpression" && parent.callee === node) return true;
  if (
    parent?.type === "ImportSpecifier" ||
    parent?.type === "ImportDefaultSpecifier" ||
    parent?.type === "ExportSpecifier" ||
    parent?.type === "ExportDefaultDeclaration"
  )
    return true;
  return (
    relativePath === validatorRelativePath &&
    parent?.type === "FunctionDeclaration" &&
    parent.id === node
  );
}

function isStaticPropertyName(node, parent) {
  if (
    (parent?.type === "MemberExpression" ||
      parent?.type === "OptionalMemberExpression") &&
    !parent.computed &&
    parent.property === node
  )
    return true;
  if (
    parent?.type === "ObjectProperty" &&
    parent.shorthand &&
    parent.value === node
  )
    return false;
  if (parent?.computed || parent?.key !== node) return false;
  return (
    parent.type === "ObjectProperty" ||
    parent.type === "ObjectMethod" ||
    parent.type === "ClassProperty" ||
    parent.type === "ClassMethod"
  );
}

export function createLexicalBindingIndex(source, filePath) {
  const linter = new Linter({ configType: "flat" });
  const messages = linter.verify(
    source,
    [
      {
        languageOptions: {
          ecmaVersion: "latest",
          sourceType: "module",
          parserOptions: { ecmaFeatures: { jsx: true } },
        },
      },
    ],
    { filename: filePath },
  );
  const fatal = messages.find((message) => message.fatal);
  if (fatal)
    throw new Error(
      `scope analysis ${filePath}:${fatal.line}:${fatal.column}: ${fatal.message}`,
    );
  const scopeManager = linter.getSourceCode()?.scopeManager;
  if (!scopeManager)
    throw new Error(
      `scope analysis ${filePath} did not produce a scope manager`,
    );
  const bindingsByRange = new Map();
  const programBindings = new Map();
  for (const scope of scopeManager.scopes)
    for (const variable of scope.variables) {
      if (variable.identifiers.length === 0) continue;
      for (const identifier of variable.identifiers)
        bindingsByRange.set(nodeRangeKey(identifier), variable);
      for (const reference of variable.references)
        bindingsByRange.set(nodeRangeKey(reference.identifier), variable);
      if (scope.type === "module") programBindings.set(variable.name, variable);
    }
  return {
    bindingFor(identifier) {
      return identifier?.type === "Identifier"
        ? bindingsByRange.get(nodeRangeKey(identifier))
        : undefined;
    },
    programBinding(name) {
      return programBindings.get(name);
    },
  };
}

function nodeRangeKey(node) {
  const start = node?.start ?? node?.range?.[0];
  const end = node?.end ?? node?.range?.[1];
  return Number.isInteger(start) && Number.isInteger(end)
    ? `${start}:${end}`
    : "";
}
