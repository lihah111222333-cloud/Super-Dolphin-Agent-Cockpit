import * as audit from "../rpc-contract-audit.mjs";

function collectRpcMethodReferenceKeys(node) {
  const keys = new Set();
  audit.traverseAst(node, (candidate) => {
    if (
      candidate.type === "MemberExpression" &&
      !candidate.computed &&
      candidate.object.type === "Identifier" &&
      candidate.object.name === "RPC_METHODS" &&
      candidate.property.type === "Identifier"
    ) {
      keys.add(candidate.property.name);
    }
  });
  return keys;
}

function staticPropertyKeyName(property) {
  if (property.computed) return "";
  if (property.key.type === "Identifier") return property.key.name;
  if (property.key.type === "StringLiteral") return property.key.value;
  return "";
}

function sourceDeclaresFunction(source, functionName) {
  let found = false;
  audit.traverseAst(audit.parseFrontendAst(source), (node) => {
    if (node.type === "FunctionDeclaration" && node.id?.name === functionName) found = true;
  });
  return found;
}

function sourceContainsStringLiteral(source, value) {
  let found = false;
  audit.traverseAst(audit.parseFrontendAst(source), (node) => {
    if (node.type === "StringLiteral" && node.value === value) found = true;
  });
  return found;
}

function serviceFacadeMemberRpcKey(source, serviceName, memberName, backendFacadeRpcKeys) {
  if (!serviceName || !memberName || !audit.collectNamedExports(source).has(serviceName)) return "";
  const ast = audit.parseFrontendAst(source);
  let factoryName = "";
  audit.traverseAst(ast, (node) => {
    if (
      factoryName ||
      node.type !== "VariableDeclarator" ||
      node.id.type !== "Identifier" ||
      node.id.name !== serviceName ||
      node.init?.type !== "CallExpression" ||
      node.init.callee.type !== "Identifier"
    ) {
      return;
    }
    factoryName = node.init.callee.name;
  });
  if (!factoryName) return "";

  let factory = null;
  audit.traverseAst(ast, (node) => {
    if (!factory && node.type === "FunctionDeclaration" && node.id?.name === factoryName)
      factory = node;
  });
  if (!factory) return "";
  const apiParameterName =
    factory.params[0]?.type === "AssignmentPattern"
      ? factory.params[0].left?.name
      : factory.params[0]?.name;
  if (!apiParameterName) return "";

  const returnedObjects = [];
  for (const statement of factory.body.body) {
    if (statement.type !== "ReturnStatement") continue;
    if (statement.argument?.type === "ObjectExpression") {
      returnedObjects.push(statement.argument);
      continue;
    }
    if (statement.argument?.type !== "Identifier") continue;
    const returnedBinding = factory.body.body.find(
      (candidate) =>
        candidate.type === "VariableDeclaration" &&
        candidate.declarations.some(
          (declaration) =>
            declaration.id.type === "Identifier" &&
            declaration.id.name === statement.argument.name &&
            declaration.init?.type === "ObjectExpression",
        ),
    );
    if (!returnedBinding) continue;
    const declarator = returnedBinding.declarations.find(
      (declaration) =>
        declaration.id.type === "Identifier" && declaration.id.name === statement.argument.name,
    );
    returnedObjects.push(declarator.init);
  }

  for (const objectExpression of returnedObjects) {
    const member = objectExpression.properties
      .filter((property) => property.type === "ObjectMethod" || property.type === "ObjectProperty")
      .find((property) => staticPropertyKeyName(property) === memberName);
    if (!member) continue;
    const backendFacades = new Set();
    audit.traverseAst(member, (node) => {
      if (
        node.type === "MemberExpression" &&
        !node.computed &&
        node.object.type === "Identifier" &&
        node.object.name === apiParameterName &&
        node.property.type === "Identifier"
      ) {
        backendFacades.add(node.property.name);
      }
    });
    if (backendFacades.size !== 1) return "";
    return backendFacadeRpcKeys.get([...backendFacades][0]) ?? "";
  }
  return "";
}

function assertRpcMethodsFacadeReExport(source) {
  const ast = audit.parseFrontendAst(source);
  let exactReExportCount = 0;
  let conflictingBindingCount = 0;

  for (const statement of ast.program.body) {
    if (statement.type === "ImportDeclaration") {
      conflictingBindingCount += statement.specifiers.filter(
        (specifier) => specifier.local?.name === "RPC_METHODS",
      ).length;
      continue;
    }
    if (statement.type === "ExportNamedDeclaration") {
      if (declarationBindsName(statement.declaration, "RPC_METHODS")) {
        conflictingBindingCount += 1;
      }
      for (const specifier of statement.specifiers) {
        if (specifier.type !== "ExportSpecifier") continue;
        const localName = moduleExportName(specifier.local);
        const exportedName = moduleExportName(specifier.exported);
        if (localName !== "RPC_METHODS" && exportedName !== "RPC_METHODS") continue;
        if (
          statement.source?.value === audit.RPC_FACADE_REEXPORT_SOURCE &&
          localName === "RPC_METHODS" &&
          exportedName === "RPC_METHODS"
        ) {
          exactReExportCount += 1;
        } else {
          conflictingBindingCount += 1;
        }
      }
      continue;
    }
    if (declarationBindsName(statement, "RPC_METHODS")) {
      conflictingBindingCount += 1;
    }
  }

  if (exactReExportCount !== 1 || conflictingBindingCount !== 0) {
    throw new Error(
      `backendApi.js must named re-export RPC_METHODS from ${audit.RPC_FACADE_REEXPORT_SOURCE} exactly once`,
    );
  }
}

