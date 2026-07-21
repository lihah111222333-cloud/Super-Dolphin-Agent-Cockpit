import * as audit from "../rpc-contract-audit.mjs";

function responsePolicyRpcMethod(entry) {
  if (entry.key === "TURN_INTERRUPT") return "thread.interrupt";
  return entry.method;
}

async function resolvePolicyLocator(
  auditContext,
  entry,
  field,
  locator,
  requireTestFile,
  findings,
) {
  const { repoRoot } = auditContext;
  if (!locator.path.trim()) {
    findings.push(policyFinding(entry, field, locator, "path must be non-blank"));
    return null;
  }
  if (!locator.symbol.trim()) {
    findings.push(policyFinding(entry, field, locator, "symbol must be non-blank"));
    return null;
  }
  const normalizedPath = audit.normalize(locator.path).replaceAll("\\", "/");
  const absolutePath = audit.resolve(repoRoot, locator.path);
  const relativePath = audit.relative(repoRoot, absolutePath).replaceAll("\\", "/");
  if (
    audit.isAbsolute(locator.path) ||
    normalizedPath !== locator.path ||
    relativePath === ".." ||
    relativePath.startsWith("../") ||
    audit.isAbsolute(relativePath)
  ) {
    findings.push(
      policyFinding(entry, field, locator, "path must be normalized and repository-confined"),
    );
    return null;
  }
  if (requireTestFile && !/\.(?:test|spec)\.(?:js|jsx|mjs)$/.test(locator.path)) {
    findings.push(
      policyFinding(entry, field, locator, "path must identify a JavaScript test file"),
    );
    return null;
  }
  if (await pathContainsSymbolicLink(repoRoot, locator.path)) {
    findings.push(
      policyFinding(entry, field, locator, "path must not resolve through a symbolic link"),
    );
    return null;
  }
  try {
    const canonicalPath = await audit.realpath(absolutePath);
    const canonicalRelative = audit.relative(repoRoot, canonicalPath).replaceAll("\\", "/");
    if (
      canonicalRelative === ".." ||
      canonicalRelative.startsWith("../") ||
      audit.isAbsolute(canonicalRelative)
    ) {
      findings.push(
        policyFinding(entry, field, locator, "path must be normalized and repository-confined"),
      );
      return null;
    }
    return { ast: await readAuditAst(auditContext, locator.path), path: locator.path };
  } catch (error) {
    if (error?.code === "ENOENT") {
      findings.push(policyFinding(entry, field, locator, "file does not exist"));
      return null;
    }
    throw error;
  }
}

async function pathContainsSymbolicLink(repoRoot, filePath) {
  let current = repoRoot;
  for (const segment of filePath.split("/")) {
    current = audit.join(current, segment);
    try {
      if ((await audit.lstat(current)).isSymbolicLink()) return true;
    } catch (error) {
      if (error?.code === "ENOENT") return false;
      throw error;
    }
  }
  return false;
}

async function readAuditSource(auditContext, filePath) {
  const cached = auditContext.sourceByPath.get(filePath);
  if (cached !== undefined) return cached;
  let pending = auditContext.sourcePromiseByPath.get(filePath);
  if (!pending) {
    auditContext.auditStats.sourceReads += 1;
    pending = audit.readFile(audit.join(auditContext.repoRoot, filePath), "utf8");
    auditContext.sourcePromiseByPath.set(filePath, pending);
  }
  const source = await pending;
  auditContext.sourceByPath.set(filePath, source);
  auditContext.sourcePromiseByPath.delete(filePath);
  return source;
}

function readAuditSourceSync(auditContext, filePath) {
  const cached = auditContext.sourceByPath.get(filePath);
  if (cached !== undefined) return cached;
  auditContext.auditStats.sourceReads += 1;
  const source = audit.readFileSync(audit.join(auditContext.repoRoot, filePath), "utf8");
  auditContext.sourceByPath.set(filePath, source);
  return source;
}

async function readAuditAst(auditContext, filePath) {
  const cached = auditContext.astByPath.get(filePath);
  if (cached) return cached;
  let pending = auditContext.astPromiseByPath.get(filePath);
  if (!pending) {
    pending = readAuditSource(auditContext, filePath).then((source) => {
      auditContext.auditStats.astParses += 1;
      return audit.parseFrontendAst(source);
    });
    auditContext.astPromiseByPath.set(filePath, pending);
  }
  const ast = await pending;
  auditContext.astByPath.set(filePath, ast);
  auditContext.astPromiseByPath.delete(filePath);
  return ast;
}

