import {
  enclosingSymbol,
  parseModuleCached,
  readProperty,
  staticStringBindings,
  walkNode,
  writtenProperty,
} from './frontend-state-ownership-ast.mjs';
import { assertUniqueRecords } from './frontend-state-ownership-registry.mjs';

export function discoverRecordsByRoot({
  definitions,
  rootField,
  sourcesForRoot,
  discover,
  parseCache,
  analysisCache,
}) {
  const propertiesByRoot = new Map();
  for (const [, definition] of definitions) {
    const root = definition[rootField];
    const properties = propertiesByRoot.get(root) || new Set();
    definition.properties.forEach((property) => properties.add(property));
    propertiesByRoot.set(root, properties);
  }
  const recordsByRoot = new Map();
  for (const [sourceRoot, properties] of propertiesByRoot) {
    recordsByRoot.set(
      sourceRoot,
      discover(sourcesForRoot(sourceRoot), [...properties], parseCache, analysisCache),
    );
  }
  return recordsByRoot;
}

export function recordsForProperties(records = [], properties) {
  return records.filter(({ key }) => properties.some((property) => key.endsWith(`:${property}`)));
}

export function discoverStateWritersFromSources(sources, properties) {
  return discoverStateWriterRecordsFromSources(sources, properties).map(({ key }) => key);
}

export function discoverStateWriterRecordsFromSources(
  sources,
  properties,
  parseCache = new Map(),
  analysisCache = new Map(),
) {
  const guardedProperties = new Set(properties);
  const writers = [];
  for (const [relativePath, source] of [...sources].sort(([left], [right]) => left.localeCompare(right))) {
    writers.push(...analyzeSourceCached({
      analysisCache,
      analysisKind: 'writers',
      guardedProperties,
      parseCache,
      relativePath,
      source,
      analyze(ast) {
        const records = [];
        const stringBindings = staticStringBindings(ast);
        walkNode(ast, [], (node, ancestors) => {
          const property = writtenProperty(node, stringBindings);
          if (!property || !guardedProperties.has(property.name)) return;
          const symbol = enclosingSymbol(ancestors);
          records.push({
            key: `${relativePath}:${symbol}:${property.operation}:${property.name}`,
            value: property.value,
          });
        });
        return records;
      },
    }));
  }
  assertUniqueRecords('discovered state writer', writers);
  return writers.sort((left, right) => left.key.localeCompare(right.key));
}

export function discoverStateConsumersFromSources(
  sources,
  properties,
  parseCache = new Map(),
  analysisCache = new Map(),
) {
  const guardedProperties = new Set(properties);
  const consumers = new Map();
  for (const [relativePath, source] of [...sources].sort(([left], [right]) => left.localeCompare(right))) {
    const records = analyzeSourceCached({
      analysisCache,
      analysisKind: 'consumers',
      guardedProperties,
      parseCache,
      relativePath,
      source,
      analyze(ast) {
        const discovered = new Map();
        const stringBindings = staticStringBindings(ast);
        walkNode(ast, [], (node, ancestors) => {
          const read = readProperty(node, ancestors, stringBindings);
          if (!read || !guardedProperties.has(read.name)) return;
          const symbol = enclosingSymbol(ancestors);
          const key = `${relativePath}:${symbol}:${read.operation}:${read.name}`;
          discovered.set(key, { key });
        });
        return [...discovered.values()];
      },
    });
    for (const record of records) consumers.set(record.key, record);
  }
  return [...consumers.values()].sort((left, right) => left.key.localeCompare(right.key));
}

function analyzeSourceCached({
  analysisCache,
  analysisKind,
  guardedProperties,
  parseCache,
  relativePath,
  source,
  analyze,
}) {
  const propertyKey = [...guardedProperties].sort().join('\0');
  const cacheKey = `${analysisKind}\0${relativePath}\0${propertyKey}`;
  const cached = analysisCache.get(cacheKey);
  if (cached?.source === source) return cached.records;
  const records = analyze(parseModuleCached(source, relativePath, parseCache));
  analysisCache.set(cacheKey, { records, source });
  return records;
}
