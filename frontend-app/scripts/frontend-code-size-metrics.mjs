import { parse as parseJavaScriptSource } from "@babel/parser";

export function countEffectiveLines(lines) {
  let count = 0;
  let inBlock = false;
  for (const raw of lines) {
    const line = raw.trimStart();
    if (inBlock) {
      if (line.includes("*/")) inBlock = false;
      continue;
    }
    if (line === "" || line.startsWith("//") || line.startsWith("*")) continue;
    if (line.startsWith("/*")) {
      if (!line.includes("*/")) inBlock = true;
      continue;
    }
    count += 1;
  }
  return count;
}

function stripStrings(line) {
  return line
    .replace(/`(?:[^`\\]|\\.)*`/g, '\"\"')
    .replace(/\"(?:[^\"\\]|\\.)*\"/g, '\"\"')
    .replace(/'(?:[^'\\]|\\.)*'/g, '\"\"');
}

export function extractFunctions(lines) {
  const functions = [];
  let depth = 0;
  let activeName = "";
  let activeStart = 0;
  let activeStartDepth = 0;
  const functionRe =
    /^(?:export\s+(?:default\s+)?)?(?:async\s+)?function\s+([A-Za-z_$][\w$]*)?\s*\(/;
  const arrowAssignRe =
    /^(?:export\s+)?(?:const|let|var)\s+([A-Za-z_$][\w$]*)\s*(?::\s*[^=]+)?\s*=\s*(?:async\s+)?(?:\([^)]*\)|[A-Za-z_$][\w$]*)\s*=>/;
  const methodRe =
    /^\s*(?:async\s+)?([A-Za-z_$][\w$]*)\s*\([^)]*\)\s*(?::\s*[\w<>[\], |&.?]+)?\s*\{/;
  for (let index = 0; index < lines.length; index += 1) {
    const line = lines[index];
    const trimmed = line.trimStart();
    if (
      trimmed.startsWith("//") ||
      trimmed.startsWith("*") ||
      trimmed.startsWith("/*")
    )
      continue;
    if (!activeName) {
      const functionMatch = trimmed.match(functionRe);
      const arrowMatch = !functionMatch && trimmed.match(arrowAssignRe);
      const methodMatch =
        !functionMatch && !arrowMatch && depth > 0 && trimmed.match(methodRe);
      if (functionMatch) {
        activeName = functionMatch[1] || "anonymous";
        activeStart = index;
        activeStartDepth = depth;
      } else if (
        arrowMatch &&
        line.includes("{") &&
        !line.trimEnd().endsWith("{}")
      ) {
        activeName = arrowMatch[1];
        activeStart = index;
        activeStartDepth = depth;
      } else if (methodMatch) {
        activeName = methodMatch[1];
        activeStart = index;
        activeStartDepth = depth;
      }
    }
    for (const ch of stripStrings(line)) {
      if (ch === "{") depth += 1;
      if (ch === "}") depth -= 1;
    }
    if (activeName && depth <= activeStartDepth) {
      functions.push({
        name: activeName,
        start: activeStart + 1,
        end: index + 1,
        lines: index - activeStart + 1,
      });
      activeName = "";
      if (depth < 0) depth = 0;
    }
  }
  return functions;
}

export function measureMaxNesting(lines) {
  let maxDepth = 0;
  let depth = 0;
  for (const line of lines) {
    const trimmed = line.trimStart();
    if (trimmed.startsWith("//") || trimmed.startsWith("*")) continue;
    for (const ch of stripStrings(line)) {
      if (ch === "{") {
        depth += 1;
        if (depth > maxDepth) maxDepth = depth;
      }
      if (ch === "}") depth -= 1;
    }
  }
  return maxDepth;
}

export function countFunctionParams(lines, funcStartLine) {
  const match = (lines[funcStartLine - 1] || "").match(/\(([^)]*)\)/);
  if (!match) return 0;
  const params = match[1].trim();
  return params === ""
    ? 0
    : params.split(",").filter((part) => part.trim() !== "").length;
}

export function countExports(source) {
  return source.match(/^export\s+/gm)?.length || 0;
}

function parseFrontendCodeSizeAst(source) {
  return parseJavaScriptSource(source, {
    sourceType: "module",
    plugins: ["jsx", "typescript"],
  });
}

function astPropertyKeyName(key) {
  if (key?.type === "Identifier") return key.name;
  if (key?.type === "StringLiteral" || key?.type === "NumericLiteral")
    return String(key.value);
  if (key?.type === "PrivateName") return key.id.name;
  return "anonymous";
}

function astChildren(node) {
  const children = [];
  for (const [key, value] of Object.entries(node)) {
    if (key === "loc" || key === "start" || key === "end") continue;
    if (Array.isArray(value))
      children.push(
        ...value.filter((entry) => entry && typeof entry.type === "string"),
      );
    else if (value && typeof value.type === "string") children.push(value);
  }
  return children;
}

function traverseAst(node, visit, parent = null) {
  if (!node || typeof node.type !== "string") return;
  visit(node, parent);
  for (const child of astChildren(node)) traverseAst(child, visit, node);
}

function astFunctionName(node, parent) {
  if (node.type === "FunctionDeclaration") return node.id?.name || "anonymous";
  if (
    node.type === "ObjectMethod" ||
    node.type === "ClassMethod" ||
    node.type === "ClassPrivateMethod"
  )
    return astPropertyKeyName(node.key);
  if (
    node.type !== "ArrowFunctionExpression" &&
    node.type !== "FunctionExpression"
  )
    return "";
  return parent?.type === "VariableDeclarator" &&
    parent.id.type === "Identifier"
    ? parent.id.name
    : "";
}

export function extractFunctionsFromAst(ast) {
  const functions = [];
  traverseAst(ast, (node, parent) => {
    const name = astFunctionName(node, parent);
    if (!name || !node.loc) return;
    functions.push({
      name,
      start: node.loc.start.line,
      end: node.loc.end.line,
      lines: node.loc.end.line - node.loc.start.line + 1,
      params: Array.isArray(node.params) ? node.params.length : 0,
    });
  });
  return functions.sort(
    (left, right) =>
      left.start - right.start || left.name.localeCompare(right.name),
  );
}

export function measureMaxNestingFromAst(ast) {
  let maxDepth = 0;
  const walk = (node, depth) => {
    if (!node || typeof node.type !== "string") return;
    const nextDepth = [
      "BlockStatement",
      "ObjectExpression",
      "ClassBody",
      "SwitchStatement",
    ].includes(node.type)
      ? depth + 1
      : depth;
    if (nextDepth > maxDepth) maxDepth = nextDepth;
    for (const child of astChildren(node)) walk(child, nextDepth);
  };
  walk(ast, 0);
  return maxDepth;
}

export function countExportsFromAst(ast) {
  return ast.program.body.filter((node) =>
    [
      "ExportNamedDeclaration",
      "ExportDefaultDeclaration",
      "ExportAllDeclaration",
    ].includes(node.type),
  ).length;
}

export function parseFrontendCodeSizeSourceAst(source) {
  return parseFrontendCodeSizeAst(source);
}
