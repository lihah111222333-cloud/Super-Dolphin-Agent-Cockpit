import fs from "node:fs";
import path from "node:path";
import {
  FRONTEND_CODE_SIZE_LIMITS,
  isFrontendTestFile,
} from "./frontend-code-size-config.mjs";
import {
  checkFrontendCodeSizeSource,
  measureFrontendCodeSizeSource,
} from "./frontend-code-size-violations.mjs";

function loadBaseline(filePath) {
  return fs.existsSync(filePath)
    ? JSON.parse(fs.readFileSync(filePath, "utf8"))
    : { _meta: null, files: {} };
}
function saveBaseline(filePath, data) {
  fs.writeFileSync(filePath, `${JSON.stringify(data, null, 2)}\n`, "utf8");
}
function canonicalViolationSignature(signature) {
  return typeof signature === "string" &&
    signature.split("\u0000")[0] === "file-length"
    ? "file-length"
    : signature;
}
function violationSignature(entry) {
  return canonicalViolationSignature(`${entry.rule}\u0000${entry.message}`);
}

function unfrozenViolations(violations, frozen) {
  const budget = new Map();
  for (const signature of Array.isArray(frozen?.frozenViolations)
    ? frozen.frozenViolations
    : []) {
    const canonical = canonicalViolationSignature(signature);
    budget.set(canonical, (budget.get(canonical) || 0) + 1);
  }
  if (budget.size === 0) return violations;
  return violations.filter((entry) => {
    const signature = violationSignature(entry);
    const remaining = budget.get(signature) || 0;
    if (remaining <= 0) return true;
    budget.set(signature, remaining - 1);
    return false;
  });
}

function makeViolation(file, line, rule, message) {
  return { file, line, rule, message };
}
function baselineEntry(relFile, source, violations) {
  const metrics = measureFrontendCodeSizeSource(relFile, source);
  metrics.frozenViolations = violations.map(violationSignature).sort();
  return metrics;
}

function ratchetViolations(relFile, current, frozen) {
  const checks = [
    ["freeze/file-length", "lines", "行"],
    ["freeze/func-length", "maxFuncLen", "行"],
    ["freeze/nesting", "maxNesting", "层"],
    ["freeze/params", "maxParams", "个"],
    ["freeze/exports", "exportCount", "个"],
    ["freeze/console-log", "consoleLogs", "处"],
    ["freeze/any", "anyCount", "处"],
    ["freeze/empty-func", "emptyFuncs", "个"],
    ["freeze/todo", "todoCount", "处"],
    ["freeze/line-length", "longLineCount", "处"],
  ];
  return checks.flatMap(([rule, key, unit]) =>
    frozen?.[key] === undefined ||
    current?.[key] === undefined ||
    current[key] <= frozen[key]
      ? []
      : [
          makeViolation(
            relFile,
            1,
            rule,
            `从 ${frozen[key]}${unit} 增长到 ${current[key]}${unit}（已冻结，只允许减少）`,
          ),
        ],
  );
}

function directorySizeViolations(files, baseline) {
  const counts = new Map();
  for (const { rel } of files.filter((file) => !isFrontendTestFile(file.rel)))
    counts.set(path.dirname(rel), (counts.get(path.dirname(rel)) || 0) + 1);
  return [...counts.entries()].flatMap(([dir, count]) => {
    const frozen = baseline.files?.[`__dir__:${dir}`];
    if (frozen && count > frozen.lines)
      return [
        makeViolation(
          dir,
          0,
          "freeze/dir-size",
          `从 ${frozen.lines} 个文件增长到 ${count} 个（已冻结，只允许减少）`,
        ),
      ];
    return !frozen && count > FRONTEND_CODE_SIZE_LIMITS.maxDirectoryFiles
      ? [
          makeViolation(
            dir,
            0,
            "dir-size",
            `目录含 ${count} 个生产文件，超过上限 ${FRONTEND_CODE_SIZE_LIMITS.maxDirectoryFiles} 个，应拆分`,
          ),
        ]
      : [];
  });
}

function refreshDirectoryBaseline(files, baseline) {
  const nextFiles = { ...(baseline.files || {}) };
  const counts = new Map();
  for (const { rel } of files.filter((file) => !isFrontendTestFile(file.rel)))
    counts.set(path.dirname(rel), (counts.get(path.dirname(rel)) || 0) + 1);
  for (const key of Object.keys(nextFiles))
    if (key.startsWith("__dir__:") && !counts.has(key.slice("__dir__:".length)))
      delete nextFiles[key];
  for (const [dir, count] of counts.entries()) {
    if (count > FRONTEND_CODE_SIZE_LIMITS.maxDirectoryFiles)
      nextFiles[`__dir__:${dir}`] = { lines: count };
    else delete nextFiles[`__dir__:${dir}`];
  }
  return { ...baseline, files: nextFiles };
}

