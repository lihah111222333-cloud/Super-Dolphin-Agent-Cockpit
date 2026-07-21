import path from "node:path";
import process from "node:process";
import { fileURLToPath } from "node:url";
import {
  appRoot,
  assertInsideAppRoot,
  assertValidScope,
  baselinePath,
  baselineTestPath,
  defaultSourceRoots,
  collectExplicitFiles,
  collectFiles,
  filterFilesByScope,
  normalizeRel,
  readOptionValue,
} from "./frontend-code-size-config.mjs";
import { runCheck, runFreeze } from "./frontend-code-size-baseline.mjs";

export { FRONTEND_CODE_SIZE_LIMITS } from "./frontend-code-size-config.mjs";
export {
  countEffectiveLines,
  extractFunctions,
  measureMaxNesting,
} from "./frontend-code-size-metrics.mjs";
export {
  checkFrontendCodeSizeSource,
  measureFrontendCodeSizeSource,
  measureFrontendCodeSizeSourceAstShadow,
} from "./frontend-code-size-violations.mjs";

export function parseFrontendCodeSizeGuardArgs(args) {
  const options = {
    mode: "check",
    dirs: [],
    files: [],
    scope: "all",
    printScanDirs: false,
  };
  for (let index = 0; index < args.length; index += 1) {
    const arg = args[index];
    if (arg === "--freeze") options.mode = "freeze";
    else if (arg === "--strict") options.mode = "strict";
    else if (arg === "--print-scan-dirs") options.printScanDirs = true;
    else if (arg === "--scope") {
      options.scope = readOptionValue(args, index, arg);
      assertValidScope(options.scope);
      index += 1;
    } else if (arg === "--file") {
      options.files.push(readOptionValue(args, index, arg));
      index += 1;
    } else if (arg === "--dir") {
      options.dirs.push(
        path.resolve(appRoot, readOptionValue(args, index, arg)),
      );
      index += 1;
    } else throw new Error(`unknown argument: ${arg}`);
  }
  if (options.dirs.length === 0)
    options.dirs = defaultSourceRoots.map((root) => path.join(appRoot, root));
  return options;
}

function main() {
  const options = parseFrontendCodeSizeGuardArgs(process.argv.slice(2));
  for (const dir of options.dirs) assertInsideAppRoot(dir);
  if (options.printScanDirs) {
    for (const dir of options.dirs) console.log(normalizeRel(dir));
    return;
  }
  const files = filterFilesByScope(
    options.files.length > 0
      ? collectExplicitFiles(options.files)
      : collectFiles(options.dirs),
    options.scope,
  );
  if (files.length === 0) throw new Error("no frontend source files found");
  if (options.mode === "freeze")
    return runFreeze(files, baselinePath, baselineTestPath);
  const result = runCheck(files, baselinePath, baselineTestPath, {
    strict: options.mode === "strict",
  });
  if (result.violations.length === 0) {
    console.log(
      `frontend code size guard passed: files=${files.length}, frozen=${result.frozenFiles}`,
    );
    return;
  }
  console.error(
    `frontend code size guard failed: ${result.violations.length} violation(s)`,
  );
  for (const entry of result.violations.slice(0, 80)) {
    const location =
      entry.line > 0 ? `${entry.file}:${entry.line}` : entry.file;
    console.error(`- ${location} [${entry.rule}] ${entry.message}`);
  }
  if (result.violations.length > 80)
    console.error(`- ... ${result.violations.length - 80} more violation(s)`);
  process.exit(1);
}

if (process.argv[1] === fileURLToPath(import.meta.url)) {
  try {
    main();
  } catch (error) {
    console.error(
      `frontend code size guard: ${error instanceof Error ? error.message : String(error)}`,
    );
    process.exit(2);
  }
}
