import fs from "node:fs";
import path from "node:path";

export const appRoot = path.resolve(new URL("..", import.meta.url).pathname);
export const baselinePath = path.join(
  appRoot,
  ".frontend_code_size_guard_baseline.json",
);
export const baselineTestPath = path.join(
  appRoot,
  ".frontend_code_size_guard_baseline_test.json",
);

export const FRONTEND_CODE_SIZE_LIMITS = Object.freeze({
  maxFileLines: 800,
  maxFunctionLines: 150,
  maxNesting: 4,
  maxParams: 5,
  maxExports: 20,
  maxDirectoryFiles: 15,
  maxLineLength: 300,
});

export const sourceExtensionPattern = /\.[cm]?[jt]sx?$/;
export const defaultSourceRoots = Object.freeze(["src"]);
export const ignoreDirNames = new Set(["node_modules", "dist", "coverage"]);

export function readOptionValue(args, index, flag) {
  const value = args[index + 1];
  if (value === undefined || value === "" || value.startsWith("--"))
    throw new Error(`missing value for ${flag}`);
  return value;
}

export function normalizeRel(filePath, root = appRoot) {
  return path.relative(root, filePath).split(path.sep).join("/");
}

export function assertInsideAppRoot(targetPath) {
  const rel = path.relative(appRoot, targetPath);
  if (rel === ".." || rel.startsWith(`..${path.sep}`) || path.isAbsolute(rel)) {
    throw new Error(
      `scan directory is outside frontend app root: ${targetPath}`,
    );
  }
}

export function isFrontendTestFile(relFile) {
  return (
    /\.(test|spec)(?:-helper)?\.[cm]?[jt]sx?$/.test(relFile) ||
    relFile.includes("/__tests__/")
  );
}

export function assertValidScope(scope) {
  if (!["production", "test", "all"].includes(scope))
    throw new Error(`invalid value for --scope: ${scope}`);
}

function walkSourceFiles(dir, files = []) {
  if (!fs.existsSync(dir))
    throw new Error(`frontend code size guard root does not exist: ${dir}`);
  if (!fs.statSync(dir).isDirectory())
    throw new Error(`frontend code size guard root is not a directory: ${dir}`);
  for (const entry of fs.readdirSync(dir, { withFileTypes: true })) {
    const fullPath = path.join(dir, entry.name);
    if (entry.isDirectory()) {
      if (!ignoreDirNames.has(entry.name) && !entry.name.startsWith("."))
        walkSourceFiles(fullPath, files);
    } else if (
      entry.isFile() &&
      sourceExtensionPattern.test(entry.name) &&
      !entry.name.endsWith(".d.ts")
    ) {
      files.push(fullPath);
    }
  }
  return files;
}

export function collectFiles(scanDirs) {
  return scanDirs
    .flatMap((dir) => walkSourceFiles(dir))
    .map((abs) => ({ abs, rel: normalizeRel(abs) }));
}

function assertAllowedSourceFile(relFile, absFile) {
  if (path.isAbsolute(relFile))
    throw new Error(`--file must be relative to frontend app root: ${relFile}`);
  assertInsideAppRoot(absFile);
  if (!fs.existsSync(absFile))
    throw new Error(`--file does not exist: ${relFile}`);
  if (!fs.statSync(absFile).isFile())
    throw new Error(`--file is not a file: ${relFile}`);
  if (!sourceExtensionPattern.test(relFile) || relFile.endsWith(".d.ts"))
    throw new Error(`--file is not a frontend source file: ${relFile}`);
  for (const part of relFile.split("/"))
    if (ignoreDirNames.has(part) || part.startsWith("."))
      throw new Error(`--file is under an ignored directory: ${relFile}`);
}

export function collectExplicitFiles(relFiles) {
  return relFiles.map((relFile) => {
    const abs = path.resolve(appRoot, relFile);
    assertAllowedSourceFile(relFile, abs);
    return { abs, rel: normalizeRel(abs) };
  });
}

export function filterFilesByScope(files, scope) {
  assertValidScope(scope);
  if (scope === "all") return files;
  return files.filter((file) =>
    scope === "production"
      ? !isFrontendTestFile(file.rel)
      : isFrontendTestFile(file.rel),
  );
}
