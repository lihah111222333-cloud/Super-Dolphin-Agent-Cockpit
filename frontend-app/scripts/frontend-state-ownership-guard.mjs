import path from 'node:path';
import process from 'node:process';
import { fileURLToPath } from 'node:url';

import {
  discoverRecordsByRoot,
  discoverStateConsumersFromSources,
  discoverStateWriterRecordsFromSources,
  discoverStateWritersFromSources,
  recordsForProperties,
} from './frontend-state-ownership-model.mjs';
import {
  assertExactRecords,
  assertExactSet,
  validateRegistryShape,
  validateStateDefinition,
} from './frontend-state-ownership-registry.mjs';
import { discoverCaseIds, productionSources, readJSON, readSource } from './frontend-state-ownership-source.mjs';

const appRoot = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..');
const registryPath = 'scripts/frontend-state-ownership-registry.json';
const sharedParseCache = new Map();
const sharedAnalysisCache = new Map();

export { discoverStateConsumersFromSources, discoverStateWriterRecordsFromSources, discoverStateWritersFromSources };

export function validateFrontendStateOwnership({
  root = appRoot,
  registry = readJSON(path.join(root, registryPath)),
  sourceOverrides = new Map(),
} = {}) {
  validateRegistryShape(registry);
  const actualCaseIds = discoverCaseIds(readSource(root, registry.testFile, sourceOverrides), registry.testFile, 'A02-');
  assertExactSet('A02 regression case IDs', registry.caseIds, actualCaseIds);

  const definitions = Object.entries(registry.states);
  for (const [stateId, definition] of definitions) validateStateDefinition(stateId, definition);

  const sourcesByRoot = new Map();
  const sourcesForRoot = (sourceRoot) => {
    if (!sourcesByRoot.has(sourceRoot)) {
      const allSources = sourcesByRoot.get('src');
      if (sourceRoot !== 'src' && allSources && sourceRoot.startsWith('src/')) {
        sourcesByRoot.set(
          sourceRoot,
          new Map([...allSources].filter(([relativePath]) => relativePath.startsWith(`${sourceRoot}/`))),
        );
      } else {
        sourcesByRoot.set(sourceRoot, productionSources(root, sourceRoot, sourceOverrides));
      }
    }
    return sourcesByRoot.get(sourceRoot);
  };
  for (const consumerRoot of new Set(definitions.map(([, definition]) => definition.consumerRoot))) {
    sourcesForRoot(consumerRoot);
  }
  const writerRecordsByRoot = discoverRecordsByRoot({
    definitions,
    rootField: 'sourceRoot',
    sourcesForRoot,
    discover: discoverStateWriterRecordsFromSources,
    parseCache: sharedParseCache,
    analysisCache: sharedAnalysisCache,
  });
  const consumerRecordsByRoot = discoverRecordsByRoot({
    definitions,
    rootField: 'consumerRoot',
    sourcesForRoot,
    discover: discoverStateConsumersFromSources,
    parseCache: sharedParseCache,
    analysisCache: sharedAnalysisCache,
  });

  const summaries = {};
  for (const [stateId, definition] of definitions) {
    const discoveredWriters = recordsForProperties(
      writerRecordsByRoot.get(definition.sourceRoot),
      definition.properties,
    );
    assertExactRecords(`${stateId} writers`, definition.writers, discoveredWriters, ['key', 'value']);
    const owners = definition.writers.filter(({ role }) => role === 'owner');
    if (owners.length !== 1 || owners[0].key !== definition.ownerWriter) {
      throw new Error(`${stateId} must have exactly one owner writer matching ownerWriter`);
    }
    const discoveredConsumers = recordsForProperties(
      consumerRecordsByRoot.get(definition.consumerRoot),
      definition.properties,
    );
    assertExactRecords(`${stateId} consumers`, definition.consumers, discoveredConsumers, ['key']);
    summaries[stateId] = {
      consumerCount: discoveredConsumers.length,
      writerCount: discoveredWriters.length,
      ownerWriter: definition.ownerWriter,
    };
  }
  return summaries;
}

if (process.argv[1] && path.resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  const summaries = validateFrontendStateOwnership();
  console.log(`frontend state ownership guard passed (${Object.keys(summaries).length} states)`);
}
