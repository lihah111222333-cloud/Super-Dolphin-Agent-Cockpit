import * as audit from "../rpc-contract-audit.mjs";

async function buildProductionFacadeReferenceIndex(auditContext, entries, backendFacadeRpcKeys) {
  auditContext.auditStats.productionFacadeReferenceIndexBuilds += 1;
  const files = await listJavaScriptSourceFiles(
    audit.join(auditContext.repoRoot, "frontend-app/src"),
  );
  const excludedPaths = new Set([
    audit.RPC_MATRIX_PATH,
    audit.RPC_FACADE_PATH,
    ...audit.BACKEND_API_FACTORY_PATHS,
    ...audit.SERVICE_FACADE_LOCATORS.values(),
    ...[...audit.DIRECT_FACADE_RPC_LOCATORS.values()].flatMap((locator) => [
      locator.implementationPath,
      locator.methodPath,
    ]),
  ]);
  const productionFilePaths = files
    .map((absolutePath) =>
      audit.relative(auditContext.repoRoot, absolutePath).replaceAll("\\", "/"),
    )
    .filter((filePath) => !isExcludedProductionScanPath(filePath));
  const astEntries = [];
  const readBatchSize = 64;
  for (let start = 0; start < productionFilePaths.length; start += readBatchSize) {
    astEntries.push(
      ...(await Promise.all(
        productionFilePaths
          .slice(start, start + readBatchSize)
          .map(async (filePath) => [filePath, await audit.readAuditAst(auditContext, filePath)]),
      )),
    );
  }
  const astByFilePath = new Map(astEntries);
  auditContext.auditStats.productionSourceFilesIndexed = astByFilePath.size;
  const index = new Map();
  const reExportStatementsByPath = new Map();
  for (const [filePath, ast] of astByFilePath) {
    const statements = ast.program.body.filter(
      (statement) =>
        statement.source &&
        (statement.type === "ExportAllDeclaration" || statement.type === "ExportNamedDeclaration"),
    );
    if (statements.length > 0) reExportStatementsByPath.set(filePath, statements);
  }
  const facadeModulePathsByKey = new Map();
  for (const entry of entries) {
    facadeModulePathsByKey.set(
      entry.key,
      collectFacadeReExportPaths(reExportStatementsByPath, entry),
    );
  }
  for (const [filePath, ast] of astByFilePath) {
    if (excludedPaths.has(filePath)) continue;
    for (const entry of entries) {
      if (
        !index.has(entry.key) &&
        astReferencesFacade(
          ast,
          filePath,
          entry,
          backendFacadeRpcKeys,
          facadeModulePathsByKey.get(entry.key),
        )
      ) {
        index.set(entry.key, filePath);
      }
    }
  }
  return index;
}

function collectFacadeReExportPaths(reExportStatementsByPath, entry) {
  const facadeParts = entry.facade.split(".");
  const exportsByPath = new Map([[audit.RPC_FACADE_PATH, new Set([facadeParts[0]])]]);
  if (facadeParts.length !== 1) return exportsByPath;
  let changed = true;
  while (changed) {
    changed = false;
    for (const [filePath, statements] of reExportStatementsByPath) {
      const exportedNames = new Set(exportsByPath.get(filePath));
      for (const statement of statements) {
        const sourceNames = exportsByPath.get(
          audit.moduleSpecifierResolvedPath(filePath, statement.source.value),
        );
        if (!sourceNames) continue;
        if (statement.type === "ExportAllDeclaration" && !statement.exported) {
          for (const name of sourceNames) exportedNames.add(name);
        }
        if (statement.type === "ExportAllDeclaration" && statement.exported) {
          const namespaceName = audit.moduleExportName(statement.exported);
          for (const name of sourceNames) exportedNames.add(`${namespaceName}.${name}`);
        }
        if (statement.type === "ExportNamedDeclaration") {
          for (const specifier of statement.specifiers) {
            if (specifier.type === "ExportNamespaceSpecifier") {
              const namespaceName = audit.moduleExportName(specifier.exported);
              for (const name of sourceNames) exportedNames.add(`${namespaceName}.${name}`);
              continue;
            }
            if (specifier.type !== "ExportSpecifier") continue;
            const localName = audit.moduleExportName(specifier.local);
            const exportedName = audit.moduleExportName(specifier.exported);
            if (sourceNames.has(localName)) exportedNames.add(exportedName);
            const namespacePrefix = `${localName}.`;
            for (const name of sourceNames) {
              if (name.startsWith(namespacePrefix)) {
                exportedNames.add(`${exportedName}.${name.slice(namespacePrefix.length)}`);
              }
            }
          }
        }
      }
      const previousSize = exportsByPath.get(filePath)?.size ?? 0;
      if (exportedNames.size > previousSize) {
        exportsByPath.set(filePath, exportedNames);
        changed = true;
      }
    }
  }
  return exportsByPath;
}

