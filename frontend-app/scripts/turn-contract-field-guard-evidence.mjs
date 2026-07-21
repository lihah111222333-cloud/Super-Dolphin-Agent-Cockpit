import fs from "node:fs";
import path from "node:path";
import {
  assertUniqueProductionSymbols,
  namedProductionFunctions,
  parseModule,
  walkFunctionBody,
  walkNode,
  stringLiteralValue,
} from "./turn-contract-field-guard-ast.mjs";
import { readRepositorySource } from "./turn-contract-field-guard-utils.mjs";
import {
  assertValidatorBindingsSafe,
  createLexicalBindingIndex,
  validatorBindingTarget,
  validatorNamespace,
} from "./turn-contract-field-guard-bindings.mjs";

const validatorRelativePath =
  "frontend-app/src/shared/contracts/turnContractValidators.js";

function moduleName(node) {
  return node?.type === "Identifier" ? node.name : stringLiteralValue(node);
}

function hasValidatorBindings(bindings) {
  return bindings.identifiers.size > 0 || bindings.namespaces.size > 0;
}

function requiredLexicalBinding(bindings, identifier, relativePath) {
  const binding = bindings.lexical?.bindingFor(identifier);
  if (!binding) {
    throw new Error(
      `${relativePath} import binding ${identifier?.name ?? ""} cannot be resolved`,
    );
  }
  return binding;
}

export function consumerKey(consumer) {
  return `${consumer.path}:${consumer.symbol}:${consumer.calls}`;
}

export function discoverJSValidatorConsumers(
  repoRoot,
  sourceOverrides,
  targetSchemas,
  resolveValidatorExports,
) {
  const discovered = new Map(
    [...targetSchemas.values()].map((schemaName) => [schemaName, []]),
  );
  for (const absolutePath of productionJavaScriptFiles(
    path.join(repoRoot, "frontend-app/src"),
  )) {
    const relativePath = path
      .relative(repoRoot, absolutePath)
      .split(path.sep)
      .join("/");
    const ast = parseModuleSource(repoRoot, sourceOverrides, relativePath);
    resolveValidatorExports(relativePath);
    const bindings = validatorBindings(
      repoRoot,
      sourceOverrides,
      ast,
      relativePath,
      targetSchemas,
      resolveValidatorExports,
    );
    if (!hasValidatorBindings(bindings)) continue;
    assertValidatorBindingsSafe(ast, bindings, relativePath);
    const claimedCalls = new Set();
    const functions = namedProductionFunctions(ast);
    assertUniqueProductionSymbols(functions, relativePath);
    for (const fn of functions)
      walkFunctionBody(fn, (node) => {
        const target =
          node.type === "CallExpression" &&
          validatorBindingTarget(node.callee, bindings);
        if (!target) return;
        claimedCalls.add(node);
        discovered.get(target.schemaName).push(
          consumerKey({
            path: relativePath,
            symbol: fn.symbol,
            calls: target.symbol,
          }),
        );
      });
    walkNode(ast.program, (node) => {
      const target =
        node.type === "CallExpression" &&
        validatorBindingTarget(node.callee, bindings);
      if (target && !claimedCalls.has(node))
        throw new Error(
          `${relativePath} validator call ${target.symbol} cannot be attributed to a stable production symbol`,
        );
    });
  }
  for (const [schemaName, consumers] of discovered)
    discovered.set(schemaName, [...new Set(consumers)].sort());
  return discovered;
}

function productionJavaScriptFiles(root) {
  const files = [];
  for (const entry of fs.readdirSync(root, { withFileTypes: true })) {
    const absolutePath = path.join(root, entry.name);
    if (entry.isDirectory())
      files.push(...productionJavaScriptFiles(absolutePath));
    else if (
      /\.(?:js|jsx)$/.test(entry.name) &&
      !/\.(?:test|spec)\.(?:js|jsx)$/.test(entry.name)
    )
      files.push(absolutePath);
  }
  return files;
}

