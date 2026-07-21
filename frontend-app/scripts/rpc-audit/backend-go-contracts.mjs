import * as audit from "../rpc-contract-audit.mjs";
import { collectFrontendPayloadKeysFromSource as discoverFrontendPayloadKeys } from "./frontend-payload-discovery.mjs";

export function collectSidebarRequiredFieldFindingsFromSources({
  goSource = "",
  runtimeSource = "",
} = {}) {
  const producerFields = parseRequiredGoStructJSONTags(goSource, "Sidebar");
  const { consumerFields, registryUsedByRequiredCheck } =
    parseSidebarRuntimeRequiredFields(runtimeSource);
  const producerSet = new Set(producerFields);
  const consumerSet = new Set(consumerFields);
  const findings = [
    ...producerFields.filter((field) => !consumerSet.has(field)).map((field) => "missing:" + field),
    ...consumerFields.filter((field) => !producerSet.has(field)).map((field) => "stale:" + field),
  ];
  if (!registryUsedByRequiredCheck) {
    findings.push("runtime:SIDEBAR_REQUIRED_RESPONSE_KEYS is not used by the required-field check");
  }
  return findings;
}

function parseRequiredGoStructJSONTags(source, symbol) {
  if (typeof source !== "string" || !source.trim()) {
    throw new Error(symbol + " Go source is required");
  }
  const structMatch = source.match(
    new RegExp("type\\s+" + symbol + "\\s+struct\\s*\\{([\\s\\S]*?)\\n\\}"),
  );
  if (!structMatch) {
    throw new Error(symbol + " struct was not found in Go DTO source");
  }
  const fields = [];
  const seenNames = new Set();
  for (const line of structMatch[1].split("\n")) {
    const trimmed = line.trim();
    if (!trimmed || trimmed.startsWith("//")) continue;
    const field = line.match(/^\s*([A-Z][A-Za-z0-9_]*)\s+.+?\s+\x60([^\x60]*)\x60\s*(?:\/\/.*)?$/);
    if (!field) {
      throw new Error(symbol + " field declaration could not be parsed: " + trimmed);
    }
    const [, goName, rawTags] = field;
    const tagMatches = [...rawTags.matchAll(/([A-Za-z][A-Za-z0-9_]*):"([^"]*)"/g)];
    const canonicalTags = tagMatches.map(([raw]) => raw).join(" ");
    const jsonTags = tagMatches.filter(([, name]) => name === "json");
    if (canonicalTags !== rawTags.trim() || tagMatches.length !== 1 || jsonTags.length !== 1) {
      throw new Error(symbol + "." + goName + " must declare exactly one json tag");
    }
    const [jsonName, ...options] = jsonTags[0][2].split(",");
    if (!jsonName || jsonName === "-") {
      throw new Error(symbol + "." + goName + " json tag must name a field");
    }
    if (new Set(options).size !== options.length) {
      throw new Error(symbol + "." + goName + " json tag contains duplicate options");
    }
    for (const option of options) {
      if (option !== "omitempty") {
        throw new Error(symbol + "." + goName + " has unsupported json option " + option);
      }
    }
    if (seenNames.has(jsonName)) {
      throw new Error(symbol + " has duplicate json field " + jsonName);
    }
    seenNames.add(jsonName);
    fields.push({ name: jsonName, required: !options.includes("omitempty") });
  }
  if (fields.length === 0) {
    throw new Error(symbol + " has no JSON-tagged fields");
  }
  return audit.uniqueSorted(fields.filter((field) => field.required).map((field) => field.name));
}