async function listJavaScriptSourceFiles(directory) {
  const entries = await audit.readdir(directory, { withFileTypes: true });
  const groups = await Promise.all(
    entries.map(async (entry) => {
      const filePath = audit.join(directory, entry.name);
      if (entry.isSymbolicLink()) {
        throw new Error(`production scan tree must not contain symbolic links: ${filePath}`);
      }
      if (entry.isDirectory()) {
        if (entry.name === "dist" || entry.name === "generated" || entry.name === "node_modules")
          return [];
        return listJavaScriptSourceFiles(filePath);
      }
      return entry.isFile() && /\.(?:js|jsx|mjs)$/.test(entry.name) ? [filePath] : [];
    }),
  );
  return groups.flat().sort();
}

function isExcludedProductionScanPath(filePath) {
  return (
    /\.(?:test|spec|stories)\.(?:js|jsx|mjs)$/.test(filePath) ||
    filePath.includes("/__fixtures__/") ||
    /\.(?:fixture|mock)\.(?:js|jsx|mjs)$/.test(filePath)
  );
}

function staticMemberExpressionParts(node) {
  if (node?.type === "Identifier") return [node.name];
  if (node?.type !== "MemberExpression") return null;
  const objectParts = staticMemberExpressionParts(node.object);
  if (!objectParts) return null;
  const propertyName = node.computed
    ? node.property.type === "StringLiteral"
      ? node.property.value
      : ""
    : node.property.type === "Identifier"
      ? node.property.name
      : "";
  return propertyName ? [...objectParts, propertyName] : null;
}

