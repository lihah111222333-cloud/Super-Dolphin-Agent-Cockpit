import {
  FRONTEND_CODE_SIZE_LIMITS,
  isFrontendTestFile,
} from "./frontend-code-size-config.mjs";
import {
  countEffectiveLines,
  countExports,
  countExportsFromAst,
  countFunctionParams,
  extractFunctions,
  extractFunctionsFromAst,
  measureMaxNesting,
  measureMaxNestingFromAst,
  parseFrontendCodeSizeSourceAst,
} from "./frontend-code-size-metrics.mjs";

function makeViolation(file, line, rule, message) {
  return { file, line, rule, message };
}

function rulesForSource(relFile, source, limits = FRONTEND_CODE_SIZE_LIMITS) {
  const lines = source.split("\n");
  const violations = [];
  const testFile = isFrontendTestFile(relFile);
  const effectiveLines = countEffectiveLines(lines);
  if (!testFile && effectiveLines > limits.maxFileLines)
    violations.push(
      makeViolation(
        relFile,
        1,
        "file-length",
        `文件有效代码 ${effectiveLines} 行，超过上限 ${limits.maxFileLines} 行`,
      ),
    );
  for (const func of extractFunctions(lines)) {
    if (func.lines > limits.maxFunctionLines)
      violations.push(
        makeViolation(
          relFile,
          func.start,
          "func-length",
          `函数 ${func.name} 共 ${func.lines} 行，超过上限 ${limits.maxFunctionLines} 行`,
        ),
      );
    const params = countFunctionParams(lines, func.start);
    if (params > limits.maxParams)
      violations.push(
        makeViolation(
          relFile,
          func.start,
          "params",
          `函数 ${func.name} 有 ${params} 个参数，超过上限 ${limits.maxParams} 个`,
        ),
      );
  }
  const maxNesting = measureMaxNesting(lines);
  if (!testFile && maxNesting > limits.maxNesting)
    violations.push(
      makeViolation(
        relFile,
        1,
        "nesting",
        `最大嵌套 ${maxNesting} 层，超过上限 ${limits.maxNesting} 层`,
      ),
    );
  const exportCount = countExports(source);
  if (exportCount > limits.maxExports)
    violations.push(
      makeViolation(
        relFile,
        1,
        "exports",
        `${exportCount} 个 export，超过上限 ${limits.maxExports} 个`,
      ),
    );
  lines.forEach((line, index) => {
    const trimmed = line.trimStart();
    if (trimmed.startsWith("//") || trimmed.startsWith("*")) return;
    if (!testFile && /console\.log\s*\(/.test(trimmed))
      violations.push(
        makeViolation(
          relFile,
          index + 1,
          "console-log",
          "生产代码禁止 console.log()，请用 logger 或删除",
        ),
      );
    if (
      /(?::\s*any\b|<any>|as\s+any\b|\bany\[\]|\bany\s*[|&]|[|&]\s*any\b)/.test(
        trimmed,
      )
    )
      violations.push(
        makeViolation(
          relFile,
          index + 1,
          "any",
          "禁止使用 any 类型，请使用具体类型或 unknown",
        ),
      );
    if (!testFile && line.length > limits.maxLineLength)
      violations.push(
        makeViolation(
          relFile,
          index + 1,
          "line-length",
          `单行 ${line.length} 字符，超过上限 ${limits.maxLineLength}，禁止用超长单行绕过复杂度守卫`,
        ),
      );
  });
  for (let index = 0; index < lines.length - 1; index += 1) {
    const line = lines[index].trimEnd();
    if (
      !testFile &&
      (/(?:function\s+\w+|=>\s*)\{\s*\}\s*[;,]?\s*$/.test(line) ||
        (line.endsWith("{") && (lines[index + 1] || "").trim() === "}"))
    )
      violations.push(
        makeViolation(
          relFile,
          index + 1,
          "empty-func",
          "空函数体，可能是未实现",
        ),
      );
  }
  lines.forEach((line, index) => {
    const upper = line.toUpperCase();
    if (
      upper.includes("TODO") ||
      upper.includes("FIXME") ||
      upper.includes("HACK")
    )
      violations.push(makeViolation(relFile, index + 1, "todo", line.trim()));
  });
  return violations;
}

export function checkFrontendCodeSizeSource(
  relFile,
  source,
  limits = FRONTEND_CODE_SIZE_LIMITS,
) {
  return rulesForSource(relFile, source, limits);
}

function baseMetrics(lines, functions, violations) {
  return {
    lines: countEffectiveLines(lines),
    maxFuncLen: functions.reduce((max, func) => Math.max(max, func.lines), 0),
    maxParams: functions.reduce(
      (max, func) =>
        Math.max(max, func.params ?? countFunctionParams(lines, func.start)),
      0,
    ),
    consoleLogs: violations.filter((entry) => entry.rule === "console-log")
      .length,
    anyCount: violations.filter((entry) => entry.rule === "any").length,
    emptyFuncs: violations.filter((entry) => entry.rule === "empty-func")
      .length,
    todoCount: violations.filter((entry) => entry.rule === "todo").length,
    longLineCount: violations.filter((entry) => entry.rule === "line-length")
      .length,
  };
}

export function measureFrontendCodeSizeSource(relFile, source) {
  const lines = source.split("\n");
  const violations = rulesForSource(relFile, source);
  return {
    ...baseMetrics(lines, extractFunctions(lines), violations),
    maxNesting: measureMaxNesting(lines),
    exportCount: countExports(source),
  };
}

export function measureFrontendCodeSizeSourceAstShadow(relFile, source) {
  const lines = source.split("\n");
  const ast = parseFrontendCodeSizeSourceAst(source);
  const violations = rulesForSource(relFile, source);
  return {
    ...baseMetrics(lines, extractFunctionsFromAst(ast), violations),
    maxNesting: measureMaxNestingFromAst(ast),
    exportCount: countExportsFromAst(ast),
  };
}