function parseSidebarRuntimeRequiredFields(source) {
  if (typeof source !== "string" || !source.trim()) {
    throw new Error("Sidebar runtime validator source is required");
  }
  const ast = audit.parseFrontendAst(source);
  let registry = null;
  let validator = null;
  for (const statement of ast.program.body) {
    const declaration =
      statement.type === "ExportNamedDeclaration" ? statement.declaration : statement;
    if (declaration?.type === "VariableDeclaration" && declaration.kind === "const") {
      for (const declarator of declaration.declarations) {
        if (
          declarator.id.type !== "Identifier" ||
          declarator.id.name !== "SIDEBAR_REQUIRED_RESPONSE_KEYS"
        )
          continue;
        if (registry) {
          throw new Error("SIDEBAR_REQUIRED_RESPONSE_KEYS must be declared exactly once");
        }
        registry = declarator;
      }
    }
    if (
      statement.type === "ExportNamedDeclaration" &&
      declaration?.type === "FunctionDeclaration" &&
      declaration.id?.name === "validateSidebarStateResponse"
    ) {
      if (validator) {
        throw new Error("validateSidebarStateResponse must be exported exactly once");
      }
      validator = declaration;
    }
  }
  if (!registry) {
    throw new Error("SIDEBAR_REQUIRED_RESPONSE_KEYS registry was not found");
  }
  if (registry.init?.type !== "ArrayExpression") {
    throw new Error("SIDEBAR_REQUIRED_RESPONSE_KEYS must be a literal array");
  }
  const consumerFields = registry.init.elements.map((element, index) =>
    audit.stringLiteralValue(element, "SIDEBAR_REQUIRED_RESPONSE_KEYS[" + index + "]"),
  );
  if (consumerFields.length === 0) {
    throw new Error("SIDEBAR_REQUIRED_RESPONSE_KEYS must not be empty");
  }
  if (new Set(consumerFields).size !== consumerFields.length) {
    throw new Error("SIDEBAR_REQUIRED_RESPONSE_KEYS must not contain duplicates");
  }
  if (!validator) {
    throw new Error("validateSidebarStateResponse export was not found");
  }
  return {
    consumerFields: audit.uniqueSorted(consumerFields),
    registryUsedByRequiredCheck: hasSidebarRuntimeRequiredCheck(validator),
  };
}

function hasSidebarRuntimeRequiredCheck(validator) {
  for (const statement of validator.body.body) {
    if (
      statement.type !== "ForOfStatement" ||
      statement.right.type !== "Identifier" ||
      statement.right.name !== "SIDEBAR_REQUIRED_RESPONSE_KEYS" ||
      statement.left.type !== "VariableDeclaration" ||
      statement.left.declarations.length !== 1 ||
      statement.left.declarations[0].id.type !== "Identifier" ||
      statement.body.type !== "BlockStatement"
    ) {
      continue;
    }
    const fieldName = statement.left.declarations[0].id.name;
    for (const candidate of statement.body.body) {
      if (candidate.type === "ContinueStatement" || candidate.type === "ReturnStatement") {
        break;
      }
      if (
        candidate.type === "IfStatement" &&
        isMissingSidebarFieldCheck(candidate.test, fieldName) &&
        candidate.consequent.type === "BlockStatement" &&
        candidate.consequent.body.some((child) => child.type === "ThrowStatement")
      ) {
        return true;
      }
    }
  }
  return false;
}

function isMissingSidebarFieldCheck(node, fieldName) {
  const call = node?.type === "UnaryExpression" && node.operator === "!" ? node.argument : null;
  return (
    call?.type === "CallExpression" &&
    call.callee.type === "Identifier" &&
    call.callee.name === "hasOwn" &&
    call.arguments.length === 2 &&
    call.arguments[0].type === "Identifier" &&
    call.arguments[0].name === "value" &&
    call.arguments[1].type === "Identifier" &&
    call.arguments[1].name === fieldName
  );
}

