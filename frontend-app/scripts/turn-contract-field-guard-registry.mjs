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
import { immutableRepositoryBaseline } from "./turn-contract-field-guard-baseline.mjs";

const registryRelativePath = "internal/dto/turn/schema/field_consumers.json";

export function canonicalSchemas(repoRoot, sourceOverrides) {
  const schemas = new Map();
  for (const relativePath of immutableRepositoryBaseline(repoRoot)
    .canonicalSchemaPaths) {
    const schemaFile = path.posix.basename(relativePath);
    const schema = parseJSON(
      readRepositorySource(repoRoot, relativePath, sourceOverrides),
      `canonical schema ${schemaFile}`,
    );
    if (!schema.title || !isRecord(schema.properties))
      throw new Error(
        `canonical schema ${schemaFile} must have title and properties`,
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
