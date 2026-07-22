import { existsSync, readdirSync, readFileSync, statSync } from 'node:fs';
import { extname, resolve } from 'node:path';
import { parse } from '@babel/parser';

const CLIENT_STORE_IDENTIFIERS = Object.freeze(new Set(['store', 'sourceStore']));
const ROUTE_CONSUMER_PATHS = Object.freeze([
  'src/App.jsx',
  'src/AppRoutes.jsx',
  'src/pages/chat',
  'src/pages/files',
  'src/pages/prompts',
  'src/pages/workflows',
]);
const ROUTE_STORE_CONSUMER_EXEMPTIONS = Object.freeze({
  selectThread: 'ChatPage citation adapters may supply the optional selectThread capability; useClientStore does not.',
});

function sourceFiles(path) {
  if (!existsSync(path)) throw new Error(`route consumer path is missing: ${path}`);
  if (!statSync(path).isDirectory()) return [path];
  return readdirSync(path, { withFileTypes: true })
    .filter((entry) => !entry.isDirectory() || !['__tests__', 'test', 'tests', 'spec', 'specs'].includes(entry.name))
    .flatMap((entry) => sourceFiles(resolve(path, entry.name)))
    .filter((entry) => ['.js', '.jsx'].includes(extname(entry))
      && !/\.(?:test|spec)\.[^.]+$/i.test(entry)
      && !/(?:test|spec)[-_]?support\.[^.]+$/i.test(entry));
}

function collectObjectPatternKeys(pattern, keys) {
  pattern.properties.forEach((property) => {
    if (property.type !== 'ObjectProperty' || property.computed || property.key.type !== 'Identifier') {
      throw new Error('route store destructuring must use static identifier keys');
    }
    keys.add(property.key.name);
  });
}

function storeKeysFromSource(source, filePath) {
  const ast = parse(source, { plugins: ['jsx'], sourceType: 'module' });
  const keys = new Set();
  const visited = new WeakSet();
  const visit = (value) => {
    if (!value || typeof value !== 'object' || visited.has(value)) return;
    visited.add(value);
    if (['MemberExpression', 'OptionalMemberExpression'].includes(value.type)
      && value.object?.type === 'Identifier' && CLIENT_STORE_IDENTIFIERS.has(value.object.name)) {
      if (value.computed || value.property?.type !== 'Identifier') {
        throw new Error('route store member access must use a static identifier key');
      }
      keys.add(value.property.name);
    }
    if (value.type === 'VariableDeclarator' && value.id?.type === 'ObjectPattern'
      && value.init?.type === 'Identifier' && CLIENT_STORE_IDENTIFIERS.has(value.init.name)) {
      collectObjectPatternKeys(value.id, keys);
    }
    Object.values(value).forEach(visit);
  };
  try {
    visit(ast);
  } catch (error) {
    throw new Error(`${filePath}: ${error.message}`);
  }
  return keys;
}

function collectRouteStoreConsumerKeys(frontendRoot) {
  const files = ROUTE_CONSUMER_PATHS.flatMap((path) => sourceFiles(resolve(frontendRoot, path))).sort();
  const keys = new Set();
  files.forEach((filePath) => {
    storeKeysFromSource(readFileSync(filePath, 'utf8'), filePath).forEach((key) => keys.add(key));
  });
  return Object.freeze([...keys].sort());
}

function validateAppShellStoreContract({
  consumerKeys,
  exemptions = ROUTE_STORE_CONSUMER_EXEMPTIONS,
  producerKeys,
  selectorKeys,
}) {
  const producer = new Set(producerKeys);
  const selector = new Set(selectorKeys);
  const exemptionKeys = Object.keys(exemptions).sort();
  exemptionKeys.forEach((key) => {
    if (typeof exemptions[key] !== 'string' || exemptions[key].trim().length === 0) {
      throw new Error(`AppShell store contract exemption ${key} requires a reason`);
    }
  });
  const unknownConsumers = consumerKeys.filter((key) => !producer.has(key));
  const unknown = unknownConsumers.filter((key) => !exemptionKeys.includes(key));
  const staleExemptions = exemptionKeys.filter((key) => !unknownConsumers.includes(key));
  const expectedSelectorKeys = consumerKeys.filter((key) => producer.has(key));
  const expectedSelector = new Set(expectedSelectorKeys);
  const missing = expectedSelectorKeys.filter((key) => !selector.has(key));
  const stale = selectorKeys.filter((key) => !producer.has(key));
  const unusedLiveProducer = selectorKeys.filter((key) => producer.has(key) && !expectedSelector.has(key));
  if (unknown.length > 0 || missing.length > 0 || stale.length > 0
    || unusedLiveProducer.length > 0 || staleExemptions.length > 0) {
    throw new Error(`AppShell store contract mismatch: unknown=[${unknown}] missing=[${missing}] stale=[${stale}] unusedLiveProducer=[${unusedLiveProducer}] staleExemptions=[${staleExemptions}]`);
  }
  return Object.freeze({
    consumerKeys: Object.freeze([...consumerKeys]),
    producerKeys: Object.freeze([...producerKeys]),
    selectorKeys: Object.freeze([...selectorKeys]),
  });
}

export {
  ROUTE_CONSUMER_PATHS,
  ROUTE_STORE_CONSUMER_EXEMPTIONS,
  collectRouteStoreConsumerKeys,
  sourceFiles,
  storeKeysFromSource,
  validateAppShellStoreContract,
};