function policyFinding(entry, field, locator, reason) {
  return {
    key: entry.key,
    kind: entry.responsePolicy.kind,
    field,
    path: locator?.path ?? "",
    symbol: locator?.symbol ?? "",
    reason,
  };
}

function comparePolicyFindings(left, right) {
  return [left.key, left.kind, left.field, left.path, left.symbol, left.reason]
    .join("\u0000")
    .localeCompare(
      [right.key, right.kind, right.field, right.path, right.symbol, right.reason].join("\u0000"),
    );
}

function findProductionSymbol(ast, symbol) {
  const exportedLocalNames = new Set();
  const candidates = [];
  for (const statement of ast.program.body) {
    if (statement.type !== "ExportNamedDeclaration" || statement.source) continue;
    for (const specifier of statement.specifiers) {
      if (audit.moduleExportName(specifier.exported) === symbol) {
        exportedLocalNames.add(audit.moduleExportName(specifier.local));
      }
    }
    const declaration = statement.declaration;
    if (
      (declaration?.type === "FunctionDeclaration" || declaration?.type === "ClassDeclaration") &&
      declaration.id?.name === symbol
    ) {
      exportedLocalNames.add(symbol);
    }
    if (declaration?.type === "VariableDeclaration") {
      for (const item of declaration.declarations) {
        if (item.id.type === "Identifier" && item.id.name === symbol)
          exportedLocalNames.add(symbol);
      }
    }
  }
  if (exportedLocalNames.size !== 1) return null;
  const [localName] = exportedLocalNames;
  for (const statement of ast.program.body) {
    const declaration =
      statement.type === "ExportNamedDeclaration" ? statement.declaration : statement;
    if (
      (declaration?.type === "FunctionDeclaration" || declaration?.type === "ClassDeclaration") &&
      declaration.id?.name === localName
    ) {
      candidates.push(declaration);
    }
    if (declaration?.type === "VariableDeclaration") {
      for (const item of declaration.declarations) {
        if (item.id.type === "Identifier" && item.id.name === localName)
          candidates.push(item.init ?? item);
      }
    }
  }
  return candidates.length === 1 ? candidates[0] : null;
}

function findResponsePolicyConsumerSymbol(ast, locator) {
  if (locator.visibility === "module-private") {
    return findModulePrivateFunctionSymbol(ast, locator.symbol);
  }
  return findProductionSymbol(ast, locator.symbol);
}

function findModulePrivateFunctionSymbol(ast, symbol) {
  if (findProductionSymbol(ast, symbol)) return null;
  const candidates = [];
  audit.walkAstWithAncestors(ast, (node) => {
    if (node.type === "FunctionDeclaration" && node.id?.name === symbol) {
      candidates.push(node);
      return;
    }
    if (
      node.type === "VariableDeclarator" &&
      node.id.type === "Identifier" &&
      node.id.name === symbol
    ) {
      if (
        node.init?.type === "ArrowFunctionExpression" ||
        node.init?.type === "FunctionExpression"
      ) {
        candidates.push(node.init);
        return;
      }
      const callback = node.init?.type === "CallExpression" ? node.init.arguments[0] : null;
      if (callback?.type === "ArrowFunctionExpression" || callback?.type === "FunctionExpression") {
        candidates.push(callback);
      }
    }
  });
  return candidates.length === 1 ? candidates[0] : null;
}

function findModuleLevelSymbol(ast, symbol) {
  const candidates = [];
  for (const statement of ast.program.body) {
    const declaration =
      statement.type === "ExportNamedDeclaration" ? statement.declaration : statement;
    if (
      (declaration?.type === "FunctionDeclaration" || declaration?.type === "ClassDeclaration") &&
      declaration.id?.name === symbol
    )
      candidates.push(declaration);
    if (declaration?.type === "VariableDeclaration") {
      for (const item of declaration.declarations) {
        if (item.id.type === "Identifier" && item.id.name === symbol)
          candidates.push(item.init ?? item);
      }
    }
  }
  return candidates.length === 1 ? candidates[0] : null;
}

export {
  responsePolicyRpcMethod,
  resolvePolicyLocator,
  pathContainsSymbolicLink,
  readAuditSource,
  readAuditSourceSync,
  readAuditAst,
  policyFinding,
  comparePolicyFindings,
  findProductionSymbol,
  findResponsePolicyConsumerSymbol,
  findModulePrivateFunctionSymbol,
  findModuleLevelSymbol,
};
