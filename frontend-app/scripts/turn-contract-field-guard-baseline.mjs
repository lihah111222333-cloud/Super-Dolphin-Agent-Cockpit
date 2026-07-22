import fs from "node:fs";
import path from "node:path";

import { parseModule } from "./turn-contract-field-guard-ast.mjs";

const registryRelativePath =
  "internal/dto/turn/schema/field_consumers.json";
const repositoryBaselines = new Map();

export function immutableRepositoryBaseline(repoRoot) {
  const normalizedRoot = path.resolve(repoRoot);
  const cached = repositoryBaselines.get(normalizedRoot);
  if (cached) return cached;

  const productionPaths = productionJavaScriptFiles(
    path.join(normalizedRoot, "frontend-app/src"),
  ).map((absolutePath) =>
    path.relative(normalizedRoot, absolutePath).split(path.sep).join("/"),
  );
  const schemaDir = path.join(normalizedRoot, "internal/dto/turn/schema");
  const canonicalSchemaPaths = fs
    .readdirSync(schemaDir, { withFileTypes: true })
    .filter(
      (entry) =>
        !entry.isDirectory() &&
        entry.name.endsWith(".json") &&
        entry.name !== "field_consumers.json",
    )
    .map((entry) => `internal/dto/turn/schema/${entry.name}`);
  const indexedPaths = new Set([
    ...productionPaths,
    ...canonicalSchemaPaths,
    registryRelativePath,
  ]);
  const sources = new Map(
    [...indexedPaths].map((relativePath) => [
      relativePath,
      fs.readFileSync(path.join(normalizedRoot, relativePath), "utf8"),
    ]),
  );
  const baseline = Object.freeze({
    canonicalSchemaPaths: Object.freeze(canonicalSchemaPaths),
    productionPaths: Object.freeze(productionPaths),
    source(relativePath) {
      return sources.get(relativePath);
    },
  });
  repositoryBaselines.set(normalizedRoot, baseline);
  for (const relativePath of productionPaths)
    parseModule(sources.get(relativePath), relativePath);
  return baseline;
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