export function createValidatorExportResolver(
  repoRoot,
  sourceOverrides,
  targetSchemas,
) {
  const directExports = new Map(
    [...targetSchemas].map(([symbol, schemaName]) => [
      symbol,
      { schemaName, symbol },
    ]),
  );
  const cache = new Map([[validatorRelativePath, directExports]]);
  const resolving = new Set();
  function resolveValidatorExports(modulePath) {
    if (cache.has(modulePath)) return cache.get(modulePath);
    if (resolving.has(modulePath)) return new Map();
    resolving.add(modulePath);
    try {
      const ast = parseModuleSource(repoRoot, sourceOverrides, modulePath);
      const bindings = validatorBindings(
        repoRoot,
        sourceOverrides,
        ast,
        modulePath,
        targetSchemas,
        resolveValidatorExports,
      );
      const exports = collectValidatorExports(
        repoRoot,
        sourceOverrides,
        ast,
        modulePath,
        bindings,
        resolveValidatorExports,
      );
      cache.set(modulePath, exports);
      return exports;
    } finally {
      resolving.delete(modulePath);
    }
  }
  return resolveValidatorExports;
}

function parseModuleSource(repoRoot, sourceOverrides, relativePath) {
  return parseModule(
    readRepositorySource(repoRoot, relativePath, sourceOverrides),
    relativePath,
  );
}

export function validatorBindings(
  repoRoot,
  sourceOverrides,
  ast,
  relativePath,
  targetSchemas,
  resolveValidatorExports,
) {
  const bindings = importedValidatorBindings(
    repoRoot,
    sourceOverrides,
    ast,
    relativePath,
    resolveValidatorExports,
  );
  if (relativePath === validatorRelativePath) {
    bindings.lexical ??= createLexicalBindingIndex(
      readRepositorySource(repoRoot, relativePath, sourceOverrides),
      relativePath,
    );
    for (const [symbol, schemaName] of targetSchemas) {
      const binding = bindings.lexical.programBinding(symbol);
      if (!binding)
        throw new Error(
          `${relativePath} validator declaration ${symbol} is missing`,
        );
      bindings.identifiers.set(binding, { schemaName, symbol });
    }
  }
  return bindings;
}

function importedValidatorBindings(
  repoRoot,
  sourceOverrides,
  ast,
  relativePath,
  resolveValidatorExports,
) {
  const bindings = {
    identifiers: new Map(),
    namespaces: new Map(),
    lexical: undefined,
  };
  const imported = [];
  for (const statement of ast.program.body) {
    if (
      statement.type !== "ImportDeclaration" ||
      typeof statement.source?.value !== "string"
    )
      continue;
    const sourcePath = resolveLocalModulePath(
      repoRoot,
      sourceOverrides,
      relativePath,
      statement.source.value,
    );
    if (!sourcePath) continue;
    const sourceExports = resolveValidatorExports(sourcePath);
    if (sourceExports.size === 0) continue;
    for (const specifier of statement.specifiers) {
      if (specifier.type === "ImportSpecifier") {
        const target = sourceExports.get(moduleName(specifier.imported));
        if (target)
          imported.push({ kind: "identifier", local: specifier.local, target });
      } else if (specifier.type === "ImportNamespaceSpecifier")
        imported.push({
          kind: "namespace",
          local: specifier.local,
          target: sourceExports,
        });
      else if (specifier.type === "ImportDefaultSpecifier") {
        const target = sourceExports.get("default");
        if (target)
          imported.push({ kind: "identifier", local: specifier.local, target });
      }
    }
  }
  if (imported.length === 0) return bindings;
  bindings.lexical = createLexicalBindingIndex(
    readRepositorySource(repoRoot, relativePath, sourceOverrides),
    relativePath,
  );
  for (const entry of imported) {
    const binding = requiredLexicalBinding(bindings, entry.local, relativePath);
    (entry.kind === "namespace"
      ? bindings.namespaces
      : bindings.identifiers
    ).set(binding, entry.target);
  }
  return bindings;
}