async function collectHardcodedPayloadGuardFindings(auditContext, frontendSource) {
  const inspectedFiles = audit.uniqueSorted([
    ...new Set(
      [...audit.GO_PAYLOAD_STRUCTS.values()].flat().map((locator) => locator.split(":")[0]),
    ),
  ]);
  const goSources = new Map();
  await Promise.all(
    inspectedFiles.map(async (filePath) => {
      goSources.set(filePath, await audit.readAuditSource(auditContext, filePath));
    }),
  );
  return collectHardcodedPayloadGuardFindingsFromSources({
    frontendPath: audit.FRONTEND_PAYLOAD_BUILDERS_PATH,
    frontendSource,
    goSources,
  });
}

export function collectHardcodedPayloadGuardFindingsFromSources({
  frontendPath = audit.RPC_FACADE_PATH,
  frontendSource = "",
  goSources = new Map(),
} = {}) {
  const findings = [];
  const frontendAst = audit.parseFrontendAst(frontendSource);
  for (const statement of frontendAst.program.body) {
    const declaration =
      statement.type === "ExportNamedDeclaration" ? statement.declaration : statement;
    if (declaration?.type !== "VariableDeclaration") continue;
    for (const declarator of declaration.declarations) {
      const name = declarator.id.type === "Identifier" ? declarator.id.name : "";
      const isPayloadGuardName =
        name === "RPC_ALLOWED_PAYLOAD_KEYS" || /^[A-Z0-9_]+_ALLOWED_KEYS$/.test(name);
      const isSetOfArray =
        declarator.init?.type === "NewExpression" &&
        declarator.init.callee.type === "Identifier" &&
        declarator.init.callee.name === "Set" &&
        declarator.init.arguments[0]?.type === "ArrayExpression";
      if (isPayloadGuardName && isSetOfArray) {
        findings.push(`${frontendPath}:${name}`);
      }
    }
  }
  for (const [filePath, source] of goSources.entries()) {
    const goMapPattern =
      /^\s*var\s+([A-Za-z0-9_]*(?:Param|Payload)[A-Za-z0-9_]*(?:Fields|Keys))\s*=\s*map\[string\]struct\{\}/gm;
    let goMapMatch;
    while ((goMapMatch = goMapPattern.exec(source)) !== null) {
      findings.push(`${filePath}:${goMapMatch[1]}`);
    }
  }
  return findings;
}

export function collectFrontendPayloadKeysFromSource(
  source,
  methodValues = new Map(),
  requiredMethods = null,
) {
  return discoverFrontendPayloadKeys(source, {
    sourcePath: audit.FRONTEND_PAYLOAD_BUILDERS_PATH,
    methodValues,
    requiredMethods,
  });
}

export function collectPayloadRegistryDrift(goPayloadKeysByMethod, frontendPayloadKeysByMethod) {
  const drift = [];
  for (const [method, goKeys] of goPayloadKeysByMethod.entries()) {
    if (audit.FRONTEND_PAYLOAD_METHOD_EXEMPTIONS.has(method)) {
      continue;
    }
    const frontendKeys = frontendPayloadKeysByMethod.get(method) ?? [];
    const goKeySet = new Set(goKeys);
    const frontendKeySet = new Set(frontendKeys);
    const facadeOnlyKeys = new Set(audit.FRONTEND_FACADE_ONLY_PAYLOAD_KEYS.get(method) ?? []);
    const missingFrontendKeys = goKeys.filter((key) => !frontendKeySet.has(key));
    const extraFrontendKeys = frontendKeys.filter(
      (key) => !goKeySet.has(key) && !facadeOnlyKeys.has(key),
    );
    if (missingFrontendKeys.length > 0 || extraFrontendKeys.length > 0) {
      drift.push({
        method,
        missingFrontendKeys,
        extraFrontendKeys,
      });
    }
  }
  return drift.sort((a, b) => a.method.localeCompare(b.method));
}

export {
  parseRequiredGoStructJSONTags,
  parseSidebarRuntimeRequiredFields,
  hasSidebarRuntimeRequiredCheck,
  isMissingSidebarFieldCheck,
  collectHardcodedPayloadGuardFindings,
};