function astReferencesFacade(ast, filePath, entry, backendFacadeRpcKeys, facadeModulePaths) {
  const bindings = audit.collectFacadeCallBindings(
    ast,
    filePath,
    entry,
    backendFacadeRpcKeys,
    facadeModulePaths,
  );
  const namespaceMemberPaths = bindings.namespaceMemberPaths ?? new Map();
  if (
    bindings.identifierAliases.size === 0 &&
    bindings.namespaceAliases.size === 0 &&
    namespaceMemberPaths.size === 0
  ) {
    return false;
  }
  const addNamespaceAliasPaths = (name, paths) => {
    const existing = namespaceMemberPaths.get(name) ?? new Set();
    const previousSize = existing.size;
    for (const path of paths) existing.add(path);
    if (existing.size === previousSize) return false;
    namespaceMemberPaths.set(name, existing);
    bindings.namespaceAliases.add(name);
    return true;
  };
  const propagateMemberAlias = (localName, expression) => {
    const parts = staticMemberExpressionParts(expression);
    if (!parts || parts.length < 2) return false;
    const sourcePaths = namespaceMemberPaths.get(parts[0]);
    if (!sourcePaths) return false;
    const consumedPath = parts.slice(1).join(".");
    const remainingPaths = [];
    let exact = false;
    for (const targetPath of sourcePaths) {
      if (targetPath === consumedPath) exact = true;
      else if (targetPath.startsWith(`${consumedPath}.`)) {
        remainingPaths.push(targetPath.slice(consumedPath.length + 1));
      }
    }
    let propagated = false;
    if (exact && !bindings.identifierAliases.has(localName)) {
      bindings.identifierAliases.add(localName);
      propagated = true;
    }
    return addNamespaceAliasPaths(localName, remainingPaths) || propagated;
  };
  let changed = true;
  while (changed) {
    changed = false;
    audit.traverseAst(ast, (node) => {
      if (
        node.type === "VariableDeclarator" &&
        node.id.type === "Identifier" &&
        node.init?.type === "Identifier" &&
        bindings.identifierAliases.has(node.init.name) &&
        !bindings.identifierAliases.has(node.id.name)
      ) {
        bindings.identifierAliases.add(node.id.name);
        changed = true;
      }
      if (
        node.type === "VariableDeclarator" &&
        node.id.type === "Identifier" &&
        node.init?.type === "Identifier" &&
        namespaceMemberPaths.has(node.init.name)
      ) {
        changed =
          addNamespaceAliasPaths(node.id.name, namespaceMemberPaths.get(node.init.name)) || changed;
      }
      if (
        node.type === "VariableDeclarator" &&
        node.id.type === "Identifier" &&
        node.init?.type === "MemberExpression"
      ) {
        changed = propagateMemberAlias(node.id.name, node.init) || changed;
      }
      if (
        node.type === "VariableDeclarator" &&
        node.id.type === "Identifier" &&
        node.init?.type === "MemberExpression" &&
        !node.init.computed &&
        node.init.object.type === "Identifier" &&
        bindings.namespaceAliases.has(node.init.object.name) &&
        node.init.property.type === "Identifier" &&
        (node.init.property.name === bindings.memberName ||
          bindings.namespaceMemberNames?.has(node.init.property.name)) &&
        !bindings.identifierAliases.has(node.id.name)
      ) {
        bindings.identifierAliases.add(node.id.name);
        changed = true;
      }
      if (
        node.type === "VariableDeclarator" &&
        node.id.type === "ObjectPattern" &&
        node.init?.type === "Identifier" &&
        bindings.namespaceAliases.has(node.init.name)
      ) {
        for (const property of node.id.properties) {
          if (
            property.type === "ObjectProperty" &&
            (audit.staticPropertyKeyName(property) === bindings.memberName ||
              bindings.namespaceMemberNames?.has(audit.staticPropertyKeyName(property))) &&
            property.value.type === "Identifier" &&
            !bindings.identifierAliases.has(property.value.name)
          ) {
            bindings.identifierAliases.add(property.value.name);
            changed = true;
          }
        }
      }
    });
  }
  let found = false;
  audit.walkAstWithAncestors(ast, (node, ancestors) => {
    if (found) return;
    if (
      node.type === "Identifier" &&
      bindings.identifierAliases.has(node.name) &&
      isReferencedIdentifierAt(node, ancestors) &&
      !audit.bindingShadowsNameAt(ancestors, node.name)
    ) {
      found = true;
      return;
    }
    const memberParts = staticMemberExpressionParts(node);
    if (memberParts?.length > 1) {
      const targetPaths = namespaceMemberPaths.get(memberParts[0]);
      if (
        targetPaths?.has(memberParts.slice(1).join(".")) &&
        !audit.bindingShadowsNameAt(ancestors, memberParts[0])
      ) {
        found = true;
        return;
      }
    }
    if (
      node.type === "MemberExpression" &&
      !node.computed &&
      node.object.type === "Identifier" &&
      node.property.type === "Identifier" &&
      (node.property.name === bindings.memberName ||
        bindings.namespaceMemberNames?.has(node.property.name)) &&
      bindings.namespaceAliases.has(node.object.name) &&
      !audit.bindingShadowsNameAt(ancestors, node.object.name)
    ) {
      found = true;
    }
  });
  return found;
}

function isReferencedIdentifierAt(node, ancestors) {
  const parent = ancestors.at(-1);
  return !(
    parent?.type === "ImportSpecifier" ||
    parent?.type === "ImportNamespaceSpecifier" ||
    (parent?.type === "VariableDeclarator" && parent.id === node) ||
    ((parent?.type === "FunctionDeclaration" || parent?.type === "FunctionExpression") &&
      (parent.id === node || parent.params.includes(node))) ||
    ((parent?.type === "ObjectProperty" || parent?.type === "ObjectMethod") &&
      parent.key === node &&
      !parent.computed) ||
    (parent?.type === "MemberExpression" && parent.property === node && !parent.computed)
  );
}

function collectNamedExports(source) {
  const names = new Set();
  for (const statement of audit.parseFrontendAst(source).program.body) {
    if (statement.type !== "ExportNamedDeclaration") continue;
    for (const specifier of statement.specifiers) {
      const exportedName = audit.moduleExportName(specifier.exported);
      if (exportedName) names.add(exportedName);
    }
    collectDeclarationBindingNames(statement.declaration, names);
  }
  return names;
}

