export function validateRegistryShape(registry) {
  if (registry?.version !== 1 || !isRecord(registry.states) || Object.keys(registry.states).length === 0) {
    throw new Error('state ownership registry must be version 1 with non-empty states');
  }
  if (!Array.isArray(registry.caseIds) || registry.caseIds.length === 0) {
    throw new Error('state ownership registry must register at least one regression case');
  }
  if (typeof registry.testFile !== 'string' || !registry.testFile.endsWith('.test.mjs')) {
    throw new Error('state ownership registry testFile must name a test module');
  }
}

export function validateStateDefinition(stateId, definition) {
  if (
    !isRecord(definition)
    || typeof definition.sourceRoot !== 'string'
    || typeof definition.consumerRoot !== 'string'
  ) {
    throw new Error(`${stateId} state definition is incomplete`);
  }
  if (!Array.isArray(definition.properties) || definition.properties.length === 0) {
    throw new Error(`${stateId} must guard at least one property`);
  }
  if (!Array.isArray(definition.writers) || definition.writers.length === 0) {
    throw new Error(`${stateId} must register at least one writer`);
  }
  if (!Array.isArray(definition.consumers) || definition.consumers.length === 0) {
    throw new Error(`${stateId} must register at least one consumer`);
  }
  const keys = new Set();
  for (const writer of definition.writers) {
    if (
      !isRecord(writer)
      || typeof writer.key !== 'string'
      || typeof writer.value !== 'string'
      || !['owner', 'initializer', 'projector'].includes(writer.role)
    ) {
      throw new Error(`${stateId} writer registration is incomplete`);
    }
    if (keys.has(writer.key)) throw new Error(`${stateId} has duplicate writer ${writer.key}`);
    keys.add(writer.key);
  }
  const consumerKeys = new Set();
  for (const consumer of definition.consumers) {
    if (
      !isRecord(consumer)
      || typeof consumer.key !== 'string'
      || !['contract-validator', 'diagnostics', 'mapper-input', 'renderer', 'selector'].includes(consumer.role)
    ) {
      throw new Error(`${stateId} consumer registration is incomplete`);
    }
    if (consumerKeys.has(consumer.key)) throw new Error(`${stateId} has duplicate consumer ${consumer.key}`);
    consumerKeys.add(consumer.key);
  }
}

export function assertExactSet(label, expectedValues, actualValues) {
  const expected = [...new Set(expectedValues)].sort();
  const actual = [...new Set(actualValues)].sort();
  const missing = actual.filter((value) => !expected.includes(value));
  const stale = expected.filter((value) => !actual.includes(value));
  if (missing.length > 0 || stale.length > 0) {
    throw new Error(`${label} registry drift: missing=${JSON.stringify(missing)} stale=${JSON.stringify(stale)}`);
  }
}

export function assertExactRecords(label, expectedRecords, actualRecords, fields) {
  const serialize = (record) => fields.map((field) => `${field}=${record[field]}`).join('|');
  assertExactSet(label, expectedRecords.map(serialize), actualRecords.map(serialize));
}

export function assertUniqueRecords(label, records) {
  const seen = new Set();
  for (const record of records) {
    if (seen.has(record.key)) throw new Error(`${label} is duplicated: ${record.key}`);
    seen.add(record.key);
  }
}

function isRecord(value) {
  return value !== null && typeof value === 'object' && !Array.isArray(value);
}
