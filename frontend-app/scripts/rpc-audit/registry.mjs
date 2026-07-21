import {
  findFrozenObjectExport,
  objectPropertiesOnly,
  propertyKeyName,
  stringLiteralValue,
} from "./ast-parsing.mjs";

const RESPONSE_VALIDATOR_POLICY_EXCEPTIONS = new Map([
  ["validateControlResponse", "mcpServerControlResponse"],
]);

function parseRpcMethods(source) {
  const objectExpression = findFrozenObjectExport(source, "RPC_METHODS");
  if (!objectExpression) {
    throw new Error("RPC_METHODS object was not found in backend/backendRpcMethods.js");
  }

  return objectPropertiesOnly(objectExpression, "RPC_METHODS").map((property) => ({
    key: propertyKeyName(property),
    method: stringLiteralValue(property.value, `RPC_METHODS.${propertyKeyName(property)}`),
  }));
}

function parseContractMatrix(source) {
  const objectExpression = findFrozenObjectExport(source, "RPC_CONTRACT_REGISTRY");
  if (!objectExpression) {
    throw new Error("RPC_CONTRACT_REGISTRY object was not found in backendApi.contractMatrix.js");
  }

  const entries = objectPropertiesOnly(objectExpression, "RPC_CONTRACT_REGISTRY").map((property) =>
    parseContractRegistryProperty(property),
  );

  const badKey = entries.find((entry) => entry.key !== entry.declaredKey);
  if (badKey) {
    throw new Error(`Contract key mismatch: ${badKey.key} declares ${badKey.declaredKey}`);
  }

  return entries;
}

function parseContractRegistryProperty(property) {
  const key = propertyKeyName(property);
  if (
    property.value.type !== "CallExpression" ||
    property.value.callee.type !== "Identifier" ||
    property.value.callee.name !== "contract"
  ) {
    throw new Error(`RPC_CONTRACT_REGISTRY.${key} must call contract(...)`);
  }
  const args = property.value.arguments;
  const options = args[8]?.type === "ObjectExpression" ? args[8] : null;
  const methodReference = rpcMethodReferenceValue(args[1], key);
  const level = stringLiteralValue(args[3], `RPC_CONTRACT_REGISTRY.${key} level`);
  const responseMetadata = parseResponseMetadata(options, key, level);
  return {
    key,
    declaredKey: stringLiteralValue(args[0], `RPC_CONTRACT_REGISTRY.${key} declared key`),
    method: methodReference.method,
    methodReferenceKey: methodReference.key,
    facade: stringLiteralValue(args[2], `RPC_CONTRACT_REGISTRY.${key} facade`),
    level,
    ...responseMetadata,
  };
}

function parseResponseMetadata(options, key, level) {
  const label = `RPC_CONTRACT_REGISTRY.${key}`;
  const properties = strictObjectProperties(options, `${label} options`);
  if (properties.has("responsePassthroughReason")) {
    throw new Error(`${label} responsePassthroughReason is forbidden`);
  }
  const allowed = new Set(["responseValidator", "responsePolicy"]);
  for (const name of properties.keys()) {
    if (!allowed.has(name)) throw new Error(`${label} options has extra field ${name}`);
  }
  const hasValidator = properties.has("responseValidator");
  const hasPolicy = properties.has("responsePolicy");
  if ((level === "P0" || level === "P1") && hasValidator === hasPolicy) {
    throw new Error(`${label} must declare exactly one of responseValidator or responsePolicy`);
  }
  if (hasValidator && hasPolicy) {
    throw new Error(`${label} must declare exactly one of responseValidator or responsePolicy`);
  }
  const responseValidator = hasValidator
    ? nonBlankStringLiteralValue(
        properties.get("responseValidator").value,
        `${label} responseValidator`,
      )
    : "";
  const responsePolicy = hasPolicy
    ? parseResponsePolicy(properties.get("responsePolicy").value, label)
    : null;
  return { responseValidator, responsePolicy };
}