function collectDeclarationBindingNames(declaration, names) {
  if (!declaration) return;
  if (declaration.type === "VariableDeclaration") {
    for (const entry of declaration.declarations) collectBindingNames(entry.id, names);
    return;
  }
  if (
    (declaration.type === "FunctionDeclaration" || declaration.type === "ClassDeclaration") &&
    declaration.id?.name
  ) {
    names.add(declaration.id.name);
  }
}

function collectBindingNames(pattern, names) {
  if (!pattern) return;
  if (pattern.type === "Identifier") {
    names.add(pattern.name);
    return;
  }
  if (pattern.type === "AssignmentPattern") return collectBindingNames(pattern.left, names);
  if (pattern.type === "RestElement") return collectBindingNames(pattern.argument, names);
  if (pattern.type === "ArrayPattern") {
    for (const entry of pattern.elements) collectBindingNames(entry, names);
    return;
  }
  if (pattern.type === "ObjectPattern") {
    for (const entry of pattern.properties) {
      collectBindingNames(entry.type === "RestElement" ? entry.argument : entry.value, names);
    }
  }
}

async function collectBackendFacadeRpcKeys(auditContext) {
  const facadeRpcKeys = new Map();
  for (const filePath of audit.BACKEND_API_FACTORY_PATHS) {
    const ast = await audit.readAuditAst(auditContext, filePath);
    audit.traverseAst(ast, (node) => {
      if (
        node.type !== "FunctionDeclaration" ||
        !/^create[A-Za-z0-9]+Api$/.test(node.id?.name ?? "")
      ) {
        return;
      }
      for (const statement of node.body.body) {
        if (statement.type !== "ReturnStatement" || statement.argument?.type !== "ObjectExpression")
          continue;
        for (const property of statement.argument.properties) {
          if (property.type !== "ObjectMethod" && property.type !== "ObjectProperty") continue;
          const facade = audit.staticPropertyKeyName(property);
          if (!facade) continue;
          const rpcKeys = collectRpcMethodReferenceKeysWithHelpers(property, ast);
          if (rpcKeys.size !== 1) continue;
          const [rpcKey] = rpcKeys;
          const existing = facadeRpcKeys.get(facade);
          if (existing && existing !== rpcKey) {
            throw new Error(`backend API facade ${facade} maps both ${existing} and ${rpcKey}`);
          }
          facadeRpcKeys.set(facade, rpcKey);
        }
      }
    });
  }
  for (const [rpcKey, locator] of audit.DIRECT_FACADE_RPC_LOCATORS.entries()) {
    const implementationSource = await audit.readAuditSource(
      auditContext,
      locator.implementationPath,
    );
    const methodSource =
      locator.methodPath === locator.implementationPath
        ? implementationSource
        : await audit.readAuditSource(auditContext, locator.methodPath);
    if (
      !collectNamedExports(implementationSource).has(locator.facade) ||
      !audit.sourceDeclaresFunction(implementationSource, locator.facade) ||
      !audit.sourceContainsStringLiteral(methodSource, locator.method)
    ) {
      throw new Error(`${locator.facade} must trace to ${locator.method} for ${rpcKey}`);
    }
    facadeRpcKeys.set(locator.facade, rpcKey);
  }
  return facadeRpcKeys;
}

function collectRpcMethodReferenceKeysWithHelpers(node, ast) {
  const keys = audit.collectRpcMethodReferenceKeys(node);
  const helperNames = new Set();
  audit.traverseAst(node, (candidate) => {
    if (candidate.type === "CallExpression" && candidate.callee.type === "Identifier") {
      helperNames.add(candidate.callee.name);
    }
  });
  if (helperNames.size === 0) return keys;
  audit.traverseAst(ast, (candidate) => {
    if (candidate.type === "FunctionDeclaration" && helperNames.has(candidate.id?.name ?? "")) {
      for (const key of audit.collectRpcMethodReferenceKeys(candidate)) keys.add(key);
    }
  });
  return keys;
}

export {
  buildProductionFacadeReferenceIndex,
  collectFacadeReExportPaths,
  listJavaScriptSourceFiles,
  isExcludedProductionScanPath,
  staticMemberExpressionParts,
  astReferencesFacade,
  isReferencedIdentifierAt,
  collectNamedExports,
  collectDeclarationBindingNames,
  collectBindingNames,
  collectBackendFacadeRpcKeys,
  collectRpcMethodReferenceKeysWithHelpers,
};