function saveBaselineIfChanged(filePath, current, next) {
  if (JSON.stringify(current.files || {}) === JSON.stringify(next.files || {}))
    return;
  saveBaseline(filePath, {
    _meta: { updatedAt: new Date().toISOString().replace(/\.\d{3}Z$/, "Z") },
    files: next.files,
  });
}

export function runFreeze(files, baselinePath, baselineTestPath) {
  const meta = {
    updatedAt: new Date().toISOString().replace(/\.\d{3}Z$/, "Z"),
  };
  const prodBaseline = { _meta: meta, files: {} };
  const testBaseline = { _meta: meta, files: {} };
  for (const file of files) {
    const source = fs.readFileSync(file.abs, "utf8");
    const violations = checkFrontendCodeSizeSource(file.rel, source);
    if (violations.length === 0) continue;
    const target = isFrontendTestFile(file.rel) ? testBaseline : prodBaseline;
    target.files[file.rel] = baselineEntry(file.rel, source, violations);
  }
  const counts = new Map();
  for (const { rel } of files.filter((file) => !isFrontendTestFile(file.rel)))
    counts.set(path.dirname(rel), (counts.get(path.dirname(rel)) || 0) + 1);
  for (const [dir, count] of counts.entries())
    if (count > FRONTEND_CODE_SIZE_LIMITS.maxDirectoryFiles)
      prodBaseline.files[`__dir__:${dir}`] = { lines: count };
  saveBaseline(baselinePath, prodBaseline);
  saveBaseline(baselineTestPath, testBaseline);
  console.log(
    `frontend code size guard froze ${Object.keys(prodBaseline.files).length} production entries and ${Object.keys(testBaseline.files).length} test entries`,
  );
}

export function runCheck(
  files,
  baselinePath,
  baselineTestPath,
  { strict = false } = {},
) {
  const prodBaseline = strict ? { files: {} } : loadBaseline(baselinePath);
  const testBaseline = strict ? { files: {} } : loadBaseline(baselineTestPath);
  const nextProd = {
    ...prodBaseline,
    files: { ...(prodBaseline.files || {}) },
  };
  const nextTest = {
    ...testBaseline,
    files: { ...(testBaseline.files || {}) },
  };
  const scanned = new Set(files.map((file) => file.rel));
  const violations = [];
  let frozenFiles = 0;
  for (const file of files) {
    const source = fs.readFileSync(file.abs, "utf8");
    const current = checkFrontendCodeSizeSource(file.rel, source);
    const baseline = isFrontendTestFile(file.rel) ? testBaseline : prodBaseline;
    const next = isFrontendTestFile(file.rel) ? nextTest : nextProd;
    const frozen = baseline.files?.[file.rel];
    violations.push(
      ...(frozen ? unfrozenViolations(current, frozen) : current),
    );
    if (!frozen) continue;
    const ratchets = ratchetViolations(
      file.rel,
      measureFrontendCodeSizeSource(file.rel, source),
      frozen,
    );
    violations.push(...ratchets);
    if (current.length > 0 || ratchets.length > 0) {
      frozenFiles += 1;
      if (ratchets.length === 0)
        next.files[file.rel] = baselineEntry(file.rel, source, current);
    } else delete next.files[file.rel];
  }
  violations.push(
    ...directorySizeViolations(files, strict ? { files: {} } : prodBaseline),
  );
  if (violations.length > 0) return { violations, frozenFiles };
  if (!strict) {
    for (const key of Object.keys(nextProd.files))
      if (
        !key.startsWith("__dir__:") &&
        (!scanned.has(key) || isFrontendTestFile(key))
      )
        delete nextProd.files[key];
    for (const key of Object.keys(nextTest.files))
      if (!scanned.has(key) || !isFrontendTestFile(key))
        delete nextTest.files[key];
    saveBaselineIfChanged(
      baselinePath,
      prodBaseline,
      refreshDirectoryBaseline(files, nextProd),
    );
    saveBaselineIfChanged(baselineTestPath, testBaseline, nextTest);
  }
  return { violations, frozenFiles };
}