function parseResponsePolicy(node, label) {
  if (node?.type !== "ObjectExpression")
    throw new Error(`${label} responsePolicy must be an object literal`);
  const properties = strictObjectProperties(node, `${label} responsePolicy`);
  const kind = nonBlankStringLiteralValue(
    properties.get("kind")?.value,
    `${label} responsePolicy.kind`,
  );
  const fieldsByKind = new Map([
    ["ignored-result", ["kind", "consumer", "regressionTest"]],
    ["result-handled", ["kind", "consumer", "handler", "regressionTest"]],
    ["consumer-validated", ["kind", "consumer", "shape", "regressionTest"]],
    ["unused", ["kind", "productionScanRoots", "excludedGlobs"]],
  ]);
  const expectedFields = fieldsByKind.get(kind);
  if (!expectedFields) throw new Error(`${label} responsePolicy.kind is invalid: ${kind}`);
  const policyFields =
    kind === "ignored-result" && properties.has("outcome")
      ? [...expectedFields, "outcome"]
      : expectedFields;
  assertExactFields(properties, policyFields, `${label} responsePolicy`);
  if (kind === "unused") {
    const productionScanRoots = stringLiteralArrayValue(
      properties.get("productionScanRoots").value,
      `${label} responsePolicy.productionScanRoots`,
    );
    if (productionScanRoots.length !== 1 || productionScanRoots[0] !== "frontend-app/src") {
      throw new Error(
        `${label} responsePolicy.productionScanRoots must equal ['frontend-app/src']`,
      );
    }
    return {
      kind,
      productionScanRoots,
      excludedGlobs: stringLiteralArrayValue(
        properties.get("excludedGlobs").value,
        `${label} responsePolicy.excludedGlobs`,
      ),
    };
  }
  const responsePolicy = {
    kind,
    consumer: parseResponsePolicyLocator(
      properties.get("consumer").value,
      `${label} responsePolicy.consumer`,
      { allowModulePrivate: true },
    ),
    regressionTest: parseResponsePolicyLocator(
      properties.get("regressionTest").value,
      `${label} responsePolicy.regressionTest`,
    ),
  };
  if (kind === "ignored-result" && properties.has("outcome")) {
    responsePolicy.outcome = parseIgnoredResultOutcome(
      properties.get("outcome").value,
      `${label} responsePolicy.outcome`,
    );
  }
  if (kind === "consumer-validated") {
    responsePolicy.shape = parseResponsePolicyLocator(
      properties.get("shape").value,
      `${label} responsePolicy.shape`,
    );
  }
  if (kind === "result-handled") {
    responsePolicy.handler = parseResponsePolicyLocator(
      properties.get("handler").value,
      `${label} responsePolicy.handler`,
    );
  }
  return responsePolicy;
}

function parseIgnoredResultOutcome(node, label) {
  if (node?.type !== "ObjectExpression") throw new Error(`${label} must be an object literal`);
  const properties = strictObjectProperties(node, label);
  assertExactFields(properties, ["kind", "target"], label);
  const kind = nonBlankStringLiteralValue(properties.get("kind")?.value, `${label}.kind`);
  if (kind !== "published-callback") throw new Error(`${label}.kind must equal published-callback`);
  const target = stringLiteralArrayValue(properties.get("target").value, `${label}.target`);
  if (target.length === 0 || target.some((part) => !part.trim())) {
    throw new Error(`${label}.target must contain non-blank strings`);
  }
  return { kind, target };
}

function parseResponsePolicyLocator(node, label, { allowModulePrivate = false } = {}) {
  if (node?.type !== "ObjectExpression") throw new Error(`${label} must be an object literal`);
  const properties = strictObjectProperties(node, label);
  const requiredFields = new Set(["path", "symbol"]);
  const allowedFields = new Set([...requiredFields, ...(allowModulePrivate ? ["visibility"] : [])]);
  for (const field of requiredFields) {
    if (!properties.has(field)) throw new Error(`${label} is missing field ${field}`);
  }
  for (const field of properties.keys()) {
    if (!allowedFields.has(field)) throw new Error(`${label} has extra field ${field}`);
  }
  const locator = {
    path: stringLiteralValue(properties.get("path").value, `${label}.path`),
    symbol: stringLiteralValue(properties.get("symbol").value, `${label}.symbol`),
  };
  if (properties.has("visibility")) {
    const visibility = stringLiteralValue(
      properties.get("visibility").value,
      `${label}.visibility`,
    );
    if (visibility !== "module-private") {
      throw new Error(`${label}.visibility must equal module-private`);
    }
    locator.visibility = visibility;
  }
  return locator;
}