function moduleExportName(node) {
  if (node?.type === "Identifier" || node?.type === "StringLiteral") return node.name ?? node.value;
  return "";
}

function declarationBindsName(declaration, name) {
  if (!declaration) return false;
  if (declaration.type === "VariableDeclaration") {
    return declaration.declarations.some((entry) => bindingPatternContainsName(entry.id, name));
  }
  if (declaration.type === "FunctionDeclaration" || declaration.type === "ClassDeclaration") {
    return declaration.id?.name === name;
  }
  return false;
}

function bindingPatternContainsName(pattern, name) {
  if (!pattern) return false;
  if (pattern.type === "Identifier") return pattern.name === name;
  if (pattern.type === "AssignmentPattern") return bindingPatternContainsName(pattern.left, name);
  if (pattern.type === "RestElement") return bindingPatternContainsName(pattern.argument, name);
  if (pattern.type === "ArrayPattern") {
    return pattern.elements.some((entry) => bindingPatternContainsName(entry, name));
  }
  if (pattern.type === "ObjectPattern") {
    return pattern.properties.some((entry) =>
      entry.type === "RestElement"
        ? bindingPatternContainsName(entry.argument, name)
        : bindingPatternContainsName(entry.value, name),
    );
  }
  return false;
}

function collectFrontendResponseValidators(source) {
  const validators = new Map();
  audit.traverseAst(audit.parseFrontendAst(source), (node) => {
    if (node.type !== "ReturnStatement") return;
    const objectExpression = audit.unwrapObjectFreezeObject(node.argument);
    if (!objectExpression) return;
    for (const property of audit.objectPropertiesOnly(
      objectExpression,
      "backend response validators",
    )) {
      if (
        !property.computed ||
        property.key.type !== "MemberExpression" ||
        property.key.computed ||
        property.key.object.type !== "Identifier" ||
        property.key.object.name !== "methods" ||
        property.key.property.type !== "Identifier" ||
        property.value.type !== "Identifier"
      ) {
        continue;
      }
      const key = property.key.property.name;
      if (validators.has(key)) {
      throw new Error(`response validator registry maps ${key} more than once`);
      }
      validators.set(key, property.value.name);
    }
  });
  return validators;
}

async function collectGoPayloadKeys(auditContext) {
  const out = new Map();
  for (const [method, locators] of audit.GO_PAYLOAD_STRUCTS.entries()) {
    const keys = [];
    for (const locator of locators) {
      const [filePath, symbol] = locator.split(":");
      const source = await audit.readAuditSource(auditContext, filePath);
      keys.push(...parseGoStructJSONTags(source, symbol));
    }
    out.set(method, audit.uniqueSorted(keys));
  }
  return out;
}

function parseGoStructJSONTags(source, symbol) {
  const structMatch = source.match(
    new RegExp(`type\\s+${symbol}\\s+struct\\s*\\{([\\s\\S]*?)\\n\\}`),
  );
  if (!structMatch) {
    throw new Error(`${symbol} struct was not found in Go DTO source`);
  }
  const keys = [];
  for (const line of structMatch[1].split("\n")) {
    const tag = line.match(/`[^`]*json:"([^"]*)"[^`]*`/);
    if (!tag) continue;
    const name = tag[1].split(",")[0];
    if (!name || name === "-") {
      continue;
    }
    keys.push(name);
  }
  return keys;
}

export {
  collectRpcMethodReferenceKeys,
  staticPropertyKeyName,
  sourceDeclaresFunction,
  sourceContainsStringLiteral,
  serviceFacadeMemberRpcKey,
  assertRpcMethodsFacadeReExport,
  moduleExportName,
  declarationBindsName,
  bindingPatternContainsName,
  collectFrontendResponseValidators,
  collectGoPayloadKeys,
  parseGoStructJSONTags,
};
export {
  collectSidebarRequiredFieldFindingsFromSources,
  parseRequiredGoStructJSONTags,
  parseSidebarRuntimeRequiredFields,
  hasSidebarRuntimeRequiredCheck,
  isMissingSidebarFieldCheck,
  collectHardcodedPayloadGuardFindings,
  collectHardcodedPayloadGuardFindingsFromSources,
  collectPayloadRegistryDrift,
} from "./backend-go-contracts.mjs";
