import fs from "node:fs";
import path from "node:path";
import {
  isRecord,
  parseJSON,
  readRepositorySource,
  validateLocatorShape,
} from "./turn-contract-field-guard-utils.mjs";
import {
  functionHasCall,
  functionHasValidatorCall,
  resolveJSFunction,
} from "./turn-contract-field-guard-validation.mjs";

const registryRelativePath = "internal/dto/turn/schema/field_consumers.json";

export function canonicalSchemas(repoRoot, sourceOverrides) {
  const schemaDir = path.join(repoRoot, "internal/dto/turn/schema");
  const schemas = new Map();
  for (const entry of fs.readdirSync(schemaDir, { withFileTypes: true })) {
    if (
      entry.isDirectory() ||
      !entry.name.endsWith(".json") ||
      entry.name === "field_consumers.json"
    )
      continue;
    const schema = parseJSON(
      readRepositorySource(
        repoRoot,
        `internal/dto/turn/schema/${entry.name}`,
        sourceOverrides,
      ),
      `canonical schema ${entry.name}`,
    );
    if (!schema.title || !isRecord(schema.properties))
      throw new Error(
        `canonical schema ${entry.name} must have title and properties`,
      );
    if (schemas.has(schema.title))
      throw new Error(`duplicate canonical schema ${schema.title}`);
    schemas.set(schema.title, schema);
  }
  if (schemas.size !== 3)
    throw new Error(
      `expected exactly three canonical turn schemas, found ${schemas.size}`,
    );
  return schemas;
}
export function loadRegistry(repoRoot, sourceOverrides) {
  return parseJSON(
    readRepositorySource(repoRoot, registryRelativePath, sourceOverrides),
    "consumer registry",
  );
}
export function validateSchemaRegistryEntry(
  repoRoot,
  sourceOverrides,
  schemaName,
  entry,
  targetSchemas,
  resolveValidatorExports,
) {
  if (!isRecord(entry))
    throw new Error(`consumer registry missing schema ${schemaName}`);
  validateLocatorShape(repoRoot, entry.goType, ".go");
  validateLocatorShape(repoRoot, entry.goValidator, ".go");
  validateCallLocators(
    repoRoot,
    entry.goConsumers,
    ".go",
    `${schemaName} Go consumers`,
  );
  const validator = resolveJSFunction(
    repoRoot,
    sourceOverrides,
    entry.jsValidator,
  );
  if (!functionHasCall(validator.fn, "validateNamedSchema", schemaName))
    throw new Error(
      `${schemaName} JS validator does not call validateNamedSchema for its schema`,
    );
  if (!Array.isArray(entry.jsConsumers) || entry.jsConsumers.length === 0)
    throw new Error(`${schemaName} has no JS production consumers`);
  for (const consumer of entry.jsConsumers) {
    const resolved = resolveJSFunction(repoRoot, sourceOverrides, consumer);
    if (
      !functionHasValidatorCall(
        repoRoot,
        sourceOverrides,
        resolved,
        consumer,
        targetSchemas,
        resolveValidatorExports,
      )
    )
      throw new Error(
        `${consumer.path}:${consumer.symbol} missing call ${consumer.calls}`,
      );
  }
}
function validateCallLocators(repoRoot, locators, extension, label) {
  if (!Array.isArray(locators) || locators.length === 0)
    throw new Error(`${label} must not be empty`);
  for (const locator of locators) {
    validateLocatorShape(repoRoot, locator, extension);
    if (!locator.calls)
      throw new Error(`${label} contains a blank call target`);
  }
}