function strictObjectProperties(objectExpression, label) {
  const properties = new Map();
  if (!objectExpression) return properties;
  for (const property of objectExpression.properties) {
    if (property.type !== "ObjectProperty")
      throw new Error(`${label} must not contain spread or methods`);
    if (property.computed) throw new Error(`${label} must not contain computed fields`);
    const name = propertyKeyName(property);
    if (properties.has(name)) throw new Error(`${label} has duplicate field ${name}`);
    properties.set(name, property);
  }
  return properties;
}

function assertExactFields(properties, expectedFields, label) {
  const expected = new Set(expectedFields);
  for (const field of expected) {
    if (!properties.has(field)) throw new Error(`${label} is missing field ${field}`);
  }
  for (const field of properties.keys()) {
    if (!expected.has(field)) throw new Error(`${label} has extra field ${field}`);
  }
}

function nonBlankStringLiteralValue(node, label) {
  const value = stringLiteralValue(node, label);
  if (!value.trim()) throw new Error(`${label} must be non-blank`);
  return value;
}

function stringLiteralArrayValue(node, label) {
  if (node?.type !== "ArrayExpression") throw new Error(`${label} must be an array literal`);
  return node.elements.map((element, index) =>
    nonBlankStringLiteralValue(element, `${label}[${index}]`),
  );
}

function rpcMethodReferenceValue(node, key) {
  if (node?.type === "StringLiteral") return { key: "", method: node.value };
  if (
    node?.type === "MemberExpression" &&
    !node.computed &&
    node.object.type === "Identifier" &&
    node.object.name === "RPC_METHODS" &&
    node.property.type === "Identifier" &&
    node.property.name === key
  ) {
    return { key, method: "" };
  }
  throw new Error(`RPC_CONTRACT_REGISTRY.${key} method must reference RPC_METHODS.${key}`);
}

function responseValidatorPolicyMatches(policyName, implementationName) {
  if (!implementationName) return false;
  const expectedPolicyName = responseValidatorPolicyName(implementationName);
  return policyName.toLowerCase() === expectedPolicyName.toLowerCase();
}

function responseValidatorPolicyName(implementationName) {
  const explicitPolicyName = RESPONSE_VALIDATOR_POLICY_EXCEPTIONS.get(implementationName);
  if (explicitPolicyName) return explicitPolicyName;
  const implementationStem = implementationName.replace(/^validate/, "");
  return implementationStem
    .replace(/^[A-Z]+(?=[A-Z][a-z]|$)/, (prefix) => prefix.toLowerCase())
    .replace(/^./, (prefix) => prefix.toLowerCase());
}

function collectResponseValidatorFindings(registryEntries, runtimeValidators) {
  const registryByKey = new Map(registryEntries.map((entry) => [entry.key, entry]));
  const keys = new Set([
    ...runtimeValidators.keys(),
    ...registryEntries
      .filter((entry) => entry.responseValidator.trim() !== "")
      .map((entry) => entry.key),
  ]);
  const findings = [];
  for (const key of keys) {
    const entry = registryByKey.get(key);
    const implementationName = runtimeValidators.get(key) ?? "";
    const runtimeResponseValidator = implementationName
      ? responseValidatorPolicyName(implementationName)
      : "";
    const responseValidator = entry?.responseValidator ?? "";
    if (
      entry &&
      implementationName &&
      responseValidatorPolicyMatches(responseValidator, implementationName)
    ) {
      continue;
    }
    findings.push({
      key,
      method: entry?.method ?? "",
      responseValidator,
      runtimeResponseValidator,
    });
  }
  return findings.sort((a, b) => a.key.localeCompare(b.key));
}

export { collectResponseValidatorFindings, parseContractMatrix, parseRpcMethods };
