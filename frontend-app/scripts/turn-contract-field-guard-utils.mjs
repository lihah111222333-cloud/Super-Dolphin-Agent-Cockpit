import fs from "node:fs";
import path from "node:path";

export function readRepositorySource(repoRoot, relativePath, sourceOverrides) {
  if (sourceOverrides.has(relativePath))
    return sourceOverrides.get(relativePath);
  return fs.readFileSync(path.join(repoRoot, relativePath), "utf8");
}

export function parseJSON(source, label) {
  try {
    return JSON.parse(source);
  } catch (error) {
    throw new Error(`parse ${label}: ${error.message}`);
  }
}

export function assertExactSet(label, expected, actual) {
  if (
    expected.length === actual.length &&
    expected.every((value, index) => value === actual[index])
  )
    return;
  const expectedSet = new Set(expected);
  const actualSet = new Set(actual);
  const missing = expected.filter((value) => !actualSet.has(value));
  const stale = actual.filter((value) => !expectedSet.has(value));
  throw new Error(
    `${label} missing=${missing.join(",")} stale=${stale.join(",")}`,
  );
}

export function stableJSON(value) {
  if (Array.isArray(value)) return `[${value.map(stableJSON).join(",")}]`;
  if (value && typeof value === "object") {
    return `{${Object.keys(value)
      .sort()
      .map((key) => `${JSON.stringify(key)}:${stableJSON(value[key])}`)
      .join(",")}}`;
  }
  return JSON.stringify(value);
}

export function isRecord(value) {
  return value !== null && typeof value === "object" && !Array.isArray(value);
}

export function validateLocatorShape(repoRoot, locator, extension) {
  if (
    !isRecord(locator) ||
    typeof locator.path !== "string" ||
    typeof locator.symbol !== "string" ||
    !locator.symbol.trim()
  ) {
    throw new Error("source locator is incomplete");
  }
  const normalized = path.posix.normalize(locator.path);
  if (
    !locator.path ||
    path.isAbsolute(locator.path) ||
    normalized !== locator.path ||
    normalized.startsWith("../")
  ) {
    throw new Error(
      `locator path ${locator.path} must be normalized and repository-confined`,
    );
  }
  if (path.posix.extname(locator.path) !== extension)
    throw new Error(`locator path ${locator.path} must end with ${extension}`);
  const info = fs.lstatSync(path.join(repoRoot, locator.path));
  if (!info.isFile() || info.isSymbolicLink())
    throw new Error(`locator path ${locator.path} is not a regular file`);
}