function collectValidatorExports(
  repoRoot,
  sourceOverrides,
  ast,
  modulePath,
  bindings,
  resolveValidatorExports,
) {
  const exports = new Map();
  for (const statement of ast.program.body) {
    if (statement.type === "ExportAllDeclaration") {
      if (
        validatorExportsFromSource(
          repoRoot,
          sourceOverrides,
          modulePath,
          statement.source?.value,
          resolveValidatorExports,
        ).size > 0
      ) {
        throw new Error(
          `${modulePath} validator export escape requires explicit named re-exports`,
        );
      }
    } else if (statement.type === "ExportNamedDeclaration") {
      collectNamedValidatorExports(
        repoRoot,
        sourceOverrides,
        modulePath,
        statement,
        bindings,
        resolveValidatorExports,
        exports,
      );
    } else if (statement.type === "ExportDefaultDeclaration") {
      const target = validatorBindingTarget(statement.declaration, bindings);
      if (target) setValidatorExport(exports, "default", target, modulePath);
      else if (validatorNamespace(statement.declaration, bindings))
        throw new Error(
          `${modulePath} validator namespace export escape cannot be resolved exactly`,
        );
    }
  }
  return exports;
}

function collectNamedValidatorExports(
  repoRoot,
  sourceOverrides,
  modulePath,
  statement,
  bindings,
  resolveValidatorExports,
  exports,
) {
  if (typeof statement.source?.value === "string") {
    const sourceExports = validatorExportsFromSource(
      repoRoot,
      sourceOverrides,
      modulePath,
      statement.source.value,
      resolveValidatorExports,
    );
    for (const specifier of statement.specifiers) {
      if (specifier.type !== "ExportSpecifier") {
        if (sourceExports.size > 0)
          throw new Error(
            `${modulePath} validator export escape cannot be resolved exactly`,
          );
      } else {
        const target = sourceExports.get(moduleName(specifier.local));
        if (target)
          setValidatorExport(
            exports,
            moduleName(specifier.exported),
            target,
            modulePath,
          );
      }
    }
    return;
  }
  for (const specifier of statement.specifiers) {
    if (specifier.type !== "ExportSpecifier") continue;
    const target = validatorBindingTarget(specifier.local, bindings);
    if (target)
      setValidatorExport(
        exports,
        moduleName(specifier.exported),
        target,
        modulePath,
      );
    else if (validatorNamespace(specifier.local, bindings))
      throw new Error(
        `${modulePath} validator namespace export escape cannot be resolved exactly`,
      );
  }
  if (statement.declaration?.type === "VariableDeclaration")
    for (const declarator of statement.declaration.declarations) {
      if (validatorBindingTarget(declarator.init, bindings))
        throw new Error(
          `${modulePath} validator export escape cannot be resolved exactly`,
        );
    }
}

function validatorExportsFromSource(
  repoRoot,
  sourceOverrides,
  modulePath,
  sourceValue,
  resolveValidatorExports,
) {
  const sourcePath = resolveLocalModulePath(
    repoRoot,
    sourceOverrides,
    modulePath,
    sourceValue,
  );
  return sourcePath ? resolveValidatorExports(sourcePath) : new Map();
}

function resolveLocalModulePath(
  repoRoot,
  sourceOverrides,
  importerPath,
  sourceValue,
) {
  if (typeof sourceValue !== "string" || !sourceValue.startsWith("."))
    return "";
  const base = path.posix.normalize(
    path.posix.join(path.posix.dirname(importerPath), sourceValue),
  );
  if (!base.startsWith("frontend-app/src/")) return "";
  const extension = path.posix.extname(base);
  if (extension && !/\.(?:js|jsx)$/.test(extension)) return "";
  for (const candidate of extension
    ? [base]
    : [`${base}.js`, `${base}.jsx`, `${base}/index.js`, `${base}/index.jsx`]) {
    if (sourceOverrides.has(candidate)) return candidate;
    try {
      const info = fs.lstatSync(path.join(repoRoot, candidate));
      if (info.isFile() && !info.isSymbolicLink()) return candidate;
    } catch (error) {
      if (error?.code !== "ENOENT") throw error;
    }
  }
  return "";
}

function setValidatorExport(exports, exportedName, target, modulePath) {
  if (!exportedName)
    throw new Error(`${modulePath} validator export has a blank name`);
  const existing = exports.get(exportedName);
  if (
    existing &&
    (existing.schemaName !== target.schemaName ||
      existing.symbol !== target.symbol)
  )
    throw new Error(
      `${modulePath} validator export ${exportedName} is ambiguous`,
    );
  exports.set(exportedName, target);
}
