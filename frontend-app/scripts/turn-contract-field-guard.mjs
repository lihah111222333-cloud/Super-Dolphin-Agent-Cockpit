import path from "node:path";
import process from "node:process";
import { fileURLToPath, pathToFileURL } from "node:url";

import { generatedSchemas } from "../src/shared/contracts/turnContracts.generated.js";
import {
  createValidatorExportResolver,
  consumerKey,
  discoverJSValidatorConsumers,
} from "./turn-contract-field-guard-evidence.mjs";
import {
  canonicalSchemas,
  loadRegistry,
  validateSchemaRegistryEntry,
} from "./turn-contract-field-guard-registry.mjs";
import {
  assertExactSet,
  isRecord,
  readRepositorySource,
  stableJSON,
} from "./turn-contract-field-guard-utils.mjs";
import {
  validateJSTerminalChains,
  validateMapperSource,
} from "./turn-contract-field-guard-validation.mjs";

const appRoot = path.resolve(
  path.dirname(fileURLToPath(import.meta.url)),
  "..",
);
const defaultRepoRoot = path.resolve(appRoot, "..");

export function validateTurnContractFieldGuard({
  repoRoot = defaultRepoRoot,
  sourceOverrides = new Map(),
} = {}) {
  const schemas = canonicalSchemas(repoRoot, sourceOverrides);
  const registry = loadRegistry(repoRoot, sourceOverrides);
  if (registry.version !== 2 || !isRecord(registry.schemas)) {
    throw new Error(
      "consumer registry must be version 2 with a schemas object",
    );
  }
  const schemaNames = [...schemas.keys()].sort();
  assertExactSet(
    "consumer registry schemas",
    schemaNames,
    Object.keys(registry.schemas).sort(),
  );
  const targetSchemas = new Map(
    Object.entries(registry.schemas).map(([schemaName, entry]) => [
      entry.jsValidator.symbol,
      schemaName,
    ]),
  );
  const resolveValidatorExports = createValidatorExportResolver(
    repoRoot,
    sourceOverrides,
    targetSchemas,
  );
  const discoveredConsumers = discoverJSValidatorConsumers(
    repoRoot,
    sourceOverrides,
    targetSchemas,
    resolveValidatorExports,
  );
  for (const [schemaName, schema] of schemas) {
    const entry = registry.schemas[schemaName];
    validateSchemaRegistryEntry(
      repoRoot,
      sourceOverrides,
      schemaName,
      entry,
      targetSchemas,
      resolveValidatorExports,
    );
    assertExactSet(
      `${schemaName} JS production consumers`,
      discoveredConsumers.get(schemaName) ?? [],
      entry.jsConsumers.map(consumerKey).sort(),
    );
    if (stableJSON(schema) !== stableJSON(generatedSchemas[schemaName]))
      throw new Error(`generated JS schema drift: ${schemaName}`);
  }
  if (!Array.isArray(registry.jsMappers) || registry.jsMappers.length === 0)
    throw new Error("consumer registry must contain JS mapper chains");
  validateJSTerminalChains(
    repoRoot,
    sourceOverrides,
    registry.jsTerminalChains,
  );
  const mapperNames = new Set();
  for (const mapper of registry.jsMappers) {
    if (!mapper?.name || mapperNames.has(mapper.name))
      throw new Error(
        `JS mapper has blank or duplicate name ${mapper?.name ?? ""}`,
      );
    mapperNames.add(mapper.name);
    validateMapperSource(
      readRepositorySource(repoRoot, mapper.path, sourceOverrides),
      mapper,
    );
  }
  return { schemaCount: schemas.size, mapperCount: registry.jsMappers.length };
}

if (
  process.argv[1] &&
  pathToFileURL(path.resolve(process.argv[1])).href === import.meta.url
) {
  try {
    const report = validateTurnContractFieldGuard();
    console.log(
      `turn contract field guard passed: schemas=${report.schemaCount} mappers=${report.mapperCount}`,
    );
  } catch (error) {
    console.error(`turn contract field guard failed: ${error.message}`);
    process.exitCode = 1;
  }
}
