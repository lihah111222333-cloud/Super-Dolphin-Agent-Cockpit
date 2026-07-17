import { lstat, readFile, readdir, realpath } from 'node:fs/promises'
import { readFileSync } from 'node:fs'
import { dirname, isAbsolute, join, normalize, relative, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'
import { parse as parseJavaScriptSource } from '@babel/parser'

const SCRIPT_DIR = dirname(fileURLToPath(import.meta.url))
const DEFAULT_FRONTEND_ROOT = resolve(SCRIPT_DIR, '..')
const DEFAULT_REPO_ROOT = resolve(DEFAULT_FRONTEND_ROOT, '..')

const RPC_METHODS_PATH = 'frontend-app/src/shared/api/backend/backendRpcMethods.js'
const RPC_FACADE_PATH = 'frontend-app/src/shared/api/backendApi.js'
const FRONTEND_PAYLOAD_BUILDERS_PATH = 'frontend-app/src/shared/api/backend/backendApiFactoryThread.js'
const TURN_INTERRUPT_INJECTION_PATH = 'frontend-app/src/entities/client/model/helpers/a1/clientStoreThreadActions.js'
const TURN_INTERRUPT_RUNTIME_PATH = 'frontend-app/src/entities/client/model/threadLifecycleRuntime.js'
const TURN_INTERRUPT_REGRESSION_PATH = 'frontend-app/src/entities/client/model/threadLifecycleRuntime.test.js'
const RPC_FACADE_REEXPORT_SOURCE = './backend/backendRpcMethods.js'
const RPC_RESPONSE_VALIDATORS_PATH = 'frontend-app/src/shared/api/backendResponseValidators.js'
const RPC_MATRIX_PATH = 'frontend-app/src/shared/api/backendApi.contractMatrix.js'
const BACKEND_API_FACTORY_PATHS = [
  'frontend-app/src/shared/api/backend/backendApiFactoryCore.js',
  'frontend-app/src/shared/api/backend/backendApiFactoryOps.js',
  'frontend-app/src/shared/api/backend/backendApiFactoryThread.js',
]
const DIRECT_FACADE_RPC_LOCATORS = new Map([
  ['UI_LOG', {
    facade: 'sendFrontendLogBatch',
    implementationPath: 'frontend-app/src/shared/api/wails/wailsBridgeRpc.js',
    methodPath: 'frontend-app/src/shared/api/wails/wailsBridgeRpc.js',
    method: 'ui/log',
  }],
  ['OBSERVABILITY_FRONTEND_INGEST', {
    facade: 'emitFrontendTraceEvent',
    implementationPath: 'frontend-app/src/shared/api/wails/wailsBridgeTraceEvents.js',
    methodPath: 'frontend-app/src/shared/api/wails/wailsBridgeConstants.js',
    method: 'observability/frontend/ingest',
  }],
])
const GO_RPC_CONSTANTS_PATH = 'internal/contract/rpc_handler.go'
const GO_HANDLER_ROOTS = ['internal', 'cmd']
const GO_PAYLOAD_STRUCTS = new Map([
  ['thread/start', [
    'internal/module/thread/rpc_types.go:startParams',
    'internal/module/thread/rpc_types.go:startParamCompatFields',
  ]],
  ['turn/start', [
    'internal/module/turn/rpc_types.go:turnStartParams',
    'internal/module/turn/rpc_types.go:legacyTurnStartParams',
  ]],
  ['turn/steer', [
    'internal/module/turn/rpc_types.go:turnSteerParams',
    'internal/module/turn/rpc_types.go:legacyTurnSteerParams',
  ]],
  ['turn/interrupt', [
    'internal/module/turn/rpc_types.go:turnInterruptParams',
    'internal/module/turn/rpc_types.go:legacyTurnInterruptParams',
  ]],
])

const FRONTEND_PAYLOAD_BUILDERS = new Map([
  ['thread/start', 'threadStartPayload'],
  ['turn/start', 'turnStartPayload'],
  ['turn/interrupt', 'turnInterruptPayload'],
])

const RESPONSE_VALIDATOR_POLICY_EXCEPTIONS = new Map([
  ['validateControlResponse', 'mcpServerControlResponse'],
])

const SERVICE_FACADE_LOCATORS = new Map([
  ['OBSERVABILITY_TRACE_GET', 'frontend-app/src/pages/observability/services/observabilityPageService.js'],
  ['OBSERVABILITY_RECENT_LIST', 'frontend-app/src/pages/observability/services/observabilityPageService.js'],
  ['UI_MEMORY_ENTRY_GET', 'frontend-app/src/pages/memory/services/memoryPageService.js'],
  ['UI_MEMORY_ENTRY_UPSERT', 'frontend-app/src/pages/memory/services/memoryPageService.js'],
  ['UI_MEMORY_ENTRY_DELETE', 'frontend-app/src/pages/memory/services/memoryPageService.js'],
  ['UI_MEMORY_AUTO_DREAM_SET_INTENT', 'frontend-app/src/pages/memory/services/memoryPageService.js'],
  ['UI_MEMORY_ENTRY_MERGE', 'frontend-app/src/pages/memory/services/memoryPageService.js'],
  ['UI_MEMORY_SIMILARITY_IGNORE', 'frontend-app/src/pages/memory/services/memoryPageService.js'],
  ['UI_MEMORY_SIMILARITY_CONSOLIDATE_ALL_START', 'frontend-app/src/pages/memory/services/memoryPageService.js'],
  ['UI_MEMORY_SIMILARITY_CONSOLIDATE_ALL_STATUS', 'frontend-app/src/pages/memory/services/memoryPageService.js'],
  ['UI_SHARED_FILE_GET', 'frontend-app/src/pages/files/services/filesPageService.js'],
  ['PROMPT_ASSETS_LIST', 'frontend-app/src/pages/prompts/services/promptPageService.js'],
  ['DASHBOARD_PROMPTS', 'frontend-app/src/pages/prompts/services/promptPageService.js'],
  ['PROMPTS_GET', 'frontend-app/src/pages/prompts/services/promptPageService.js'],
  ['PROMPTS_WRITE', 'frontend-app/src/pages/prompts/services/promptPageService.js'],
  ['PROMPTS_DELETE', 'frontend-app/src/pages/prompts/services/promptPageService.js'],
  ['PROMPT_INTENTS_DRAFT', 'frontend-app/src/pages/prompts/services/promptPageService.js'],
  ['PROMPT_INTENTS_COMMIT', 'frontend-app/src/pages/prompts/services/promptPageService.js'],
  ['PROMPT_INTENTS_DISCARD', 'frontend-app/src/pages/prompts/services/promptPageService.js'],
  ['PROMPT_INTENTS_DRY_RUN', 'frontend-app/src/pages/prompts/services/promptPageService.js'],
  ['PERSONALIZATION_PROFILE_GET', 'frontend-app/src/pages/prompts/services/promptPageService.js'],
  ['PERSONALIZATION_PROFILE_SAVE', 'frontend-app/src/pages/prompts/services/promptPageService.js'],
])

const FRONTEND_PAYLOAD_METHOD_EXEMPTIONS = new Map([
  ['turn/steer', 'turn/steer is provider-facing and has no React facade builder'],
])

const FRONTEND_FACADE_ONLY_PAYLOAD_KEYS = new Map([
  ['thread/start', [
    'agentKey',
    'codexModelProvider',
    'codex_model_provider',
    'deferSpawn',
    'optimisticUserMessage',
    'optimistic_user_message',
    'promptKey',
    'skipInitialRuntimeSync',
    'skip_initial_runtime_sync',
  ]],
  ['turn/start', [
    'attachments',
  ]],
  ['turn/interrupt', [
    'cwd',
  ]],
])

const GO_HANDLER_CALLS = [
  'StrictHandler',
  'LoggedStrictHandler',
  'ThreadHandler',
  'CapabilityThreadHandler',
]

export async function auditRpcContracts({ repoRoot = DEFAULT_REPO_ROOT } = {}) {
  repoRoot = await realpath(repoRoot)
  const auditContext = {
    repoRoot,
    sourceByPath: new Map(),
    sourcePromiseByPath: new Map(),
    astByPath: new Map(),
    astPromiseByPath: new Map(),
    productionFacadeReferenceIndex: null,
    auditStats: {
      sourceReads: 0,
      astParses: 0,
      productionFacadeReferenceIndexBuilds: 0,
      productionSourceFilesIndexed: 0,
    },
  }
  const [rpcMethodsSource, frontendSource, payloadBuildersSource, matrixSource, responseValidatorSource] = await Promise.all([
    readAuditSource(auditContext, RPC_METHODS_PATH),
    readAuditSource(auditContext, RPC_FACADE_PATH),
    readAuditSource(auditContext, FRONTEND_PAYLOAD_BUILDERS_PATH),
    readAuditSource(auditContext, RPC_MATRIX_PATH),
    readAuditSource(auditContext, RPC_RESPONSE_VALIDATORS_PATH),
  ])
  assertRpcMethodsFacadeReExport(frontendSource)
  const rpcMethods = parseRpcMethods(
    rpcMethodsSource,
  )
  const methodsByKey = new Map(rpcMethods.map((entry) => [entry.key, entry]))
  const parsedRegistryEntries = parseContractMatrix(
    matrixSource,
  )
  const registryEntries = parsedRegistryEntries.map((entry) => ({
    ...entry,
    method: entry.methodReferenceKey ? methodsByKey.get(entry.methodReferenceKey)?.method ?? '' : entry.method,
  }))
  const [backendHandlers, goPayloadKeysByMethod, hardcodedPayloadGuardFindings, backendFacadeRpcKeys] = await Promise.all([
    collectGoRpcHandlers(auditContext),
    collectGoPayloadKeys(auditContext),
    collectHardcodedPayloadGuardFindings(auditContext, payloadBuildersSource),
    collectBackendFacadeRpcKeys(auditContext),
  ])
  const frontendPayloadKeysByMethod = collectFrontendPayloadKeysFromSource(payloadBuildersSource)

  const registryByKey = new Map(registryEntries.map((entry) => [entry.key, entry]))
  const handlerMethods = new Set(backendHandlers.map((entry) => entry.method))
  const frontendResponseValidators = collectFrontendResponseValidators(responseValidatorSource)
  const responseContractStrategies = registryEntries
    .concat(rpcMethods.filter((entry) => !registryByKey.has(entry.key)))
    .map((entry) => ({
      key: entry.key,
      method: entry.method,
      matrixPolicy: entry.responseValidator || entry.responsePassthroughReason || '',
      frontendValidator: frontendResponseValidators.has(entry.key),
    }))

  const missingRegistryKeys = rpcMethods
    .filter((entry) => !registryByKey.has(entry.key))
    .map((entry) => entry.key)
    .sort()
  const registryWithoutRpcMethods = registryEntries
    .filter((entry) => !methodsByKey.has(entry.key))
    .map((entry) => entry.key)
    .sort()
  const mismatchedRegistryMethods = parsedRegistryEntries
    .filter((entry) => methodsByKey.has(entry.key))
    .filter((entry) => !entry.methodReferenceKey && entry.method !== methodsByKey.get(entry.key).method)
    .map((entry) => ({
      key: entry.key,
      registryMethod: entry.method,
      rpcMethod: methodsByKey.get(entry.key).method,
    }))
    .sort((a, b) => a.key.localeCompare(b.key))
  const p0MissingBackendHandlers = registryEntries
    .filter((entry) => entry.level === 'P0' && !handlerMethods.has(entry.method))
    .map((entry) => ({
      key: entry.key,
      method: entry.method,
    }))
  const allowedPayloadRegistryDrift = collectPayloadRegistryDrift(goPayloadKeysByMethod, frontendPayloadKeysByMethod)
  const missingResponsePolicies = registryEntries
    .filter((entry) => entry.level === 'P0' || entry.level === 'P1')
    .filter((entry) => !entry.responseValidator.trim() && !entry.responsePolicy)
    .map((entry) => ({
      key: entry.key,
      method: entry.method,
    }))
  const missingFrontendResponseValidators = collectResponseValidatorFindings(
    registryEntries,
    frontendResponseValidators,
  )
  const invalidFacadeLocators = await collectInvalidFacadeLocators(
    auditContext,
    registryEntries,
    frontendSource,
    backendFacadeRpcKeys,
  )
  const invalidResponsePolicyEvidence = await collectInvalidResponsePolicyEvidence(
    auditContext,
    registryEntries,
    backendFacadeRpcKeys,
  )

  return {
    rpcMethods,
    registryEntries,
    backendHandlers,
    missingRegistryKeys,
    registryWithoutRpcMethods,
    mismatchedRegistryMethods,
    p0MissingBackendHandlers,
    goPayloadKeysByMethod,
    frontendPayloadKeysByMethod,
    allowedPayloadRegistryDrift,
    hardcodedPayloadGuardFindings,
    missingResponsePolicies,
    responseContractStrategies,
    missingFrontendResponseValidators,
    invalidFacadeLocators,
    invalidResponsePolicyEvidence,
    auditStats: { ...auditContext.auditStats },
  }
}

export function formatRpcAuditReport(report) {
  return [
    `RPC methods: ${report.rpcMethods.length}`,
    `Contract registry entries: ${report.registryEntries.length}`,
    `Go backend handlers: ${report.backendHandlers.length}`,
    `Missing registry keys: ${report.missingRegistryKeys.length}`,
    `Registry entries without RPC_METHODS: ${report.registryWithoutRpcMethods.length}`,
    `Mismatched registry methods: ${report.mismatchedRegistryMethods.length}`,
    `P0 methods missing Go handlers: ${report.p0MissingBackendHandlers.length}`,
    `Allowed payload registry drift: ${report.allowedPayloadRegistryDrift.length}`,
    `Hardcoded payload guards: ${report.hardcodedPayloadGuardFindings.length}`,
    `Missing response policies: ${report.missingResponsePolicies.length}`,
    `Missing frontend response validators: ${report.missingFrontendResponseValidators.length}`,
    `Invalid facade locators: ${report.invalidFacadeLocators.length}`,
    `Invalid response policy evidence: ${report.invalidResponsePolicyEvidence.length}`,
  ].join('\n')
}
export function assertAuditPasses(report) {
  const findings = [
    report.missingRegistryKeys,
    report.registryWithoutRpcMethods,
    report.mismatchedRegistryMethods,
    report.p0MissingBackendHandlers,
    report.allowedPayloadRegistryDrift,
    report.hardcodedPayloadGuardFindings,
    report.missingResponsePolicies,
    report.missingFrontendResponseValidators,
    report.invalidFacadeLocators,
    report.invalidResponsePolicyEvidence,
  ]
  if (findings.some((values) => values.length > 0)) {
    throw new Error(formatRpcAuditReport(report))
  }
}

function parseRpcMethods(source) {
  const objectExpression = findFrozenObjectExport(source, 'RPC_METHODS')
  if (!objectExpression) {
    throw new Error('RPC_METHODS object was not found in backend/backendRpcMethods.js')
  }

  return objectPropertiesOnly(objectExpression, 'RPC_METHODS')
    .map((property) => ({
      key: propertyKeyName(property),
      method: stringLiteralValue(property.value, `RPC_METHODS.${propertyKeyName(property)}`),
    }))
}

function parseContractMatrix(source) {
  const objectExpression = findFrozenObjectExport(source, 'RPC_CONTRACT_REGISTRY')
  if (!objectExpression) {
    throw new Error('RPC_CONTRACT_REGISTRY object was not found in backendApi.contractMatrix.js')
  }

  const entries = objectPropertiesOnly(objectExpression, 'RPC_CONTRACT_REGISTRY')
    .map((property) => parseContractRegistryProperty(property))

  const badKey = entries.find((entry) => entry.key !== entry.declaredKey)
  if (badKey) {
    throw new Error(`Contract key mismatch: ${badKey.key} declares ${badKey.declaredKey}`)
  }

  return entries
}

export const parseRpcMethodsForTest = parseRpcMethods
export const parseContractMatrixForTest = parseContractMatrix
export const astReferencesFacadeForTest = astReferencesFacade

function parseContractRegistryProperty(property) {
  const key = propertyKeyName(property)
  if (property.value.type !== 'CallExpression' || property.value.callee.type !== 'Identifier' || property.value.callee.name !== 'contract') {
    throw new Error(`RPC_CONTRACT_REGISTRY.${key} must call contract(...)`)
  }
  const args = property.value.arguments
  const options = args[8]?.type === 'ObjectExpression' ? args[8] : null
  const methodReference = rpcMethodReferenceValue(args[1], key)
  const level = stringLiteralValue(args[3], `RPC_CONTRACT_REGISTRY.${key} level`)
  const responseMetadata = parseResponseMetadata(options, key, level)
  return {
    key,
    declaredKey: stringLiteralValue(args[0], `RPC_CONTRACT_REGISTRY.${key} declared key`),
    method: methodReference.method,
    methodReferenceKey: methodReference.key,
    facade: stringLiteralValue(args[2], `RPC_CONTRACT_REGISTRY.${key} facade`),
    level,
    ...responseMetadata,
  }
}

function parseResponseMetadata(options, key, level) {
  const label = `RPC_CONTRACT_REGISTRY.${key}`
  const properties = strictObjectProperties(options, `${label} options`)
  if (properties.has('responsePassthroughReason')) {
    throw new Error(`${label} responsePassthroughReason is forbidden`)
  }
  const allowed = new Set(['responseValidator', 'responsePolicy'])
  for (const name of properties.keys()) {
    if (!allowed.has(name)) throw new Error(`${label} options has extra field ${name}`)
  }
  const hasValidator = properties.has('responseValidator')
  const hasPolicy = properties.has('responsePolicy')
  if ((level === 'P0' || level === 'P1') && hasValidator === hasPolicy) {
    throw new Error(`${label} must declare exactly one of responseValidator or responsePolicy`)
  }
  if (hasValidator && hasPolicy) {
    throw new Error(`${label} must declare exactly one of responseValidator or responsePolicy`)
  }
  const responseValidator = hasValidator
    ? nonBlankStringLiteralValue(properties.get('responseValidator').value, `${label} responseValidator`)
    : ''
  const responsePolicy = hasPolicy
    ? parseResponsePolicy(properties.get('responsePolicy').value, label)
    : null
  return { responseValidator, responsePolicy }
}

function parseResponsePolicy(node, label) {
  if (node?.type !== 'ObjectExpression') throw new Error(`${label} responsePolicy must be an object literal`)
  const properties = strictObjectProperties(node, `${label} responsePolicy`)
  const kind = nonBlankStringLiteralValue(properties.get('kind')?.value, `${label} responsePolicy.kind`)
  const fieldsByKind = new Map([
    ['ignored-result', ['kind', 'consumer', 'regressionTest']],
    ['result-handled', ['kind', 'consumer', 'handler', 'regressionTest']],
    ['consumer-validated', ['kind', 'consumer', 'shape', 'regressionTest']],
    ['unused', ['kind', 'productionScanRoots', 'excludedGlobs']],
  ])
  const expectedFields = fieldsByKind.get(kind)
  if (!expectedFields) throw new Error(`${label} responsePolicy.kind is invalid: ${kind}`)
  const policyFields = kind === 'ignored-result' && properties.has('outcome')
    ? [...expectedFields, 'outcome']
    : expectedFields
  assertExactFields(properties, policyFields, `${label} responsePolicy`)
  if (kind === 'unused') {
    const productionScanRoots = stringLiteralArrayValue(
      properties.get('productionScanRoots').value,
      `${label} responsePolicy.productionScanRoots`,
    )
    if (productionScanRoots.length !== 1 || productionScanRoots[0] !== 'frontend-app/src') {
      throw new Error(`${label} responsePolicy.productionScanRoots must equal ['frontend-app/src']`)
    }
    return {
      kind,
      productionScanRoots,
      excludedGlobs: stringLiteralArrayValue(
        properties.get('excludedGlobs').value,
        `${label} responsePolicy.excludedGlobs`,
      ),
    }
  }
  const responsePolicy = {
    kind,
    consumer: parseResponsePolicyLocator(
      properties.get('consumer').value,
      `${label} responsePolicy.consumer`,
      { allowModulePrivate: true },
    ),
    regressionTest: parseResponsePolicyLocator(
      properties.get('regressionTest').value,
      `${label} responsePolicy.regressionTest`,
    ),
  }
  if (kind === 'ignored-result' && properties.has('outcome')) {
    responsePolicy.outcome = parseIgnoredResultOutcome(
      properties.get('outcome').value,
      `${label} responsePolicy.outcome`,
    )
  }
  if (kind === 'consumer-validated') {
    responsePolicy.shape = parseResponsePolicyLocator(
      properties.get('shape').value,
      `${label} responsePolicy.shape`,
    )
  }
  if (kind === 'result-handled') {
    responsePolicy.handler = parseResponsePolicyLocator(
      properties.get('handler').value,
      `${label} responsePolicy.handler`,
    )
  }
  return responsePolicy
}

function parseIgnoredResultOutcome(node, label) {
  if (node?.type !== 'ObjectExpression') throw new Error(`${label} must be an object literal`)
  const properties = strictObjectProperties(node, label)
  assertExactFields(properties, ['kind', 'target'], label)
  const kind = nonBlankStringLiteralValue(properties.get('kind')?.value, `${label}.kind`)
  if (kind !== 'published-callback') throw new Error(`${label}.kind must equal published-callback`)
  const target = stringLiteralArrayValue(properties.get('target').value, `${label}.target`)
  if (target.length === 0 || target.some((part) => !part.trim())) {
    throw new Error(`${label}.target must contain non-blank strings`)
  }
  return { kind, target }
}

function parseResponsePolicyLocator(node, label, { allowModulePrivate = false } = {}) {
  if (node?.type !== 'ObjectExpression') throw new Error(`${label} must be an object literal`)
  const properties = strictObjectProperties(node, label)
  const requiredFields = new Set(['path', 'symbol'])
  const allowedFields = new Set([...requiredFields, ...(allowModulePrivate ? ['visibility'] : [])])
  for (const field of requiredFields) {
    if (!properties.has(field)) throw new Error(`${label} is missing field ${field}`)
  }
  for (const field of properties.keys()) {
    if (!allowedFields.has(field)) throw new Error(`${label} has extra field ${field}`)
  }
  const locator = {
    path: stringLiteralValue(properties.get('path').value, `${label}.path`),
    symbol: stringLiteralValue(properties.get('symbol').value, `${label}.symbol`),
  }
  if (properties.has('visibility')) {
    const visibility = stringLiteralValue(properties.get('visibility').value, `${label}.visibility`)
    if (visibility !== 'module-private') {
      throw new Error(`${label}.visibility must equal module-private`)
    }
    locator.visibility = visibility
  }
  return locator
}

function strictObjectProperties(objectExpression, label) {
  const properties = new Map()
  if (!objectExpression) return properties
  for (const property of objectExpression.properties) {
    if (property.type !== 'ObjectProperty') throw new Error(`${label} must not contain spread or methods`)
    if (property.computed) throw new Error(`${label} must not contain computed fields`)
    const name = propertyKeyName(property)
    if (properties.has(name)) throw new Error(`${label} has duplicate field ${name}`)
    properties.set(name, property)
  }
  return properties
}

function assertExactFields(properties, expectedFields, label) {
  const expected = new Set(expectedFields)
  for (const field of expected) {
    if (!properties.has(field)) throw new Error(`${label} is missing field ${field}`)
  }
  for (const field of properties.keys()) {
    if (!expected.has(field)) throw new Error(`${label} has extra field ${field}`)
  }
}

function nonBlankStringLiteralValue(node, label) {
  const value = stringLiteralValue(node, label)
  if (!value.trim()) throw new Error(`${label} must be non-blank`)
  return value
}

function stringLiteralArrayValue(node, label) {
  if (node?.type !== 'ArrayExpression') throw new Error(`${label} must be an array literal`)
  return node.elements.map((element, index) => (
    nonBlankStringLiteralValue(element, `${label}[${index}]`)
  ))
}

function rpcMethodReferenceValue(node, key) {
  if (node?.type === 'StringLiteral') return { key: '', method: node.value }
  if (
    node?.type === 'MemberExpression'
    && !node.computed
    && node.object.type === 'Identifier'
    && node.object.name === 'RPC_METHODS'
    && node.property.type === 'Identifier'
    && node.property.name === key
  ) {
    return { key, method: '' }
  }
  throw new Error(`RPC_CONTRACT_REGISTRY.${key} method must reference RPC_METHODS.${key}`)
}

function responseValidatorPolicyMatches(policyName, implementationName) {
  if (!implementationName) return false
  const expectedPolicyName = responseValidatorPolicyName(implementationName)
  return policyName.toLowerCase() === expectedPolicyName.toLowerCase()
}

function responseValidatorPolicyName(implementationName) {
  const explicitPolicyName = RESPONSE_VALIDATOR_POLICY_EXCEPTIONS.get(implementationName)
  if (explicitPolicyName) return explicitPolicyName
  const implementationStem = implementationName.replace(/^validate/, '')
  return implementationStem
    .replace(/^[A-Z]+(?=[A-Z][a-z]|$)/, (prefix) => prefix.toLowerCase())
    .replace(/^./, (prefix) => prefix.toLowerCase())
}

function collectResponseValidatorFindings(registryEntries, runtimeValidators) {
  const registryByKey = new Map(registryEntries.map((entry) => [entry.key, entry]))
  const keys = new Set([
    ...runtimeValidators.keys(),
    ...registryEntries.filter((entry) => entry.responseValidator.trim() !== '').map((entry) => entry.key),
  ])
  const findings = []
  for (const key of keys) {
    const entry = registryByKey.get(key)
    const implementationName = runtimeValidators.get(key) ?? ''
    const runtimeResponseValidator = implementationName
      ? responseValidatorPolicyName(implementationName)
      : ''
    const responseValidator = entry?.responseValidator ?? ''
    if (
      entry
      && implementationName
      && responseValidatorPolicyMatches(responseValidator, implementationName)
    ) {
      continue
    }
    findings.push({
      key,
      method: entry?.method ?? '',
      responseValidator,
      runtimeResponseValidator,
    })
  }
  return findings.sort((a, b) => a.key.localeCompare(b.key))
}

async function collectInvalidFacadeLocators(auditContext, registryEntries, frontendSource, backendFacadeRpcKeys) {
  const backendApiExports = collectNamedExports(frontendSource)
  const serviceSources = new Map()
  const findings = []
  for (const entry of registryEntries) {
    if ((entry.level !== 'P0' && entry.level !== 'P1') || entry.responseValidator.trim() !== '') continue
    if (!entry.facade.includes('.')) {
      if (
        !backendApiExports.has(entry.facade)
        || backendFacadeRpcKeys.get(entry.facade) !== entry.key
      ) {
        findings.push({ key: entry.key, facade: entry.facade, locator: RPC_FACADE_PATH })
      }
      continue
    }
    const locator = SERVICE_FACADE_LOCATORS.get(entry.key) ?? ''
    if (!locator) {
      findings.push({ key: entry.key, facade: entry.facade, locator })
      continue
    }
    let source = serviceSources.get(locator)
    if (!source) {
      source = await readAuditSource(auditContext, locator)
      serviceSources.set(locator, source)
    }
    const [serviceName, memberName, ...extra] = entry.facade.split('.')
    if (
      extra.length > 0
      || serviceFacadeMemberRpcKey(source, serviceName, memberName, backendFacadeRpcKeys) !== entry.key
    ) {
      findings.push({ key: entry.key, facade: entry.facade, locator })
    }
  }
  return findings.sort((a, b) => a.key.localeCompare(b.key))
}

async function collectInvalidResponsePolicyEvidence(auditContext, registryEntries, backendFacadeRpcKeys) {
  const findings = []
  const unusedEntries = registryEntries.filter((entry) => entry.responsePolicy?.kind === 'unused')
  if (unusedEntries.length > 0) {
    auditContext.productionFacadeReferenceIndex = await buildProductionFacadeReferenceIndex(
      auditContext,
      unusedEntries,
      backendFacadeRpcKeys,
    )
  }
  for (const entry of registryEntries) {
    const policy = entry.responsePolicy
    if (!policy) continue
    if (policy.kind === 'unused') {
      findings.push(...collectUnusedPolicyFindings(auditContext, entry))
      continue
    }
    const consumer = await resolvePolicyLocator(auditContext, entry, 'consumer', policy.consumer, false, findings)
    const regressionTest = await resolvePolicyLocator(
      auditContext,
      entry,
      'regressionTest',
      policy.regressionTest,
      true,
      findings,
    )
    const consumerSymbol = consumer
      ? findResponsePolicyConsumerSymbol(consumer.ast, policy.consumer)
      : null
    let consumerCalls = consumerSymbol
      ? await findFacadeCalls(
        auditContext,
        consumer.ast,
        consumerSymbol,
        consumer.path,
        entry,
        backendFacadeRpcKeys,
      )
      : []
    const publishedCallbackProof = policy.kind === 'ignored-result' && policy.outcome && consumerSymbol
      ? await publishedCallbackProductionProof(
        auditContext,
        consumer.ast,
        consumer.path,
        consumerSymbol,
        policy.outcome,
        entry,
      )
      : null
    if (publishedCallbackProof) consumerCalls = [publishedCallbackProof.call]
    const consumerOutcomeProof = policy.kind === 'ignored-result' && !policy.outcome && consumerSymbol && consumerCalls.length === 1
      ? await collectIgnoredResultConsumerOutcomeProof(
        auditContext,
        consumer.ast,
        consumerSymbol,
        consumerCalls[0],
        consumer.path,
      )
      : null
    if (
      regressionTest
      && !hasRegressionTestEvidence(
        regressionTest.ast,
        regressionTest.path,
        policy.regressionTest.symbol,
        policy.consumer,
        policy.kind,
        entry,
        publishedCallbackProof ?? consumerOutcomeProof,
      )
    ) {
      findings.push(policyFinding(
        entry,
        'regressionTest',
        policy.regressionTest,
        'test callback lacks executable assertions tied to the consumer and RPC key',
      ))
    }
    if (!consumer) continue
    if (!consumerSymbol) {
      findings.push(policyFinding(entry, 'consumer', policy.consumer, 'symbol was not found'))
      continue
    }
    if (policy.kind === 'ignored-result' && policy.outcome && !publishedCallbackProof) {
      findings.push(policyFinding(entry, 'consumer', policy.consumer, 'consumer lacks the exact post-RPC published callback outcome'))
      continue
    }
    const exactTurnInterruptPolicy = isExactTurnInterruptPolicy(entry)
    const resultRuntimeInjection = exactTurnInterruptPolicy
      && await provesTurnInterruptInjection(auditContext, entry)
    if (consumerCalls.length === 0 && !resultRuntimeInjection) {
      findings.push(policyFinding(entry, 'consumer', policy.consumer, 'symbol does not call the facade for this RPC key'))
      continue
    }
    if (policy.kind === 'ignored-result') {
      if (consumerCalls.some((call) => !isIgnoredCallResult(call.ancestors))) {
        findings.push(policyFinding(entry, 'consumer', policy.consumer, 'consumer reads the RPC result'))
        continue
      }
      if (consumerCalls.length > 1) {
        findings.push(policyFinding(entry, 'consumer', policy.consumer, 'consumer calls the facade more than once'))
      }
      continue
    }
    if (policy.kind === 'result-handled') {
      const handler = await resolvePolicyLocator(auditContext, entry, 'handler', policy.handler, false, findings)
      if (!handler) continue
      const handlerSymbol = findModuleLevelSymbol(handler.ast, policy.handler.symbol)
      if (!handlerSymbol) {
        findings.push(policyFinding(entry, 'handler', policy.handler, 'symbol was not found'))
        continue
      }
      const directResultFlow = consumerCalls.length === 1 && consumerPassesFacadeResultToHandler(
        consumer.ast, consumerSymbol, consumerCalls[0]?.node, consumer.path, policy.handler,
      )
      const runtimeResultFlow = exactTurnInterruptPolicy && resultRuntimeInjection
        && runtimePassesAwaitedResultToHandler(
          handler.ast,
          handlerSymbol,
          policy.handler.symbol,
          policy.consumer.symbol,
        )
      if (!directResultFlow && !runtimeResultFlow) {
        findings.push(policyFinding(entry, 'consumer', policy.consumer, 'consumer does not pass the observed RPC result to the located handler'))
        continue
      }
      if (!handlerDirectlyInspectsEnvelope(handlerSymbol, responsePolicyRpcMethod(entry), handler.ast)) {
        findings.push(policyFinding(entry, 'handler', policy.handler, 'handler lacks direct executable envelope outcome handling'))
      }
      continue
    }
    if (consumerCalls.length !== 1) {
      findings.push(policyFinding(entry, 'consumer', policy.consumer, 'consumer must contain exactly one facade call'))
      continue
    }
    const [consumerCall] = consumerCalls
    const shape = await resolvePolicyLocator(auditContext, entry, 'shape', policy.shape, false, findings)
    if (!shape) continue
    const shapeSymbol = findProductionSymbol(shape.ast, policy.shape.symbol)
    if (!shapeSymbol) {
      findings.push(policyFinding(entry, 'shape', policy.shape, 'symbol was not found'))
      continue
    }
    if (!hasExecutableShapeNarrowing(shapeSymbol, shape.ast)) {
      findings.push(policyFinding(entry, 'shape', policy.shape, 'shape symbol lacks executable narrowing'))
      continue
    }
    if (!shapeDominatesConsumerUse(
      consumer.ast,
      consumerSymbol,
      consumerCall.node,
      consumer.path,
      shape.path,
      policy.shape.symbol,
    )) {
      findings.push(policyFinding(entry, 'shape', policy.shape, 'shape proof does not dominate consumer use'))
    }
  }
  return findings.sort(comparePolicyFindings)
}

function responsePolicyRpcMethod(entry) {
  if (entry.key === 'TURN_INTERRUPT') return 'thread.interrupt'
  return entry.method
}

async function resolvePolicyLocator(auditContext, entry, field, locator, requireTestFile, findings) {
  const { repoRoot } = auditContext
  if (!locator.path.trim()) {
    findings.push(policyFinding(entry, field, locator, 'path must be non-blank'))
    return null
  }
  if (!locator.symbol.trim()) {
    findings.push(policyFinding(entry, field, locator, 'symbol must be non-blank'))
    return null
  }
  const normalizedPath = normalize(locator.path).replaceAll('\\', '/')
  const absolutePath = resolve(repoRoot, locator.path)
  const relativePath = relative(repoRoot, absolutePath).replaceAll('\\', '/')
  if (
    isAbsolute(locator.path)
    || normalizedPath !== locator.path
    || relativePath === '..'
    || relativePath.startsWith('../')
    || isAbsolute(relativePath)
  ) {
    findings.push(policyFinding(entry, field, locator, 'path must be normalized and repository-confined'))
    return null
  }
  if (requireTestFile && !/\.(?:test|spec)\.(?:js|jsx|mjs)$/.test(locator.path)) {
    findings.push(policyFinding(entry, field, locator, 'path must identify a JavaScript test file'))
    return null
  }
  if (await pathContainsSymbolicLink(repoRoot, locator.path)) {
    findings.push(policyFinding(entry, field, locator, 'path must not resolve through a symbolic link'))
    return null
  }
  try {
    const canonicalPath = await realpath(absolutePath)
    const canonicalRelative = relative(repoRoot, canonicalPath).replaceAll('\\', '/')
    if (
      canonicalRelative === '..'
      || canonicalRelative.startsWith('../')
      || isAbsolute(canonicalRelative)
    ) {
      findings.push(policyFinding(entry, field, locator, 'path must be normalized and repository-confined'))
      return null
    }
    return { ast: await readAuditAst(auditContext, locator.path), path: locator.path }
  } catch (error) {
    if (error?.code === 'ENOENT') {
      findings.push(policyFinding(entry, field, locator, 'file does not exist'))
      return null
    }
    throw error
  }
}

async function pathContainsSymbolicLink(repoRoot, filePath) {
  let current = repoRoot
  for (const segment of filePath.split('/')) {
    current = join(current, segment)
    try {
      if ((await lstat(current)).isSymbolicLink()) return true
    } catch (error) {
      if (error?.code === 'ENOENT') return false
      throw error
    }
  }
  return false
}

async function readAuditSource(auditContext, filePath) {
  const cached = auditContext.sourceByPath.get(filePath)
  if (cached !== undefined) return cached
  let pending = auditContext.sourcePromiseByPath.get(filePath)
  if (!pending) {
    auditContext.auditStats.sourceReads += 1
    pending = readFile(join(auditContext.repoRoot, filePath), 'utf8')
    auditContext.sourcePromiseByPath.set(filePath, pending)
  }
  const source = await pending
  auditContext.sourceByPath.set(filePath, source)
  auditContext.sourcePromiseByPath.delete(filePath)
  return source
}

function readAuditSourceSync(auditContext, filePath) {
  const cached = auditContext.sourceByPath.get(filePath)
  if (cached !== undefined) return cached
  auditContext.auditStats.sourceReads += 1
  const source = readFileSync(join(auditContext.repoRoot, filePath), 'utf8')
  auditContext.sourceByPath.set(filePath, source)
  return source
}

async function readAuditAst(auditContext, filePath) {
  const cached = auditContext.astByPath.get(filePath)
  if (cached) return cached
  let pending = auditContext.astPromiseByPath.get(filePath)
  if (!pending) {
    pending = readAuditSource(auditContext, filePath).then((source) => {
      auditContext.auditStats.astParses += 1
      return parseFrontendAst(source)
    })
    auditContext.astPromiseByPath.set(filePath, pending)
  }
  const ast = await pending
  auditContext.astByPath.set(filePath, ast)
  auditContext.astPromiseByPath.delete(filePath)
  return ast
}

function policyFinding(entry, field, locator, reason) {
  return {
    key: entry.key,
    kind: entry.responsePolicy.kind,
    field,
    path: locator?.path ?? '',
    symbol: locator?.symbol ?? '',
    reason,
  }
}

function comparePolicyFindings(left, right) {
  return [
    left.key,
    left.kind,
    left.field,
    left.path,
    left.symbol,
    left.reason,
  ].join('\u0000').localeCompare([
    right.key,
    right.kind,
    right.field,
    right.path,
    right.symbol,
    right.reason,
  ].join('\u0000'))
}

function findProductionSymbol(ast, symbol) {
  const exportedLocalNames = new Set()
  const candidates = []
  for (const statement of ast.program.body) {
    if (statement.type !== 'ExportNamedDeclaration' || statement.source) continue
    for (const specifier of statement.specifiers) {
      if (moduleExportName(specifier.exported) === symbol) {
        exportedLocalNames.add(moduleExportName(specifier.local))
      }
    }
    const declaration = statement.declaration
    if (
      (declaration?.type === 'FunctionDeclaration' || declaration?.type === 'ClassDeclaration')
      && declaration.id?.name === symbol
    ) {
      exportedLocalNames.add(symbol)
    }
    if (declaration?.type === 'VariableDeclaration') {
      for (const item of declaration.declarations) {
        if (item.id.type === 'Identifier' && item.id.name === symbol) exportedLocalNames.add(symbol)
      }
    }
  }
  if (exportedLocalNames.size !== 1) return null
  const [localName] = exportedLocalNames
  for (const statement of ast.program.body) {
    const declaration = statement.type === 'ExportNamedDeclaration' ? statement.declaration : statement
    if (
      (declaration?.type === 'FunctionDeclaration' || declaration?.type === 'ClassDeclaration')
      && declaration.id?.name === localName
    ) {
      candidates.push(declaration)
    }
    if (declaration?.type === 'VariableDeclaration') {
      for (const item of declaration.declarations) {
        if (item.id.type === 'Identifier' && item.id.name === localName) candidates.push(item.init ?? item)
      }
    }
  }
  return candidates.length === 1 ? candidates[0] : null
}

function findResponsePolicyConsumerSymbol(ast, locator) {
  if (locator.visibility === 'module-private') {
    return findModulePrivateFunctionSymbol(ast, locator.symbol)
  }
  return findProductionSymbol(ast, locator.symbol)
}

function findModulePrivateFunctionSymbol(ast, symbol) {
  if (findProductionSymbol(ast, symbol)) return null
  const candidates = []
  walkAstWithAncestors(ast, (node) => {
    if (node.type === 'FunctionDeclaration' && node.id?.name === symbol) {
      candidates.push(node)
      return
    }
    if (
      node.type === 'VariableDeclarator'
      && node.id.type === 'Identifier'
      && node.id.name === symbol
    ) {
      if (node.init?.type === 'ArrowFunctionExpression' || node.init?.type === 'FunctionExpression') {
        candidates.push(node.init)
        return
      }
      const callback = node.init?.type === 'CallExpression' ? node.init.arguments[0] : null
      if (callback?.type === 'ArrowFunctionExpression' || callback?.type === 'FunctionExpression') {
        candidates.push(callback)
      }
    }
  })
  return candidates.length === 1 ? candidates[0] : null
}

function findModuleLevelSymbol(ast, symbol) {
  const candidates = []
  for (const statement of ast.program.body) {
    const declaration = statement.type === 'ExportNamedDeclaration' ? statement.declaration : statement
    if (
      (declaration?.type === 'FunctionDeclaration' || declaration?.type === 'ClassDeclaration')
      && declaration.id?.name === symbol
    ) candidates.push(declaration)
    if (declaration?.type === 'VariableDeclaration') {
      for (const item of declaration.declarations) {
        if (item.id.type === 'Identifier' && item.id.name === symbol) candidates.push(item.init ?? item)
      }
    }
  }
  return candidates.length === 1 ? candidates[0] : null
}

function hasRegressionTestEvidence(
  ast,
  testPath,
  symbol,
  consumerLocator,
  policyKind,
  entry,
  consumerOutcomeProof = null,
) {
  const consumerAliases = new Set()
  for (const statement of ast.program.body) {
    if (
      statement.type !== 'ImportDeclaration'
      || !moduleSpecifierResolvesTo(testPath, statement.source.value, consumerLocator.path)
    ) {
      continue
    }
    for (const specifier of statement.specifiers) {
      if (
        specifier.type === 'ImportSpecifier'
        && moduleExportName(specifier.imported) === consumerLocator.symbol
      ) {
        consumerAliases.add(specifier.local.name)
      }
    }
  }
  if (policyKind === 'result-handled') {
    return hasResultHandledRegressionEvidence(ast, testPath, symbol, consumerAliases, entry)
  }
  if (policyKind === 'ignored-result' && entry.responsePolicy?.outcome) {
    return hasPublishedCallbackRegressionEvidence(
      ast,
      symbol,
      consumerAliases,
      consumerOutcomeProof,
    )
  }
  if (
    policyKind === 'ignored-result'
    && hasDirectFacadeIgnoredResultRegressionEvidence(ast, symbol, entry)
  ) return true
  if (
    policyKind === 'ignored-result'
    && consumerAliases.size === 0
    && hasPageIgnoredResultRegressionEvidence(ast, symbol, entry, consumerOutcomeProof)
  ) return true
  if (consumerAliases.size === 0) return false
  const malformedFacadeMocked = policyKind !== 'consumer-validated'
    || hasMalformedFacadeMock(ast, testPath, entry)
  let found = false
  traverseAst(ast, (node) => {
    if (
      found
      || node.type !== 'CallExpression'
      || node.callee.type !== 'Identifier'
      || (node.callee.name !== 'it' && node.callee.name !== 'test')
      || node.arguments[0]?.type !== 'StringLiteral'
      || node.arguments[0].value !== symbol
    ) {
      return
    }
    const callback = node.arguments[1]
    if (
      (callback?.type !== 'ArrowFunctionExpression' && callback?.type !== 'FunctionExpression')
      || callback.body.type !== 'BlockStatement'
      || hasNonTestRunnerBinding(ast, node.callee.name)
    ) {
      return
    }
    const consumerCalls = new Set()
    const consumerResultNames = new Set()
    walkAstWithAncestors(callback.body, (candidate, ancestors) => {
      if (
        candidate.type === 'CallExpression'
        && candidate.callee.type === 'Identifier'
        && consumerAliases.has(candidate.callee.name)
        && !bindingShadowsNameAt([callback, ...ancestors], candidate.callee.name)
      ) {
        consumerCalls.add(candidate)
        const parent = ancestors.at(-1)
        const declarator = parent?.type === 'AwaitExpression' ? ancestors.at(-2) : parent
        if (declarator?.type === 'VariableDeclarator' && declarator.id.type === 'Identifier') {
          consumerResultNames.add(declarator.id.name)
        }
      }
    })
    walkAstWithAncestors(callback.body, (candidate, ancestors) => {
      if (
        found
        || candidate.type !== 'CallExpression'
        || candidate.callee.type !== 'Identifier'
        || candidate.callee.name !== 'expect'
      ) {
        return
      }
      let tiedToConsumer = false
      for (const argument of candidate.arguments) {
        traverseAst(argument, (assertedNode) => {
          if (
            consumerCalls.has(assertedNode)
            || (
              assertedNode.type === 'Identifier'
              && consumerResultNames.has(assertedNode.name)
            )
          ) {
            tiedToConsumer = true
          }
        })
      }
      if (!tiedToConsumer) return
      const matcherCall = ancestors.findLast((ancestor) => (
        ancestor.type === 'CallExpression'
        && ancestor.callee.type === 'MemberExpression'
        && nodeContainsNode(ancestor.callee.object, candidate)
      ))
      if (!matcherCall || matcherCall.callee.computed) return
      const matcherName = matcherCall.callee.property.type === 'Identifier'
        ? matcherCall.callee.property.name
        : ''
      if (policyKind === 'ignored-result' && matcherName === 'toBeUndefined') found = true
      if (
        policyKind === 'consumer-validated'
        && malformedFacadeMocked
        && (matcherName === 'toThrow' || matcherName === 'toThrowError')
        && memberChainContainsName(matcherCall.callee.object, 'rejects')
        && matcherCall.arguments.length === 1
        && isSpecificShapeFailureMatcher(matcherCall.arguments[0])
      ) {
        found = true
      }
    })
  })
  return found
}

function hasDirectFacadeIgnoredResultRegressionEvidence(ast, symbol, entry) {
  const locator = DIRECT_FACADE_RPC_LOCATORS.get(entry.key)
  if (
    !locator
    || entry.responsePolicy?.consumer?.path !== locator.implementationPath
    || entry.responsePolicy?.consumer?.symbol !== locator.facade
  ) return false
  let proven = false
  traverseAst(ast, (node) => {
    if (
      proven
      || node.type !== 'CallExpression'
      || node.callee.type !== 'Identifier'
      || (node.callee.name !== 'it' && node.callee.name !== 'test')
      || node.arguments[0]?.type !== 'StringLiteral'
      || node.arguments[0].value !== symbol
      || hasNonTestRunnerBinding(ast, node.callee.name)
    ) return
    const callback = node.arguments[1]
    if (
      (callback?.type !== 'ArrowFunctionExpression' && callback?.type !== 'FunctionExpression')
      || callback.body.type !== 'BlockStatement'
    ) return
    const rejectedMocks = collectRejectedMockBindings(callback.body)
    if (rejectedMocks.size === 0) return
    let rejectsFacade = false
    let assertsMethod = false
    traverseAst(callback.body, (candidate) => {
      if (
        candidate.type !== 'CallExpression'
        || candidate.callee.type !== 'MemberExpression'
        || candidate.callee.computed
        || candidate.callee.property.type !== 'Identifier'
      ) return
      if (
        (candidate.callee.property.name === 'toThrow' || candidate.callee.property.name === 'toThrowError')
        && memberChainContainsName(candidate.callee.object, 'rejects')
      ) {
        traverseAst(candidate.callee.object, (chainNode) => {
          if (
            chainNode.type === 'CallExpression'
            && chainNode.callee.type === 'Identifier'
            && chainNode.callee.name === 'expect'
            && chainNode.arguments.some((argument) => nodeCallsIdentifier(argument, locator.facade))
          ) rejectsFacade = true
        })
      }
      if (
        candidate.callee.property.name === 'toHaveBeenCalledWith'
        && candidate.arguments.some((argument) => argument.type === 'StringLiteral' && argument.value === locator.method)
      ) {
        traverseAst(candidate.callee.object, (chainNode) => {
          if (
            chainNode.type === 'CallExpression'
            && chainNode.callee.type === 'Identifier'
            && chainNode.callee.name === 'expect'
            && chainNode.arguments.some((argument) => (
              argument.type === 'Identifier' && rejectedMocks.has(argument.name)
            ))
          ) assertsMethod = true
        })
      }
    })
    proven = rejectsFacade && assertsMethod
  })
  return proven
}

function collectRejectedMockBindings(node) {
  const bindings = new Set()
  traverseAst(node, (candidate) => {
    if (candidate.type !== 'VariableDeclarator' || candidate.id.type !== 'Identifier') return
    const init = candidate.init
    if (
      init?.type === 'CallExpression'
      && init.callee.type === 'MemberExpression'
      && !init.callee.computed
      && init.callee.property.type === 'Identifier'
      && init.callee.property.name === 'mockRejectedValue'
      && init.arguments.length === 1
    ) bindings.add(candidate.id.name)
  })
  return bindings
}

function nodeCallsIdentifier(node, name) {
  let found = false
  traverseAst(node, (candidate) => {
    if (
      candidate.type === 'CallExpression'
      && candidate.callee.type === 'Identifier'
      && candidate.callee.name === name
    ) found = true
  })
  return found
}

async function publishedCallbackProductionProof(auditContext, ast, consumerPath, consumerSymbol, outcome, entry) {
  if (!consumerSymbol?.body || !Array.isArray(outcome?.target) || outcome.target.length < 2) return null
  const facadeName = entry.facade.split('.').at(-1)
  const facadeCandidates = []
  const publisherCandidates = []
  walkAstWithAncestors(consumerSymbol.body, (node, ancestors) => {
    if (node.type !== 'CallExpression' || nestedFunctionBetween(consumerSymbol, ancestors)) return
    const calleePath = memberExpressionPath(node.callee)
    if (calleePath.length === 0) return
    if (calleePath.at(-1) === facadeName) {
      const mapped = mapLocalPathToConsumerParameter(consumerSymbol, calleePath, ancestors)
      if (mapped && mapped.path.at(-2) === 'facade') facadeCandidates.push({ node, ancestors, mapped })
    }
    if (pathsEqual(calleePath, outcome.target)) {
      const mapped = mapLocalPathToConsumerParameter(consumerSymbol, calleePath, ancestors)
      if (mapped && node.arguments.length > 0) publisherCandidates.push({ node, ancestors, mapped })
    }
  })
  if (facadeCandidates.length !== 1) return null
  const facade = facadeCandidates[0]
  const effectiveFacade = await promoteTransparentPromiseWrapperCall(
    auditContext,
    ast,
    consumerPath,
    facade,
  )
  const postPublishers = publisherCandidates.filter((publisher) => (
    callOccursLaterInSameSuccessBlock(effectiveFacade, publisher)
  ))
  if (postPublishers.length !== 1 || !isIgnoredCallResult(effectiveFacade.ancestors)) return null
  const publisher = postPublishers[0]
  return {
    kind: 'published-callback',
    call: effectiveFacade,
    facadeTarget: facade.mapped,
    publisherTarget: publisher.mapped,
  }
}

function nestedFunctionBetween(owner, ancestors) {
  return ancestors.some((ancestor) => isFunctionNode(ancestor) && ancestor !== owner)
}

function mapLocalPathToConsumerParameter(consumerSymbol, path, ancestors) {
  const root = path[0]
  if (bindingShadowsNameAt(ancestors, root)) return null
  for (let parameterIndex = 0; parameterIndex < consumerSymbol.params.length; parameterIndex += 1) {
    const parameter = consumerSymbol.params[parameterIndex]
    if (parameter.type === 'Identifier' && parameter.name === root) {
      return { parameterIndex, path: path.slice(1) }
    }
    if (parameter.type !== 'ObjectPattern') continue
    for (const property of parameter.properties) {
      if (
        property.type === 'ObjectProperty'
        && property.value.type === 'Identifier'
        && property.value.name === root
      ) {
        const externalName = staticPropertyKeyName(property)
        if (externalName) return { parameterIndex, path: [externalName, ...path.slice(1)] }
      }
    }
  }
  return null
}

function pathsEqual(left, right) {
  return left.length === right.length && left.every((part, index) => part === right[index])
}

function callOccursLaterInSameSuccessBlock(earlier, later) {
  for (let index = 0; index < earlier.ancestors.length; index += 1) {
    const block = earlier.ancestors[index]
    if (block.type !== 'BlockStatement') continue
    const earlierStatement = earlier.ancestors[index + 1]
    const earlierIndex = block.body.indexOf(earlierStatement)
    if (earlierIndex < 0) continue
    const laterBlockIndex = later.ancestors.indexOf(block)
    if (laterBlockIndex < 0) continue
    const laterStatement = later.ancestors[laterBlockIndex + 1]
    const laterIndex = block.body.indexOf(laterStatement)
    if (laterIndex > earlierIndex) return true
  }
  return false
}

function hasPublishedCallbackRegressionEvidence(ast, symbol, consumerAliases, proof) {
  if (!proof || consumerAliases.size !== 1) return false
  const [consumerAlias] = consumerAliases
  let proven = false
  traverseAst(ast, (node) => {
    if (
      proven
      || node.type !== 'CallExpression'
      || node.callee.type !== 'Identifier'
      || (node.callee.name !== 'it' && node.callee.name !== 'test')
      || node.arguments[0]?.type !== 'StringLiteral'
      || node.arguments[0].value !== symbol
      || hasNonTestRunnerBinding(ast, node.callee.name)
    ) return
    const callback = node.arguments[1]
    if (!isFunctionNode(callback) || !callback.async || callback.body.type !== 'BlockStatement') return
    const calls = []
    walkAstWithAncestors(callback.body, (candidate, ancestors) => {
      if (
        candidate.type === 'CallExpression'
        && candidate.callee.type === 'Identifier'
        && candidate.callee.name === consumerAlias
        && !nestedFunctionBetween(callback, ancestors)
        && !bindingShadowsNameAt([callback, ...ancestors], consumerAlias)
      ) calls.push({ node: candidate, ancestors })
    })
    if (calls.length !== 1) return
    const consumerCall = calls[0]
    const awaited = consumerCall.ancestors.at(-1)
    const declarator = consumerCall.ancestors.at(-2)
    if (
      awaited?.type !== 'AwaitExpression'
      || declarator?.type !== 'VariableDeclarator'
      || declarator.id.type !== 'Identifier'
    ) return
    const callStatementIndex = callbackStatementIndex(callback.body, consumerCall.node)
    if (callStatementIndex < 0) return
    const facadeRoot = consumerCall.node.arguments[proof.facadeTarget.parameterIndex]
    const publisherRoot = consumerCall.node.arguments[proof.publisherTarget.parameterIndex]
    if (facadeRoot?.type !== 'Identifier' || publisherRoot?.type !== 'Identifier') return
    const facadePaths = equivalentConsumerArgumentPaths(
      callback.body,
      facadeRoot.name,
      proof.facadeTarget.path,
    )
    const publisherPaths = equivalentConsumerArgumentPaths(
      callback.body,
      publisherRoot.name,
      proof.publisherTarget.path,
    )
    const facadeName = proof.facadeTarget.path.at(-1)
    const malformedMocks = collectExactResolvedMalformedMocks(callback.body, facadeName)
      .filter((mock) => (
        callbackStatementIndex(callback.body, mock.node) < callStatementIndex
        && facadePaths.some((path) => pathsEqual(path, mock.path))
      ))
    if (malformedMocks.length !== 1) return
    const laterStatements = callback.body.body.slice(callStatementIndex + 1)
    const facadeAssertion = laterStatements.some((statement) => facadePaths.some((path) => exactSpyMatcher(
      statement, path, 'toHaveBeenCalledWith', { requireArgument: true },
    )))
    const publisherAssertion = laterStatements.some((statement) => publisherPaths.some((path) => exactSpyMatcher(
      statement, path, 'toHaveBeenLastCalledWith', { requireArgument: true },
    )))
    const resultAssertion = laterStatements.some((statement) => exactUndefinedResultAssertion(
      statement,
      declarator.id.name,
    ))
    if (facadeAssertion && publisherAssertion && resultAssertion) proven = true
  })
  return proven
}

function callbackStatementIndex(block, node) {
  return block.body.findIndex((statement) => nodeContainsNode(statement, node))
}

function equivalentConsumerArgumentPaths(body, rootName, suffix, visited = new Set()) {
  const paths = [[rootName, ...suffix]]
  if (visited.has(rootName)) return paths
  const nextVisited = new Set(visited)
  nextVisited.add(rootName)
  for (const statement of body.body) {
    if (statement.type !== 'VariableDeclaration') continue
    for (const declaration of statement.declarations) {
      if (declaration.id.type !== 'Identifier' || declaration.id.name !== rootName) continue
      const objectArguments = declaration.init?.type === 'CallExpression'
        ? declaration.init.arguments.filter((argument) => argument.type === 'ObjectExpression')
        : declaration.init?.type === 'ObjectExpression' ? [declaration.init] : []
      for (const object of objectArguments) {
        for (let index = object.properties.length - 1; index >= 0; index -= 1) {
          const property = object.properties[index]
          if (property.type === 'ObjectProperty' && staticPropertyKeyName(property) === suffix[0]) {
            if (property.value.type === 'Identifier') paths.push([property.value.name, ...suffix.slice(1)])
            break
          }
          if (
            property.type === 'SpreadElement'
            && property.argument.type === 'Identifier'
            && bodyHasStaticRootDeclaration(body, property.argument.name)
          ) {
            paths.push(...equivalentConsumerArgumentPaths(body, property.argument.name, suffix, nextVisited))
            break
          }
        }
      }
    }
  }
  return paths
}

function bodyHasStaticRootDeclaration(body, name) {
  return body.body.some((statement) => (
    statement.type === 'VariableDeclaration'
    && statement.declarations.some((declaration) => (
      declaration.id.type === 'Identifier'
      && declaration.id.name === name
      && (declaration.init?.type === 'ObjectExpression' || declaration.init?.type === 'CallExpression')
    ))
  ))
}

function collectExactResolvedMalformedMocks(body, facadeName) {
  const mocks = []
  walkAstWithAncestors(body, (candidate, ancestors) => {
    if (
      candidate.type !== 'CallExpression'
      || candidate.arguments.length !== 1
      || !isStaticMemberNamed(candidate.callee, 'mockResolvedValue')
      || !hasMalformedSentinel(candidate.arguments[0])
      || ancestors.some((ancestor) => isFunctionNode(ancestor))
    ) return
    const property = ancestors.findLast((ancestor) => ancestor.type === 'ObjectProperty')
    if (!property || staticPropertyKeyName(property) !== facadeName) return
    const declaration = ancestors.findLast((ancestor) => (
      ancestor.type === 'VariableDeclarator' && ancestor.id.type === 'Identifier'
    ))
    if (!declaration) return
    const propertyPath = ancestors
      .filter((ancestor) => ancestor.type === 'ObjectProperty')
      .map(staticPropertyKeyName)
      .filter(Boolean)
    mocks.push({ node: candidate, path: [declaration.id.name, ...propertyPath] })
  })
  return mocks
}

function exactSpyMatcher(statement, expectedPath, matcherName, { requireArgument = false } = {}) {
  let matched = false
  traverseAst(statement, (candidate) => {
    if (
      matched
      || candidate.type !== 'CallExpression'
      || !isStaticMemberNamed(candidate.callee, matcherName)
      || (requireArgument && candidate.arguments.length === 0)
    ) return
    const expectCall = candidate.callee.object
    if (
      expectCall.type === 'CallExpression'
      && expectCall.callee.type === 'Identifier'
      && expectCall.callee.name === 'expect'
      && expectCall.arguments.length === 1
      && pathsEqual(memberExpressionPath(expectCall.arguments[0]), expectedPath)
    ) matched = true
  })
  return matched
}

function exactUndefinedResultAssertion(statement, resultName) {
  let matched = false
  traverseAst(statement, (candidate) => {
    if (matched || candidate.type !== 'CallExpression' || !isStaticMemberNamed(candidate.callee, 'toBeUndefined')) return
    const expectCall = candidate.callee.object
    if (
      expectCall.type === 'CallExpression'
      && expectCall.callee.type === 'Identifier'
      && expectCall.callee.name === 'expect'
      && expectCall.arguments.length === 1
      && expectCall.arguments[0].type === 'Identifier'
      && expectCall.arguments[0].name === resultName
    ) matched = true
  })
  return matched
}

async function collectIgnoredResultConsumerOutcomeProof(auditContext, ast, consumerSymbol, call, consumerPath) {
  const dismissals = []
  traverseAst(consumerSymbol, (node) => {
    if (node.type !== 'BlockStatement') return
    const index = node.body.findIndex((statement) => nodeContainsNode(statement, call.node))
    if (index < 0) return
    for (const statement of node.body.slice(index + 1)) {
      dismissals.push(...collectPostCallStateDismissals(statement))
    }
  })
  const controlledUi = await resolveDismissedStateUiDescriptors(
    auditContext,
    dismissals,
    { ast, path: consumerPath, symbol: consumerSymbol },
  )
  return { controlledUi }
}

function memberExpressionPath(node) {
  const path = []
  let current = node
  while (current?.type === 'MemberExpression' && !current.computed && current.property.type === 'Identifier') {
    path.unshift(current.property.name)
    current = current.object
  }
  if (current?.type === 'Identifier') path.unshift(current.name)
  return path
}

function collectPostCallStateDismissals(statement) {
  const dismissals = []
  traverseAst(statement, (candidate) => {
    if (candidate.type !== 'CallExpression') return
    const callee = stateSetterCallee(candidate.callee)
    if (!callee) return
    for (const argument of candidate.arguments) {
      if (argument.type === 'ObjectExpression') {
        for (const property of argument.properties) {
          if (property.type !== 'ObjectProperty' || !isDismissingStateValue(property.value)) continue
          const key = staticPropertyKeyName(property)
          if (key) dismissals.push({ ...callee, clearedKey: key })
        }
      } else if (isDismissingStateValue(argument)) {
        dismissals.push({ ...callee, clearedKey: '' })
      }
    }
  })
  return dismissals
}

function stateSetterCallee(callee) {
  if (callee.type === 'Identifier' && /^set[A-Z]/.test(callee.name)) {
    return { kind: 'local', setterName: callee.name }
  }
  if (
    callee.type === 'MemberExpression'
    && !callee.computed
    && callee.object.type === 'Identifier'
    && callee.property.type === 'Identifier'
    && /^set[A-Z]/.test(callee.property.name)
  ) {
    return { kind: 'member', objectName: callee.object.name, setterName: callee.property.name }
  }
  return null
}

async function resolveDismissedStateUiDescriptors(auditContext, dismissals, consumerSource) {
  if (dismissals.length === 0) return []
  const sources = await frontendProductionAstSources(auditContext)
  const descriptors = new Map()
  for (const dismissal of dismissals) {
    const bindings = findExactStateSetterBindings(sources, dismissal, consumerSource)
    if (bindings.length !== 1) continue
    const binding = bindings[0]
    const stateAccess = {
      bindingPath: binding.source.path,
      stateName: binding.stateName,
      stateProperty: dismissal.clearedKey,
      returnedProperty: binding.returnedProperty,
      setterName: dismissal.setterName,
    }
    for (const descriptor of collectStateControlledUiDescriptors(sources, stateAccess)) {
      descriptors.set(`${descriptor.role}\u0000${descriptor.name}`, descriptor)
    }
  }
  return [...descriptors.values()]
}

async function frontendProductionAstSources(auditContext) {
  if (auditContext.productionAstSources) return auditContext.productionAstSources
  const files = await listJavaScriptSourceFiles(join(auditContext.repoRoot, 'frontend-app/src'))
  const sources = []
  for (const absolutePath of files) {
    const path = relative(auditContext.repoRoot, absolutePath).replaceAll('\\', '/')
    if (isExcludedProductionScanPath(path)) continue
    sources.push({ path, ast: await readAuditAst(auditContext, path) })
  }
  auditContext.productionAstSources = sources
  return sources
}

function findExactStateSetterBindings(sources, dismissal, consumerSource) {
  if (dismissal.kind === 'member') {
    const owners = resolveMemberObjectStateOwners(
      sources,
      consumerSource.source ?? consumerSource,
      consumerSource.symbol,
      dismissal.objectName,
      new Set(),
    )
    const bindings = owners.flatMap(({ source, owner }) => stateSetterBindingsInOwner(
      source,
      owner,
      dismissal.setterName,
      true,
    ))
    return bindings.length === 1 ? bindings : []
  }
  const bindings = []
  for (const source of sources) {
    if (source.path !== consumerSource.path) continue
    walkAstWithAncestors(source.ast, (candidate, ancestors) => {
      if (
        candidate.type !== 'VariableDeclarator'
        || candidate.id.type !== 'ArrayPattern'
        || candidate.id.elements[0]?.type !== 'Identifier'
        || candidate.id.elements[1]?.type !== 'Identifier'
        || candidate.id.elements[1].name !== dismissal.setterName
        || candidate.init?.type !== 'CallExpression'
        || candidate.init.callee.type !== 'Identifier'
        || candidate.init.callee.name !== 'useState'
      ) return
      const owner = ancestors.findLast((ancestor) => isFunctionNode(ancestor))
      if (!owner) return
      const stateName = candidate.id.elements[0].name
      const returnedProperty = functionReturnsStatePair(owner, stateName, dismissal.setterName)
      bindings.push({ stateName, returnedProperty, source, owner })
    })
  }
  return bindings
}

function resolveMemberObjectStateOwners(sources, source, owner, objectName, visited) {
  if (!owner || !objectName) return []
  const visitKey = `${source.path}:${owner.start ?? ''}:${objectName}`
  if (visited.has(visitKey)) return []
  const nextVisited = new Set(visited)
  nextVisited.add(visitKey)

  for (let index = 0; index < owner.params.length; index += 1) {
    const parameter = owner.params[index]
    if (parameter.type === 'ObjectPattern') {
      const property = parameter.properties.find((candidate) => (
        candidate.type === 'ObjectProperty'
        && candidate.value.type === 'Identifier'
        && candidate.value.name === objectName
      ))
      if (property) {
        return resolveUniqueFunctionCallArgument(
          sources,
          owner,
          index,
          staticPropertyKeyName(property),
          nextVisited,
        )
      }
    }
    if (parameter.type === 'Identifier') {
      let propertyName = ''
      traverseAst(owner.body, (candidate) => {
        if (
          candidate.type !== 'VariableDeclarator'
          || candidate.id.type !== 'ObjectPattern'
          || candidate.init?.type !== 'Identifier'
          || candidate.init.name !== parameter.name
        ) return
        const property = candidate.id.properties.find((item) => (
          item.type === 'ObjectProperty'
          && item.value.type === 'Identifier'
          && item.value.name === objectName
        ))
        if (property) propertyName = staticPropertyKeyName(property)
      })
      if (propertyName) {
        return resolveUniqueFunctionCallArgument(sources, owner, index, propertyName, nextVisited)
      }
    }
  }

  let initializer = null
  traverseAst(owner.body, (candidate) => {
    if (
      !initializer
      && candidate.type === 'VariableDeclarator'
      && candidate.id.type === 'Identifier'
      && candidate.id.name === objectName
    ) initializer = candidate.init
  })
  return resolveStateOwnerExpression(sources, source, owner, initializer, nextVisited)
}

function resolveUniqueFunctionCallArgument(sources, owner, parameterIndex, propertyName, visited) {
  const functionName = owner.id?.type === 'Identifier' ? owner.id.name : ''
  if (!functionName) return []
  const values = []
  for (const source of sources) {
    walkAstWithAncestors(source.ast, (candidate, ancestors) => {
      if (
        candidate.type !== 'CallExpression'
        || candidate.callee.type !== 'Identifier'
        || candidate.callee.name !== functionName
      ) return
      let value = candidate.arguments[parameterIndex]
      if (propertyName) {
        if (value?.type !== 'ObjectExpression') return
        const property = value.properties.find((item) => (
          item.type === 'ObjectProperty' && staticPropertyKeyName(item) === propertyName
        ))
        value = property?.type === 'ObjectProperty' ? property.value : null
      }
      const callOwner = ancestors.findLast((ancestor) => isFunctionNode(ancestor))
      if (value && callOwner) values.push({ source, owner: callOwner, value })
    })
  }
  if (values.length !== 1) return []
  const value = values[0]
  return resolveStateOwnerExpression(sources, value.source, value.owner, value.value, visited)
}

function resolveStateOwnerExpression(sources, source, owner, expression, visited) {
  if (expression?.type === 'Identifier') {
    return resolveMemberObjectStateOwners(sources, source, owner, expression.name, visited)
  }
  if (expression?.type !== 'CallExpression' || expression.callee.type !== 'Identifier') return []
  const definition = findUniqueFunctionDefinition(sources, expression.callee.name)
  return definition ? [{ source: definition.source, owner: definition.node }] : []
}

function stateSetterBindingsInOwner(source, owner, setterName, requireReturnedPair) {
  const bindings = []
  walkAstWithAncestors(owner.body, (candidate, ancestors) => {
    if (
      ancestors.some((ancestor) => ancestor !== owner.body && isFunctionNode(ancestor))
      || candidate.type !== 'VariableDeclarator'
      || candidate.id.type !== 'ArrayPattern'
      || candidate.id.elements[0]?.type !== 'Identifier'
      || candidate.id.elements[1]?.type !== 'Identifier'
      || candidate.id.elements[1].name !== setterName
      || candidate.init?.type !== 'CallExpression'
      || candidate.init.callee.type !== 'Identifier'
      || candidate.init.callee.name !== 'useState'
    ) return
    const stateName = candidate.id.elements[0].name
    const returnedProperty = functionReturnsStatePair(owner, stateName, setterName)
    if (requireReturnedPair && !returnedProperty) return
    bindings.push({ stateName, returnedProperty, source, owner })
  })
  return bindings
}

function functionReturnsStatePair(owner, stateName, setterName) {
  let stateProperty = ''
  let setterReturned = false
  traverseAst(owner.body, (candidate) => {
    if (candidate.type !== 'ReturnStatement' || candidate.argument?.type !== 'ObjectExpression') return
    for (const property of candidate.argument.properties) {
      if (property.type !== 'ObjectProperty') continue
      const key = staticPropertyKeyName(property)
      if (property.value.type === 'Identifier' && property.value.name === stateName) stateProperty = key
      if (property.value.type === 'Identifier' && property.value.name === setterName) setterReturned = true
    }
  })
  return setterReturned ? stateProperty : ''
}

function collectStateControlledUiDescriptors(sources, stateAccess) {
  const descriptors = []
  const returnedObjectNames = returnedStateObjectNamesByPath(sources, stateAccess)
  const aliasesByPath = new Map()
  const controlledCandidates = []
  for (const source of sources) {
    walkAstWithAncestors(source.ast, (candidate, ancestors) => {
      if (candidate.type === 'JSXAttribute' && candidate.name.type === 'JSXIdentifier') {
        const expression = candidate.value?.type === 'JSXExpressionContainer' ? candidate.value.expression : null
        if (expression && nodeContainsStateAccess(expression, stateAccess, source.path, returnedObjectNames)) {
          const opening = ancestors.findLast((ancestor) => ancestor.type === 'JSXOpeningElement')
          const componentName = opening?.name?.type === 'JSXIdentifier' ? opening.name.name : ''
          const definition = /^[A-Z]/.test(componentName)
            ? findUniqueFunctionDefinition(sources, componentName)
            : null
          if (definition && functionAcceptsProperty(definition.node, candidate.name.name)) {
            const aliases = aliasesByPath.get(definition.source.path) ?? new Set()
            aliases.add(candidate.name.name)
            aliasesByPath.set(definition.source.path, aliases)
          }
        }
      }
      const test = candidate.type === 'ConditionalExpression'
        ? candidate.test
        : candidate.type === 'LogicalExpression' && candidate.operator === '&&'
          ? candidate.left
          : null
      if (!test) return
      controlledCandidates.push({
        candidate,
        enclosingElement: ancestors.findLast((ancestor) => ancestor.type === 'JSXElement'),
        source,
        test,
      })
    })
  }
  for (const { candidate, enclosingElement, source, test } of controlledCandidates) {
    if (
      !nodeContainsStateAccess(test, stateAccess, source.path, returnedObjectNames)
      && !nodeContainsAlias(test, aliasesByPath.get(source.path) ?? new Set())
    ) continue
    const branch = candidate.type === 'ConditionalExpression' ? candidate.consequent : candidate.right
    const controlled = nodeContainsJsxElement(branch) ? branch : enclosingElement
    if (!controlled) continue
    descriptors.push(...uiDescriptorsFromControlledNode(controlled, source.ast, sources, new Set()))
  }
  return descriptors
}

function returnedStateObjectNamesByPath(sources, access) {
  const names = new Map()
  if (!access.returnedProperty) return names
  for (const source of sources) {
    walkAstWithAncestors(source.ast, (candidate, ancestors) => {
      if (
        candidate.type !== 'MemberExpression'
        || candidate.computed
        || candidate.object.type !== 'Identifier'
        || candidate.property.type !== 'Identifier'
        || candidate.property.name !== access.returnedProperty
      ) return
      const owner = ancestors.findLast((ancestor) => isFunctionNode(ancestor))
      if (!owner || !functionContainsMemberCall(owner, candidate.object.name, access.setterName)) return
      const sourceNames = names.get(source.path) ?? new Set()
      sourceNames.add(candidate.object.name)
      names.set(source.path, sourceNames)
    })
  }
  return names
}

function functionContainsMemberCall(owner, objectName, memberName) {
  let found = false
  traverseAst(owner.body, (candidate) => {
    if (
      candidate.type === 'MemberExpression'
      && !candidate.computed
      && candidate.object.type === 'Identifier'
      && candidate.object.name === objectName
      && candidate.property.type === 'Identifier'
      && candidate.property.name === memberName
    ) found = true
  })
  return found
}

function functionAcceptsProperty(owner, propertyName) {
  return owner.params.some((parameter) => {
    if (parameter.type === 'ObjectPattern') {
      return parameter.properties.some((property) => (
        property.type === 'ObjectProperty' && staticPropertyKeyName(property) === propertyName
      ))
    }
    if (parameter.type !== 'Identifier') return false
    let accepted = false
    traverseAst(owner.body, (candidate) => {
      if (
        candidate.type === 'VariableDeclarator'
        && candidate.id.type === 'ObjectPattern'
        && candidate.init?.type === 'Identifier'
        && candidate.init.name === parameter.name
        && candidate.id.properties.some((property) => (
          property.type === 'ObjectProperty' && staticPropertyKeyName(property) === propertyName
        ))
      ) accepted = true
    })
    return accepted
  })
}

function nodeContainsStateAccess(node, access, sourcePath, returnedObjectNames) {
  let found = false
  traverseAst(node, (candidate) => {
    if (found) return
    if (
      sourcePath === access.bindingPath
      && !access.stateProperty
      && candidate.type === 'Identifier'
      && candidate.name === access.stateName
    ) found = true
    if (
      candidate.type === 'MemberExpression'
      && !candidate.computed
      && candidate.property.type === 'Identifier'
      && (
        (sourcePath === access.bindingPath && access.stateProperty && candidate.object.type === 'Identifier'
          && candidate.object.name === access.stateName && candidate.property.name === access.stateProperty)
        || (!access.stateProperty && access.returnedProperty
          && candidate.object.type === 'Identifier'
          && returnedObjectNames.get(sourcePath)?.has(candidate.object.name)
          && candidate.property.name === access.returnedProperty)
      )
    ) found = true
  })
  return found
}

function nodeContainsJsxElement(node) {
  let found = false
  traverseAst(node, (candidate) => {
    if (candidate.type === 'JSXElement') found = true
  })
  return found
}

function nodeContainsAlias(node, aliases) {
  let found = false
  traverseAst(node, (candidate) => {
    if (candidate.type === 'Identifier' && aliases.has(candidate.name)) found = true
  })
  return found
}

function uiDescriptorsFromControlledNode(node, ast, sources, visited) {
  const descriptors = []
  traverseAst(node, (candidate) => {
    if (candidate.type !== 'JSXElement') return
    const opening = candidate.openingElement
    const name = opening.name.type === 'JSXIdentifier' ? opening.name.name : ''
    if (!name) return
    const names = jsxStaticAttributeValues(opening, ['aria-label', 'ariaLabel'], ast)
    const role = jsxStaticAttribute(opening, 'role', ast)
      || intrinsicJsxRole(name)
      || (/Dialog$/.test(name) && names.length > 0 ? 'dialog' : '')
    if (role) {
      const visibleNames = names.length > 0 ? names : collectJsxVisibleTextValues(candidate, ast)
      for (const visibleName of visibleNames) descriptors.push({ role, name: visibleName })
    }
    if (/^[A-Z]/.test(name)) {
      const visitKey = name
      if (visited.has(visitKey)) return
      const definition = findUniqueFunctionDefinition(sources, name)
      if (!definition) return
      const nextVisited = new Set(visited)
      nextVisited.add(visitKey)
      descriptors.push(...uiDescriptorsFromControlledNode(definition.node.body, definition.source.ast, sources, nextVisited))
    }
  })
  return descriptors
}

function collectJsxVisibleTextValues(element, ast) {
  const values = new Set()
  for (const child of element.children ?? []) {
    if (child.type === 'JSXText' && child.value.trim()) values.add(child.value.trim())
    if (child.type === 'JSXExpressionContainer') {
      for (const text of collectStaticTextValues(child.expression, ast)) values.add(text)
    }
    if (child.type === 'JSXElement') {
      for (const text of collectJsxVisibleTextValues(child, ast)) values.add(text)
    }
  }
  return [...values]
}

function jsxStaticAttribute(opening, name, ast) {
  return jsxStaticAttributeValues(opening, [name], ast)[0] ?? ''
}

function jsxStaticAttributeValues(opening, names, ast) {
  const values = new Set()
  for (const attribute of opening.attributes) {
    if (attribute.type !== 'JSXAttribute' || attribute.name.type !== 'JSXIdentifier' || !names.includes(attribute.name.name)) continue
    if (attribute.value?.type === 'StringLiteral') values.add(attribute.value.value)
    if (attribute.value?.type === 'JSXExpressionContainer') {
      for (const text of collectStaticTextValues(attribute.value.expression, ast)) values.add(text)
    }
  }
  return [...values]
}

function intrinsicJsxRole(name) {
  if (name === 'button') return 'button'
  if (name === 'dialog') return 'dialog'
  return ''
}

const functionDefinitionsBySources = new WeakMap()

function findUniqueFunctionDefinition(sources, name) {
  let definitionsByName = functionDefinitionsBySources.get(sources)
  if (!definitionsByName) {
    definitionsByName = new Map()
    for (const source of sources) {
      traverseAst(source.ast, (candidate) => {
        const candidateName = candidate.type === 'FunctionDeclaration' ? candidate.id?.name : ''
        if (!candidateName) return
        const definitions = definitionsByName.get(candidateName) ?? []
        definitions.push({ node: candidate, source })
        definitionsByName.set(candidateName, definitions)
      })
    }
    functionDefinitionsBySources.set(sources, definitionsByName)
  }
  const definitions = definitionsByName.get(name) ?? []
  return definitions.length === 1 ? definitions[0] : null
}

function collectStaticTextValues(node, ast, visited = new Set()) {
  const values = new Set()
  traverseAst(node, (candidate) => {
    if (candidate.type === 'StringLiteral' && candidate.value.trim()) values.add(candidate.value.trim())
    if (candidate.type === 'TemplateElement' && candidate.value.cooked?.trim()) values.add(candidate.value.cooked.trim())
    if (candidate.type === 'RegExpLiteral' && candidate.pattern.trim()) values.add(candidate.pattern.trim())
    if (
      candidate.type !== 'Identifier'
      && candidate.type !== 'MemberExpression'
      && candidate.type !== 'CallExpression'
    ) return
    const resolved = resolveStaticValueNode(ast, candidate, visited)
    if (!resolved || resolved === candidate) return
    const key = `${resolved.start ?? ''}:${resolved.end ?? ''}`
    if (visited.has(key)) return
    const nextVisited = new Set(visited)
    nextVisited.add(key)
    for (const text of collectStaticTextValues(resolved, ast, nextVisited)) values.add(text)
  })
  return values
}

function resolveStaticValueNode(ast, node) {
  if (node.type === 'Identifier') return findModuleVariableInitializer(ast, node.name)
  if (node.type === 'CallExpression' && node.callee.type === 'Identifier') {
    return findModuleFunctionDeclaration(ast, node.callee.name)?.body ?? null
  }
  if (
    node.type !== 'MemberExpression'
    || node.computed
    || node.object.type !== 'Identifier'
    || node.property.type !== 'Identifier'
  ) return null
  let object = findModuleVariableInitializer(ast, node.object.name)
  if (object?.type === 'CallExpression' && object.arguments.length === 1) object = object.arguments[0]
  if (object?.type !== 'ObjectExpression') return null
  const property = object.properties.find((candidate) => staticPropertyKeyName(candidate) === node.property.name)
  return property?.type === 'ObjectProperty' ? property.value : null
}

function findModuleFunctionDeclaration(ast, name) {
  for (const statement of ast.program.body) {
    const declaration = statement.type === 'ExportNamedDeclaration' ? statement.declaration : statement
    if (declaration?.type === 'FunctionDeclaration' && declaration.id?.name === name) return declaration
  }
  return null
}

function findModuleVariableInitializer(ast, name) {
  for (const statement of ast.program.body) {
    const declaration = statement.type === 'ExportNamedDeclaration' ? statement.declaration : statement
    if (declaration?.type !== 'VariableDeclaration') continue
    for (const item of declaration.declarations) {
      if (item.id.type === 'Identifier' && item.id.name === name) return item.init
    }
  }
  return null
}

function isDismissingStateValue(node) {
  if (node?.type === 'NullLiteral') return true
  if (node?.type === 'BooleanLiteral') return node.value === false
  if (node?.type === 'StringLiteral') return node.value === ''
  if (node?.type !== 'ObjectExpression') return false
  return node.properties.some((property) => (
    property.type === 'ObjectProperty' && isDismissingStateValue(property.value)
  ))
}

function hasPageIgnoredResultRegressionEvidence(ast, symbol, entry, consumerOutcomeProof) {
  if (!consumerOutcomeProof) return false
  const facadeName = entry.facade.split('.').at(-1)
  let proven = false
  traverseAst(ast, (node) => {
    if (
      proven
      || node.type !== 'CallExpression'
      || node.callee.type !== 'Identifier'
      || (node.callee.name !== 'it' && node.callee.name !== 'test')
      || node.arguments[0]?.type !== 'StringLiteral'
      || node.arguments[0].value !== symbol
      || hasNonTestRunnerBinding(ast, node.callee.name)
    ) return
    const callback = node.arguments[1]
    if (
      (callback?.type !== 'ArrowFunctionExpression' && callback?.type !== 'FunctionExpression')
      || callback.body.type !== 'BlockStatement'
    ) return
    const statements = callback.body.body
    let mockReceiver = ''
    const mockIndex = statements.findIndex((statement) => {
      mockReceiver = malformedFacadeMockReceiver(statement, facadeName)
      return Boolean(mockReceiver)
    })
    if (mockIndex < 0) return
    const triggerIndex = statements.findIndex((statement, index) => (
      index > mockIndex && statementContainsPageTrigger(statement)
    ))
    if (triggerIndex < 0) return
    const invocationIndex = statements.findIndex((statement, index) => (
      index > triggerIndex
      && statementContainsExactFacadeInvocationAssertion(statement, mockReceiver, facadeName)
    ))
    if (invocationIndex < 0) return
    proven = statements.some((statement, index) => (
      index > invocationIndex
      && statementContainsMatchedUiOutcomeAssertion(statement, consumerOutcomeProof)
    ))
  })
  return proven
}

function malformedFacadeMockReceiver(statement, facadeName) {
  let receiver = ''
  traverseAst(statement, (candidate) => {
    if (
      receiver
      || candidate.type !== 'CallExpression'
      || candidate.arguments.length !== 1
      || candidate.callee.type !== 'MemberExpression'
      || candidate.callee.computed
      || candidate.callee.property.type !== 'Identifier'
      || (candidate.callee.property.name !== 'mockResolvedValue'
        && candidate.callee.property.name !== 'mockResolvedValueOnce')
    ) return
    const mockedFacade = candidate.callee.object
    if (
      mockedFacade.type !== 'MemberExpression'
      || mockedFacade.computed
      || mockedFacade.property.type !== 'Identifier'
      || mockedFacade.property.name !== facadeName
    ) return
    if (mockedFacade.object.type === 'Identifier' && hasMalformedSentinel(candidate.arguments[0])) {
      receiver = mockedFacade.object.name
    }
  })
  return receiver
}

function hasMalformedSentinel(node) {
  if (node?.type === 'StringLiteral') return /malformed|unexpected|sentinel/i.test(node.value)
  if (node?.type === 'ArrayExpression') return node.elements.some(hasMalformedSentinel)
  if (node?.type !== 'ObjectExpression') return false
  return node.properties.some((property) => {
    if (property.type !== 'ObjectProperty') return false
    return /malformed|unexpected|sentinel/i.test(staticPropertyKeyName(property) ?? '')
      || hasMalformedSentinel(property.value)
  })
}

function statementContainsPageTrigger(statement) {
  let found = false
  traverseAst(statement, (candidate) => {
    if (
      candidate.type === 'CallExpression'
      && candidate.callee.type === 'MemberExpression'
      && !candidate.callee.computed
      && candidate.callee.object.type === 'Identifier'
      && (candidate.callee.object.name === 'fireEvent' || candidate.callee.object.name === 'userEvent')
    ) found = true
  })
  return found
}

function statementContainsExactFacadeInvocationAssertion(statement, receiver, facadeName) {
  let found = false
  traverseAst(statement, (candidate) => {
    if (
      found
      || candidate.type !== 'CallExpression'
      || candidate.callee.type !== 'MemberExpression'
      || candidate.callee.computed
      || candidate.callee.property.type !== 'Identifier'
      || !/^toHaveBeenCalled/.test(candidate.callee.property.name)
    ) return
    traverseAst(candidate.callee.object, (asserted) => {
      if (
        asserted.type === 'CallExpression'
        && asserted.callee.type === 'Identifier'
        && asserted.callee.name === 'expect'
        && asserted.arguments.length === 1
        && asserted.arguments[0].type === 'MemberExpression'
        && !asserted.arguments[0].computed
        && asserted.arguments[0].object.type === 'Identifier'
        && asserted.arguments[0].object.name === receiver
        && asserted.arguments[0].property.type === 'Identifier'
        && asserted.arguments[0].property.name === facadeName
      ) found = true
    })
  })
  return found
}

function statementContainsMatchedUiOutcomeAssertion(statement, proof) {
  let matched = false
  traverseAst(statement, (candidate) => {
    if (
      matched
      || candidate.type !== 'CallExpression'
      || candidate.callee.type !== 'MemberExpression'
      || candidate.callee.computed
      || candidate.callee.property.type !== 'Identifier'
    ) return
    const matcherName = candidate.callee.property.name
    let expectCall = null
    traverseAst(candidate.callee.object, (chainNode) => {
      if (
        chainNode.type === 'CallExpression'
        && chainNode.callee.type === 'Identifier'
        && chainNode.callee.name === 'expect'
        && chainNode.arguments.length === 1
      ) expectCall = chainNode
    })
    if (!expectCall) return
    let screenCall = null
    traverseAst(expectCall.arguments[0], (asserted) => {
      if (
        asserted.type === 'CallExpression'
        && asserted.callee.type === 'MemberExpression'
        && !asserted.callee.computed
        && asserted.callee.object.type === 'Identifier'
        && asserted.callee.object.name === 'screen'
        && asserted.callee.property.type === 'Identifier'
        && /^(?:find|get|query)(?:All)?By/.test(asserted.callee.property.name)
      ) screenCall = asserted
    })
    if (!screenCall) return
    const negative = memberChainContainsName(candidate.callee.object, 'not')
      || matcherName === 'toBeNull'
    const query = exactScreenQueryDescriptor(screenCall)
    if (negative && query && proof.controlledUi.some((control) => (
      control.role === query.role && control.name === query.name
    ))) matched = true
  })
  return matched
}

function exactScreenQueryDescriptor(screenCall) {
  if (
    screenCall.callee.type !== 'MemberExpression'
    || screenCall.callee.property.type !== 'Identifier'
    || !/ByRole$/.test(screenCall.callee.property.name)
    || screenCall.arguments[0]?.type !== 'StringLiteral'
    || screenCall.arguments[1]?.type !== 'ObjectExpression'
  ) return null
  const nameProperty = screenCall.arguments[1].properties.find((property) => (
    property.type === 'ObjectProperty' && staticPropertyKeyName(property) === 'name'
  ))
  if (nameProperty?.type !== 'ObjectProperty' || nameProperty.value.type !== 'StringLiteral') return null
  return { role: screenCall.arguments[0].value, name: nameProperty.value.value }
}

function nodeContainsNode(node, target) {
  let found = false
  traverseAst(node, (candidate) => {
    if (candidate === target) found = true
  })
  return found
}

function hasResultHandledRegressionEvidence(ast, testPath, symbol, consumerAliases, entry) {
  if (isExactTurnInterruptPolicy(entry) && hasRuntimeResultHandledRegressionEvidence(ast, testPath, symbol, entry)) return true
  const facadeName = entry.facade.split('.').at(-1)
  let mockedError = ''
  for (const statement of ast.program.body) {
    if (statement.type !== 'ExpressionStatement') continue
    const call = statement.expression
    if (
      call.type !== 'CallExpression' || call.callee.type !== 'MemberExpression' || call.callee.computed
      || call.callee.object.type !== 'Identifier' || call.callee.object.name !== 'vi'
      || call.callee.property.type !== 'Identifier' || call.callee.property.name !== 'mock'
      || call.arguments[0]?.type !== 'StringLiteral'
      || !moduleSpecifierResolvesTo(testPath, call.arguments[0].value, RPC_FACADE_PATH)
    ) continue
    const factory = call.arguments[1]
    if (factory?.type !== 'ArrowFunctionExpression' && factory?.type !== 'FunctionExpression') continue
    const exports = functionReturnedObject(factory)
    const facade = exports?.properties.find((property) => (
      property.type === 'ObjectProperty' && staticPropertyKeyName(property) === facadeName
    ))
    const response = facade?.type === 'ObjectProperty' ? findMockResolvedValueArgument(facade.value) : null
    if (response?.type !== 'ObjectExpression') continue
    const ok = response.properties.find((property) => property.type === 'ObjectProperty' && staticPropertyKeyName(property) === 'ok')
    const error = response.properties.find((property) => property.type === 'ObjectProperty' && staticPropertyKeyName(property) === 'error')
    if (ok?.value.type === 'BooleanLiteral' && ok.value.value === false && error?.value.type === 'StringLiteral') {
      mockedError = error.value.value
    }
  }
  if (!mockedError) return false
  const warningProducerAliases = new Set()
  const handlerLocator = entry.responsePolicy?.handler
  for (const statement of ast.program.body) {
    if (
      !handlerLocator
      || statement.type !== 'ImportDeclaration'
      || !moduleSpecifierResolvesTo(testPath, statement.source.value, handlerLocator.path)
    ) continue
    for (const specifier of statement.specifiers) {
      if (
        specifier.type === 'ImportSpecifier'
        && moduleExportName(specifier.imported) === handlerLocator.symbol
      ) warningProducerAliases.add(specifier.local.name)
    }
  }
  let proven = false
  traverseAst(ast, (node) => {
    if (
      proven || node.type !== 'CallExpression' || node.callee.type !== 'Identifier'
      || (node.callee.name !== 'it' && node.callee.name !== 'test')
      || node.arguments[0]?.type !== 'StringLiteral' || node.arguments[0].value !== symbol
      || hasNonTestRunnerBinding(ast, node.callee.name)
    ) return
    const callback = node.arguments[1]
    if (
      (callback?.type !== 'ArrowFunctionExpression' && callback?.type !== 'FunctionExpression')
      || callback.body.type !== 'BlockStatement'
    ) return
    let callsConsumer = false
    let consumerStatementIndex = -1
    let callsWarningTarget = false
    const warningSpies = new Set()
    walkAstWithAncestors(callback.body, (candidate, ancestors) => {
      if (ancestors.some((ancestor) => isFunctionNode(ancestor))) return
      if (
        candidate.type === 'CallExpression' && candidate.callee.type === 'Identifier'
        && consumerAliases.has(candidate.callee.name)
        && !bindingShadowsNameAt([callback, ...ancestors], candidate.callee.name)
      ) {
        const awaited = ancestors.at(-1)
        const statement = ancestors.at(-2)
        const statementParent = ancestors.at(-3)
        if (
          awaited?.type === 'AwaitExpression'
          && statement?.type === 'ExpressionStatement'
          && statementParent === callback.body
        ) {
          callsConsumer = true
          consumerStatementIndex = callback.body.body.indexOf(statement)
        }
      }
      if (
        candidate.type === 'CallExpression'
        && candidate.callee.type === 'Identifier'
        && warningProducerAliases.has(candidate.callee.name)
        && !bindingShadowsNameAt([callback, ...ancestors], candidate.callee.name)
      ) callsWarningTarget = true
      if (
        candidate.type === 'CallExpression'
        && candidate.callee.type === 'MemberExpression'
        && !candidate.callee.computed
        && candidate.callee.object.type === 'Identifier'
        && candidate.callee.object.name === 'console'
        && candidate.callee.property.type === 'Identifier'
        && candidate.callee.property.name === 'warn'
      ) callsWarningTarget = true
      if (candidate.type !== 'VariableDeclarator' || candidate.id.type !== 'Identifier') return
      let init = candidate.init
      while (init?.type === 'CallExpression' && init.callee.type === 'MemberExpression') {
        if (
          !init.callee.computed && init.callee.object.type === 'Identifier' && init.callee.object.name === 'vi'
          && init.callee.property.type === 'Identifier' && init.callee.property.name === 'spyOn'
          && init.arguments[0]?.type === 'Identifier' && init.arguments[0].name === 'console'
          && init.arguments[1]?.type === 'StringLiteral' && init.arguments[1].value === 'warn'
        ) warningSpies.add(candidate.id.name)
        init = init.callee.object
      }
    })
    if (!callsConsumer || consumerStatementIndex < 0 || callsWarningTarget || warningSpies.size === 0) return
    walkAstWithAncestors(callback.body, (candidate, ancestors) => {
      if (ancestors.some((ancestor) => isFunctionNode(ancestor))) return
      if (
        candidate.type === 'CallExpression' && candidate.callee.type === 'MemberExpression'
        && !candidate.callee.computed && candidate.callee.property.type === 'Identifier'
        && candidate.callee.property.name === 'toHaveBeenCalledWith'
        && candidate.arguments.length === 1 && candidate.arguments[0].type === 'StringLiteral'
        && candidate.arguments[0].value === mockedError
        && candidate.callee.object.type === 'CallExpression'
        && candidate.callee.object.callee.type === 'Identifier'
        && candidate.callee.object.callee.name === 'expect'
        && candidate.callee.object.arguments[0]?.type === 'Identifier'
        && warningSpies.has(candidate.callee.object.arguments[0].name)
      ) {
        const assertionStatement = ancestors.at(-1)
        if (
          assertionStatement?.type === 'ExpressionStatement'
          && ancestors.at(-2) === callback.body
          && callback.body.body.indexOf(assertionStatement) > consumerStatementIndex
        ) proven = true
      }
    })
  })
  return proven
}

function hasRuntimeResultHandledRegressionEvidence(ast, testPath, symbol, entry) {
  const handlerPath = entry.responsePolicy?.handler?.path
  let exactRuntimeAttachImported = false
  for (const statement of ast.program.body) {
    if (statement.type !== 'ImportDeclaration' || !moduleSpecifierResolvesTo(testPath, statement.source.value, handlerPath)) continue
    for (const specifier of statement.specifiers) {
      if (
        specifier.type === 'ImportSpecifier'
        && specifier.imported.type === 'Identifier' && specifier.imported.name === 'attachActiveThreadRpcRuntime'
        && specifier.local.name === 'attachActiveThreadRpcRuntime'
      ) exactRuntimeAttachImported = true
    }
  }
  if (!exactRuntimeAttachImported) return false
  let proven = false
  traverseAst(ast, (node) => {
    if (
      proven || node.type !== 'CallExpression' || node.callee.type !== 'Identifier'
      || (node.callee.name !== 'it' && node.callee.name !== 'test')
      || node.arguments[0]?.type !== 'StringLiteral' || node.arguments[0].value !== symbol
    ) return
    const callback = node.arguments[1]
    if (
      (callback?.type !== 'ArrowFunctionExpression' && callback?.type !== 'FunctionExpression')
      || !callback.async || callback.body.type !== 'BlockStatement'
    ) return
    const statements = callback.body.body
    const preludeSafe = statements.length >= 8
      && isExactFactoryDeclaration(statements[0], 'runtime', 'createRuntime')
      && isExactFactoryDeclaration(statements[1], 'deps', 'createDeps')
      && isExactFailureRpcDeclaration(statements[2])
      && isExactRuntimeAttachSetup(statements[3])
    const awaitedSafe = isExactInterruptFailureAwait(statements[4], responsePolicyRpcMethod(entry))
    const assertionTail = statements.slice(5)
    const tailSafe = assertionTail.length > 0 && assertionTail.every(isExpectAssertionStatement)
    const warningAssertion = assertionTail.some(isExactInterruptWarningAssertion)
    const noSuccessAssertion = assertionTail.some(isExactInterruptNoSuccessAssertion)
    const addWarningAssertion = assertionTail.some(isExactInterruptAddWarningAssertion)
    const rawNoticeLeakAssertion = assertionTail.some((statement) => isExactInterruptRawLeakAssertion(statement, 'notifyAction'))
    const rawWarningLeakAssertion = assertionTail.some((statement) => isExactInterruptRawLeakAssertion(statement, 'addWarning'))
    if (
      preludeSafe && awaitedSafe && tailSafe && warningAssertion && noSuccessAssertion
      && addWarningAssertion && rawNoticeLeakAssertion && rawWarningLeakAssertion
    ) proven = true
  })
  return proven
}

function isExactFactoryDeclaration(statement, bindingName, factoryName) {
  if (statement?.type !== 'VariableDeclaration' || statement.kind !== 'const' || statement.declarations.length !== 1) return false
  const declaration = statement.declarations[0]
  return declaration.id.type === 'Identifier' && declaration.id.name === bindingName
    && declaration.init?.type === 'CallExpression'
    && declaration.init.callee.type === 'Identifier' && declaration.init.callee.name === factoryName
    && declaration.init.arguments.length === 0
}

function isExactFailureRpcDeclaration(statement) {
  if (statement?.type !== 'VariableDeclaration' || statement.kind !== 'const' || statement.declarations.length !== 1) return false
  const declaration = statement.declarations[0]
  if (declaration.id.type !== 'Identifier' || declaration.id.name !== 'rpc') return false
  const resolvedCall = declaration.init
  if (
    resolvedCall?.type !== 'CallExpression' || resolvedCall.arguments.length !== 1
    || !isStaticMemberNamed(resolvedCall.callee, 'mockResolvedValue')
  ) return false
  const fnCall = resolvedCall.callee.object
  if (
    fnCall.type !== 'CallExpression' || fnCall.arguments.length !== 0
    || !isStaticMemberNamed(fnCall.callee, 'fn')
    || fnCall.callee.object.type !== 'Identifier' || fnCall.callee.object.name !== 'vi'
  ) return false
  const result = resolvedCall.arguments[0]
  if (result.type !== 'ObjectExpression' || result.properties.length !== 2) return false
  const ok = result.properties.find((property) => property.type === 'ObjectProperty' && staticPropertyKeyName(property) === 'ok')
  const error = result.properties.find((property) => property.type === 'ObjectProperty' && staticPropertyKeyName(property) === 'error')
  return ok?.value.type === 'BooleanLiteral' && ok.value.value === false
    && error?.value.type === 'StringLiteral' && error.value.value.trim().length > 0
}

function isExactRuntimeAttachSetup(statement) {
  if (statement.type !== 'ExpressionStatement' || statement.expression.type !== 'CallExpression') return false
  const call = statement.expression
  return call.callee.type === 'Identifier'
    && call.callee.name === 'attachActiveThreadRpcRuntime'
    && call.arguments.length === 2
    && call.arguments[0].type === 'Identifier' && call.arguments[0].name === 'runtime'
    && call.arguments[1].type === 'Identifier' && call.arguments[1].name === 'deps'
}

function isExpectAssertionStatement(statement) {
  return exactExpectMatcher(statement) !== null
}

function isExactInterruptFailureAwait(statement, rpcMethod) {
  if (statement?.type !== 'ExpressionStatement' || statement.expression.type !== 'AwaitExpression') return false
  const matcher = statement.expression.argument
  if (
    matcher.type !== 'CallExpression' || matcher.arguments.length !== 1
    || matcher.arguments[0].type !== 'BooleanLiteral' || matcher.arguments[0].value !== false
    || !isStaticMemberNamed(matcher.callee, 'toBe')
  ) return false
  const resolves = matcher.callee.object
  if (!isStaticMemberNamed(resolves, 'resolves')) return false
  const expectCall = resolves.object
  if (
    expectCall.type !== 'CallExpression' || expectCall.arguments.length !== 1
    || expectCall.callee.type !== 'Identifier' || expectCall.callee.name !== 'expect'
  ) return false
  const runtimeCall = expectCall.arguments[0]
  return runtimeCall.type === 'CallExpression'
    && isStaticMemberNamed(runtimeCall.callee, 'activeThreadRPC')
    && runtimeCall.callee.object.type === 'Identifier' && runtimeCall.callee.object.name === 'runtime'
    && runtimeCall.arguments.length === 2
    && runtimeCall.arguments[0].type === 'StringLiteral' && runtimeCall.arguments[0].value === rpcMethod
    && runtimeCall.arguments[1].type === 'Identifier' && runtimeCall.arguments[1].name === 'rpc'
}

function exactExpectMatcher(statement) {
  if (statement?.type !== 'ExpressionStatement' || statement.expression.type !== 'CallExpression') return null
  const call = statement.expression
  if (call.callee.type !== 'MemberExpression' || call.callee.computed || call.callee.property.type !== 'Identifier') return null
  let root = call.callee.object
  const modifiers = []
  while (root.type === 'MemberExpression') {
    if (root.computed || root.property.type !== 'Identifier') return null
    modifiers.unshift(root.property.name)
    root = root.object
  }
  if (
    root.type !== 'CallExpression' || root.arguments.length !== 1
    || root.callee.type !== 'Identifier' || root.callee.name !== 'expect'
  ) return null
  return { arguments: call.arguments, expectArgument: root.arguments[0], matcher: call.callee.property.name, modifiers }
}

function isExactInterruptWarningAssertion(statement) {
  const matcher = exactExpectMatcher(statement)
  return matcher?.matcher === 'toHaveBeenCalledWith' && matcher.modifiers.length === 0
    && isRuntimeMember(matcher.expectArgument, 'notifyAction')
    && hasExactStringArguments(matcher.arguments, ['中断当前执行失败，请重试。', 'warning'])
    && isExactStringObject(matcher.arguments[2], { threadId: 'thread-1' })
}

function isExactInterruptNoSuccessAssertion(statement) {
  const matcher = exactExpectMatcher(statement)
  return matcher?.matcher === 'toHaveBeenCalledWith' && matcher.modifiers.length === 1 && matcher.modifiers[0] === 'not'
    && isRuntimeMember(matcher.expectArgument, 'notifyAction')
    && hasExactStringArguments(matcher.arguments, ['已发送中断请求', 'success'])
    && isExactStringObject(matcher.arguments[2], { threadId: 'thread-1' })
}

function isExactInterruptAddWarningAssertion(statement) {
  const matcher = exactExpectMatcher(statement)
  return matcher?.matcher === 'toHaveBeenCalledWith' && matcher.modifiers.length === 0
    && isRuntimeMember(matcher.expectArgument, 'addWarning')
    && hasExactStringArguments(matcher.arguments, ['warn', 'thread.interrupt.failed'])
    && isExactStringObject(matcher.arguments[2], { threadId: 'thread-1', error: 'action failure; see Health diagnostic ID' })
}

function isExactInterruptRawLeakAssertion(statement, runtimeMember) {
  const matcher = exactExpectMatcher(statement)
  if (
    matcher?.matcher !== 'toContain' || matcher.modifiers.length !== 1 || matcher.modifiers[0] !== 'not'
    || matcher.arguments.length !== 1 || matcher.arguments[0].type !== 'StringLiteral'
    || matcher.arguments[0].value !== 'turn already completed'
  ) return false
  const stringify = matcher.expectArgument
  if (
    stringify?.type !== 'CallExpression' || stringify.arguments.length !== 1
    || !isStaticMemberNamed(stringify.callee, 'stringify')
    || stringify.callee.object.type !== 'Identifier' || stringify.callee.object.name !== 'JSON'
  ) return false
  const calls = stringify.arguments[0]
  return isStaticMemberNamed(calls, 'calls')
    && isStaticMemberNamed(calls.object, 'mock')
    && isRuntimeMember(calls.object.object, runtimeMember)
}

function isStaticMemberNamed(node, name) {
  return node?.type === 'MemberExpression' && !node.computed
    && node.property.type === 'Identifier' && node.property.name === name
}

function isRuntimeMember(node, name) {
  return isStaticMemberNamed(node, name)
    && node.object.type === 'Identifier' && node.object.name === 'runtime'
}

function hasExactStringArguments(args, values) {
  return args.length === values.length + 1
    && values.every((value, index) => args[index]?.type === 'StringLiteral' && args[index].value === value)
}

function isExactStringObject(node, expected) {
  if (node?.type !== 'ObjectExpression' || node.properties.length !== Object.keys(expected).length) return false
  return Object.entries(expected).every(([key, value]) => {
    const property = node.properties.find((candidate) => candidate.type === 'ObjectProperty' && staticPropertyKeyName(candidate) === key)
    return property?.value.type === 'StringLiteral' && property.value.value === value
  })
}

function hasMalformedFacadeMock(ast, testPath, entry) {
  const facadeName = entry.facade.split('.').at(-1)
  for (const statement of ast.program.body) {
    if (statement.type !== 'ExpressionStatement') continue
    const call = statement.expression
    if (
      call.type !== 'CallExpression'
      || call.callee.type !== 'MemberExpression'
      || call.callee.computed
      || call.callee.object.type !== 'Identifier'
      || call.callee.object.name !== 'vi'
      || call.callee.property.type !== 'Identifier'
      || call.callee.property.name !== 'mock'
      || call.arguments[0]?.type !== 'StringLiteral'
      || !moduleSpecifierResolvesTo(testPath, call.arguments[0].value, RPC_FACADE_PATH)
    ) continue
    const factory = call.arguments[1]
    if (factory?.type !== 'ArrowFunctionExpression' && factory?.type !== 'FunctionExpression') continue
    const mockedExports = functionReturnedObject(factory)
    if (!mockedExports) continue
    const facade = mockedExports.properties.find((property) => (
      property.type === 'ObjectProperty' && staticPropertyKeyName(property) === facadeName
    ))
    if (facade?.type !== 'ObjectProperty') continue
    const resolved = findMockResolvedValueArgument(facade.value)
    if (resolved && isMalformedResponseLiteral(resolved)) return true
  }
  return false
}

function isSpecificShapeFailureMatcher(node) {
  if (node?.type === 'Identifier') return node.name === 'TypeError'
  return node?.type === 'StringLiteral' && /invalid|malformed|shape/i.test(node.value)
}

function functionReturnedObject(fn) {
  if (fn.body.type === 'ObjectExpression') return fn.body
  if (fn.body.type !== 'BlockStatement') return null
  const returns = fn.body.body.filter((statement) => statement.type === 'ReturnStatement')
  return returns.length === 1 && returns[0].argument?.type === 'ObjectExpression' ? returns[0].argument : null
}

function findMockResolvedValueArgument(node) {
  let found = null
  traverseAst(node, (candidate) => {
    if (
      !found
      && candidate.type === 'CallExpression'
      && candidate.callee.type === 'MemberExpression'
      && !candidate.callee.computed
      && candidate.callee.property.type === 'Identifier'
      && candidate.callee.property.name === 'mockResolvedValue'
      && candidate.arguments.length === 1
    ) found = candidate.arguments[0]
  })
  return found
}

function isMalformedResponseLiteral(node) {
  return node.type === 'NullLiteral'
    || node.type === 'BooleanLiteral'
    || node.type === 'NumericLiteral'
    || node.type === 'StringLiteral'
    || node.type === 'ArrayExpression'
    || node.type === 'ObjectExpression'
}

function memberChainContainsName(node, name) {
  let current = node
  while (current?.type === 'MemberExpression' && !current.computed) {
    if (current.property.type === 'Identifier' && current.property.name === name) return true
    current = current.object
  }
  return false
}

function hasNonTestRunnerBinding(ast, name) {
  for (const statement of ast.program.body) {
    if (statement.type === 'ImportDeclaration') {
      if (
        statement.source.value !== 'vitest'
        && statement.specifiers.some((specifier) => specifier.local?.name === name)
      ) {
        return true
      }
      continue
    }
    if (
      declarationBindsName(statement, name)
      || (
        statement.type === 'ExportNamedDeclaration'
        && declarationBindsName(statement.declaration, name)
      )
    ) {
      return true
    }
  }
  return false
}

function moduleSpecifierResolvesTo(fromPath, specifier, targetPath) {
  return moduleSpecifierResolvedPath(fromPath, specifier) === targetPath
}

function moduleSpecifierResolvedPath(fromPath, specifier) {
  if (typeof specifier !== 'string' || !specifier.startsWith('.')) return false
  return normalize(join(dirname(fromPath), specifier)).replaceAll('\\', '/')
}

async function findFacadeCalls(auditContext, ast, symbolNode, filePath, entry, backendFacadeRpcKeys) {
  const bindings = collectFacadeCallBindings(ast, filePath, entry, backendFacadeRpcKeys)
  const directLocator = DIRECT_FACADE_RPC_LOCATORS.get(entry.key)
  const exactDirectConsumer = directLocator
    && filePath === directLocator.implementationPath
    && entry.responsePolicy?.consumer?.symbol === directLocator.facade
  const candidates = []
  walkAstWithAncestors(symbolNode, (node, ancestors) => {
    if (node.type === 'CallExpression') candidates.push({ node, ancestors })
  })
  const calls = []
  for (const call of candidates) {
    let provenance = null
    if (exactDirectConsumer && directFacadeRuntimeCallMatches(entry, filePath, call.node)) {
      provenance = directFacadeCallProvenance(call, filePath)
    } else if (!exactDirectConsumer && facadeCallMatchesBindings(call.node, bindings, call.ancestors)) {
      provenance = directFacadeCallProvenance(call, filePath)
    } else if (!exactDirectConsumer) {
      provenance = await resolveImportedWrapperProvenance(
        auditContext,
        ast,
        filePath,
        call.node,
        call.ancestors,
        entry,
        backendFacadeRpcKeys,
        new Set(),
      )
    }
    if (provenance) {
      const effectiveCall = await promoteTransparentPromiseWrapperCall(
        auditContext,
        ast,
        filePath,
        call,
      )
      calls.push({ ...effectiveCall, provenance })
    }
  }
  return calls
}

function directFacadeCallProvenance(call, filePath) {
  return {
    facadeCall: call.node,
    facadeAncestors: call.ancestors,
    layers: [{ path: filePath, node: call.node, ancestors: call.ancestors }],
  }
}

async function promoteTransparentPromiseWrapperCall(auditContext, ast, filePath, call) {
  const wrapperCall = call.ancestors.at(-1)
  if (wrapperCall?.type !== 'CallExpression') return call
  const argumentIndex = wrapperCall.arguments.indexOf(call.node)
  if (argumentIndex < 0) return call
  const wrapperTarget = resolveImportedCallTarget(
    ast,
    filePath,
    wrapperCall,
    call.ancestors.slice(0, -1),
  )
  if (!wrapperTarget) return call
  const wrapperAst = await readAuditAst(auditContext, wrapperTarget.path)
  const wrapperNode = findExportedSymbolPath(wrapperAst, wrapperTarget.symbol)
  if (!transparentPromiseWrapperAt(wrapperNode, argumentIndex)) return call
  return { node: wrapperCall, ancestors: call.ancestors.slice(0, -1) }
}

function transparentPromiseWrapperAt(node, argumentIndex) {
  const parameter = node?.params?.[argumentIndex]
  if (parameter?.type !== 'Identifier' || node.body?.type !== 'BlockStatement' || node.body.body.length !== 1) {
    return false
  }
  const statement = node.body.body[0]
  if (statement.type !== 'ReturnStatement' || statement.argument?.type !== 'CallExpression') return false
  let references = 0
  traverseAst(statement.argument, (candidate) => {
    if (candidate.type === 'Identifier' && candidate.name === parameter.name) references += 1
  })
  return references === 1 && statement.argument.arguments.some((argument) => (
    argument.type === 'Identifier' && argument.name === parameter.name
  ))
}

function directFacadeRuntimeCallMatches(entry, filePath, call) {
  const locator = DIRECT_FACADE_RPC_LOCATORS.get(entry.key)
  if (
    !locator
    || filePath !== locator.implementationPath
    || entry.responsePolicy?.consumer?.symbol !== locator.facade
  ) return false
  return call.arguments.some((argument) => (
    argument.type === 'StringLiteral' && argument.value === locator.method
  ))
}

async function resolveImportedWrapperProvenance(
  auditContext,
  ast,
  filePath,
  call,
  ancestors,
  entry,
  backendFacadeRpcKeys,
  visited,
) {
  const target = resolveImportedCallTarget(ast, filePath, call, ancestors)
  if (!target) return null
  const visitKey = `${target.path}#${target.symbol}`
  if (visited.has(visitKey)) return null
  const nextVisited = new Set(visited)
  nextVisited.add(visitKey)
  const targetAst = await readAuditAst(auditContext, target.path)
  const targetNode = findExportedSymbolPath(targetAst, target.symbol)
  if (!targetNode) return null
  const bindings = collectFacadeCallBindings(targetAst, target.path, entry, backendFacadeRpcKeys)
  const nestedCalls = []
  walkAstWithAncestors(targetNode, (node, nestedAncestors) => {
    if (node.type === 'CallExpression') nestedCalls.push({ node, ancestors: nestedAncestors })
  })
  const resolved = []
  for (const nestedCall of nestedCalls) {
    let nestedProvenance = null
    if (
      facadeCallMatchesBindings(nestedCall.node, bindings, nestedCall.ancestors)
      || directFacadeRuntimeCallMatches(entry, target.path, nestedCall.node)
    ) {
      nestedProvenance = directFacadeCallProvenance(nestedCall, target.path)
    } else {
      nestedProvenance = await resolveImportedWrapperProvenance(
        auditContext,
        targetAst,
        target.path,
        nestedCall.node,
        nestedCall.ancestors,
        entry,
        backendFacadeRpcKeys,
        nextVisited,
      )
    }
    if (nestedProvenance) resolved.push({ call: nestedCall, provenance: nestedProvenance })
  }
  if (resolved.length !== 1) return null
  const [match] = resolved
  if (!wrapperTransparentlyReturnsCall(targetNode, match.call.node, match.call.ancestors)) return null
  return {
    facadeCall: match.provenance.facadeCall,
    facadeAncestors: match.provenance.facadeAncestors,
    layers: [
      { path: target.path, symbol: target.symbol, node: targetNode, call: match.call.node },
      ...match.provenance.layers,
    ],
  }
}

function wrapperTransparentlyReturnsCall(wrapperNode, call, ancestors) {
  if (!wrapperNode || !isFunctionNode(wrapperNode)) return false
  if (wrapperNode.body.type !== 'BlockStatement') {
    return wrapperNode.body === call
      || (wrapperNode.body.type === 'AwaitExpression' && wrapperNode.body.argument === call)
  }
  const parent = ancestors.at(-1)
  const callValue = parent?.type === 'AwaitExpression' && parent.argument === call ? parent : call
  const valueParent = parent?.type === 'AwaitExpression' ? ancestors.at(-2) : parent
  if (valueParent?.type === 'ReturnStatement' && valueParent.argument === callValue) return true
  if (
    valueParent?.type !== 'VariableDeclarator'
    || valueParent.init !== callValue
    || valueParent.id.type !== 'Identifier'
  ) return false
  const declaration = ancestors.at(parent?.type === 'AwaitExpression' ? -3 : -2)
  if (declaration?.type !== 'VariableDeclaration' || declaration.declarations.length !== 1) return false
  const declarationIndex = wrapperNode.body.body.indexOf(declaration)
  if (declarationIndex < 0 || declarationIndex !== wrapperNode.body.body.length - 2) return false
  const returnStatement = wrapperNode.body.body.at(-1)
  if (
    returnStatement.type !== 'ReturnStatement'
    || returnStatement.argument?.type !== 'Identifier'
    || returnStatement.argument.name !== valueParent.id.name
  ) return false
  let references = 0
  for (const statement of wrapperNode.body.body.slice(declarationIndex)) {
    traverseAst(statement, (candidate) => {
      if (candidate.type === 'Identifier' && candidate.name === valueParent.id.name) references += 1
    })
  }
  return references === 2
}

function resolveImportedCallTarget(ast, filePath, call, ancestors) {
  if (call.callee.type !== 'Identifier') return null
  const localName = call.callee.name
  if (bindingShadowsNameAt(ancestors, localName)) return null
  for (const statement of ast.program.body) {
    if (statement.type === 'ImportDeclaration') {
      for (const specifier of statement.specifiers) {
        if (specifier.local.name !== localName) continue
        const symbol = specifier.type === 'ImportSpecifier'
          ? moduleExportName(specifier.imported)
          : specifier.type === 'ImportDefaultSpecifier' ? 'default' : ''
        const path = moduleSpecifierResolvedPath(filePath, statement.source.value)
        if (symbol && path) return { path, symbol }
      }
      continue
    }
    const declaration = statement.type === 'ExportNamedDeclaration' ? statement.declaration : statement
    if (declaration?.type !== 'VariableDeclaration') continue
    for (const item of declaration.declarations) {
      if (item.id.type !== 'ObjectPattern' || item.init?.type !== 'Identifier') continue
      const property = item.id.properties.find((candidate) => (
        candidate.type === 'ObjectProperty'
        && candidate.value.type === 'Identifier'
        && candidate.value.name === localName
      ))
      if (!property) continue
      const imported = findImportedBinding(ast, filePath, item.init.name)
      const member = staticPropertyKeyName(property)
      if (imported && member) return { path: imported.path, symbol: `${imported.symbol}.${member}` }
    }
  }
  return null
}

function findImportedBinding(ast, filePath, localName) {
  for (const statement of ast.program.body) {
    if (statement.type !== 'ImportDeclaration') continue
    for (const specifier of statement.specifiers) {
      if (specifier.local.name !== localName) continue
      const symbol = specifier.type === 'ImportSpecifier'
        ? moduleExportName(specifier.imported)
        : specifier.type === 'ImportDefaultSpecifier' ? 'default' : ''
      const path = moduleSpecifierResolvedPath(filePath, statement.source.value)
      if (symbol && path) return { path, symbol }
    }
  }
  return null
}

function findExportedSymbolPath(ast, symbolPath) {
  const [root, ...members] = symbolPath.split('.')
  let current = findProductionSymbol(ast, root)
  for (const member of members) {
    if (current?.type === 'CallExpression' && current.arguments.length === 1) current = current.arguments[0]
    if (current?.type !== 'ObjectExpression') return null
    const property = current.properties.find((candidate) => staticPropertyKeyName(candidate) === member)
    if (!property) return null
    current = property.type === 'ObjectMethod' ? property : property.value
  }
  return current
}

function collectFacadeCallBindings(
  ast,
  filePath,
  entry,
  backendFacadeRpcKeys,
  facadeExportsByPath = new Map([[RPC_FACADE_PATH, new Set([entry.facade.split('.')[0]])]]),
) {
  const facadeParts = entry.facade.split('.')
  const identifierAliases = new Set()
  const namespaceAliases = new Set()
  const namespaceMemberNames = new Set()
  const namespaceMemberPaths = new Map()
  const addNamespaceMemberPath = (localName, memberPath) => {
    if (!namespaceMemberPaths.has(localName)) namespaceMemberPaths.set(localName, new Set())
    namespaceMemberPaths.get(localName).add(memberPath)
  }
  if (facadeParts.length === 1) {
    const facade = facadeParts[0]
    if (backendFacadeRpcKeys.get(facade) !== entry.key) {
      return { identifierAliases, namespaceAliases, memberName: facade }
    }
    for (const statement of ast.program.body) {
      const importedNames = statement.type === 'ImportDeclaration'
        ? facadeExportsByPath.get(moduleSpecifierResolvedPath(filePath, statement.source.value))
        : null
      if (
        statement.type !== 'ImportDeclaration'
        || !importedNames
      ) {
        continue
      }
      for (const specifier of statement.specifiers) {
        const importedName = specifier.type === 'ImportSpecifier'
          ? moduleExportName(specifier.imported)
          : specifier.type === 'ImportDefaultSpecifier' ? 'default' : ''
        if (importedName && importedNames.has(importedName)) {
          identifierAliases.add(specifier.local.name)
        }
        if (importedName) {
          const namespacePrefix = `${importedName}.`
          for (const name of importedNames) {
            if (!name.startsWith(namespacePrefix)) continue
            namespaceAliases.add(specifier.local.name)
            const memberPath = name.slice(namespacePrefix.length)
            namespaceMemberNames.add(memberPath)
            addNamespaceMemberPath(specifier.local.name, memberPath)
          }
        }
        if (specifier.type === 'ImportNamespaceSpecifier') {
          namespaceAliases.add(specifier.local.name)
          for (const name of importedNames) {
            namespaceMemberNames.add(name)
            addNamespaceMemberPath(specifier.local.name, name)
          }
        }
      }
    }
    return {
      identifierAliases,
      namespaceAliases,
      memberName: facade,
      namespaceMemberNames,
      namespaceMemberPaths,
    }
  }
  if (facadeParts.length !== 2 || !SERVICE_FACADE_LOCATORS.has(entry.key)) {
    return { identifierAliases, namespaceAliases, memberName: '' }
  }
  const [serviceName, memberName] = facadeParts
  const servicePath = SERVICE_FACADE_LOCATORS.get(entry.key)
  for (const statement of ast.program.body) {
    if (
      statement.type !== 'ImportDeclaration'
      || !moduleSpecifierResolvesTo(filePath, statement.source.value, servicePath)
    ) {
      continue
    }
    for (const specifier of statement.specifiers) {
      if (specifier.type === 'ImportSpecifier' && moduleExportName(specifier.imported) === serviceName) {
        namespaceAliases.add(specifier.local.name)
      }
    }
  }
  return { identifierAliases, namespaceAliases, memberName }
}

function symbolBindsName(symbolNode, name) {
  if (!symbolNode) return false
  let binds = false
  traverseAst(symbolNode.body ?? symbolNode, (node) => {
    if (
      (node.type === 'VariableDeclarator' && bindingPatternContainsName(node.id, name))
      || ((node.type === 'FunctionDeclaration' || node.type === 'ClassDeclaration') && node.id?.name === name)
    ) {
      binds = true
    }
  })
  return binds
}

function facadeCallMatchesBindings(call, bindings, ancestors = []) {
  if (call.callee.type === 'Identifier') {
    return bindings.identifierAliases.has(call.callee.name) && !bindingShadowsNameAt(ancestors, call.callee.name)
  }
  return (
    call.callee.type === 'MemberExpression'
    && !call.callee.computed
    && call.callee.object.type === 'Identifier'
    && call.callee.property.type === 'Identifier'
    && (
      call.callee.property.name === bindings.memberName
      || bindings.namespaceMemberNames?.has(call.callee.property.name)
    )
    && bindings.namespaceAliases.has(call.callee.object.name)
    && !bindingShadowsNameAt(ancestors, call.callee.object.name)
  )
}

function bindingShadowsNameAt(ancestors, name) {
  for (let index = ancestors.length - 1; index >= 0; index -= 1) {
    const scope = ancestors[index]
    if (scope.type === 'CatchClause' && bindingPatternContainsName(scope.param, name)) return true
    if (isFunctionNode(scope) && scope.params.some((parameter) => bindingPatternContainsName(parameter, name))) return true
    if (scope.type === 'BlockStatement' && blockDirectlyBindsName(scope, name)) return true
  }
  return false
}

function isFunctionNode(node) {
  return node.type === 'FunctionDeclaration'
    || node.type === 'FunctionExpression'
    || node.type === 'ArrowFunctionExpression'
    || node.type === 'ObjectMethod'
    || node.type === 'ClassMethod'
}

function blockDirectlyBindsName(block, name) {
  return block.body.some((statement) => {
    const declaration = statement.type === 'ExportNamedDeclaration' ? statement.declaration : statement
    if (declaration?.type === 'VariableDeclaration') {
      return declaration.declarations.some((item) => bindingPatternContainsName(item.id, name))
    }
    return (declaration?.type === 'FunctionDeclaration' || declaration?.type === 'ClassDeclaration')
      && declaration.id?.name === name
  })
}

function walkAstWithAncestors(node, visit, ancestors = []) {
  if (!node || typeof node.type !== 'string') return
  visit(node, ancestors)
  const nextAncestors = [...ancestors, node]
  for (const value of Object.values(node)) {
    if (!value) continue
    if (Array.isArray(value)) {
      for (const child of value) walkAstWithAncestors(child, visit, nextAncestors)
    } else if (typeof value.type === 'string') {
      walkAstWithAncestors(value, visit, nextAncestors)
    }
  }
}

function isIgnoredCallResult(ancestors) {
  const parent = ancestors.at(-1)
  if (parent?.type === 'ExpressionStatement') return true
  return parent?.type === 'AwaitExpression' && ancestors.at(-2)?.type === 'ExpressionStatement'
}

function isExactTurnInterruptPolicy(entry) {
  const policy = entry.responsePolicy
  return entry.key === 'TURN_INTERRUPT'
    && entry.facade === 'interruptTurn'
    && policy?.kind === 'result-handled'
    && policy.consumer.path === TURN_INTERRUPT_RUNTIME_PATH
    && policy.consumer.symbol === 'attachActiveThreadRpcRuntime'
    && policy.handler.path === TURN_INTERRUPT_RUNTIME_PATH
    && policy.handler.symbol === 'notifyThreadActionFailure'
    && policy.regressionTest.path === TURN_INTERRUPT_REGRESSION_PATH
    && policy.regressionTest.symbol === 'reports interrupt ok:false as warning without showing success'
}

async function provesTurnInterruptInjection(auditContext, entry) {
  if (!isExactTurnInterruptPolicy(entry)) return false
  const ast = await readAuditAst(auditContext, TURN_INTERRUPT_INJECTION_PATH)
  const consumerSymbol = findModuleLevelSymbol(ast, 'createActiveThreadActions')
  if (!consumerSymbol?.body || consumerSymbol.body.type !== 'BlockStatement') return false
  const facadeAliases = new Set()
  for (const statement of ast.program.body) {
    if (
      statement.type !== 'ImportDeclaration'
      || !moduleSpecifierResolvesTo(TURN_INTERRUPT_INJECTION_PATH, statement.source.value, RPC_FACADE_PATH)
    ) continue
    for (const specifier of statement.specifiers) {
      if (
        specifier.type === 'ImportSpecifier'
        && moduleExportName(specifier.imported) === entry.facade
      ) facadeAliases.add(specifier.local.name)
    }
  }
  if (facadeAliases.size !== 1) return false
  const statements = consumerSymbol.body.body
  if (
    statements.length !== 1
    || statements[0].type !== 'ReturnStatement'
    || statements[0].argument?.type !== 'ObjectExpression'
  ) return false
  const properties = statements[0].argument.properties
  if (!properties.every((property) => property.type === 'ObjectProperty' && !property.computed)) return false
  const actions = properties.filter(
    (property) => staticPropertyKeyName(property) === 'interruptActiveThread',
  )
  if (actions.length !== 1) return false
  const arrow = actions[0].value
  const call = arrow?.type === 'ArrowFunctionExpression' ? arrow.body : null
  return call?.type === 'CallExpression'
    && call.callee.type === 'MemberExpression'
    && !call.callee.computed
    && call.callee.object.type === 'Identifier'
    && call.callee.object.name === 'runtime'
    && call.callee.property.type === 'Identifier'
    && call.callee.property.name === 'activeThreadRPC'
    && call.arguments.length === 2
    && call.arguments[0].type === 'StringLiteral'
    && call.arguments[0].value === 'thread.interrupt'
    && call.arguments[1].type === 'Identifier'
    && facadeAliases.has(call.arguments[1].name)
}

function runtimePassesAwaitedResultToHandler(ast, handlerSymbol, handlerName, consumerName) {
  const consumer = findModuleLevelSymbol(ast, consumerName)
  if (!consumer || consumer === handlerSymbol || consumer.body?.type !== 'BlockStatement') return false
  const bindings = new Map([
    ['activeThreadRPC', []],
    ['runActiveThreadRPC', []],
  ])
  for (const statement of consumer.body.body) {
    if (statement.type !== 'VariableDeclaration' || statement.kind !== 'const') continue
    for (const item of statement.declarations) {
      if (
        item.id.type === 'Identifier'
        && bindings.has(item.id.name)
        && item.init?.type === 'ArrowFunctionExpression'
        && item.init.async
      ) bindings.get(item.id.name).push(item.init)
    }
  }
  const [wrapper] = bindings.get('activeThreadRPC')
  const [helper] = bindings.get('runActiveThreadRPC')
  if (
    bindings.get('activeThreadRPC').length !== 1
    || bindings.get('runActiveThreadRPC').length !== 1
    || countRuntimeProofBindings(consumer.body, 'activeThreadRPC') !== 1
    || countRuntimeProofBindings(consumer.body, 'runActiveThreadRPC') !== 1
    || countRuntimeProofBindings(consumer.body, handlerName) !== 0
    || !hasRuntimeProofParameters(wrapper, false)
    || !hasRuntimeProofParameters(helper, true)
    || wrapper.body.type !== 'BlockStatement'
    || helper.body.type !== 'BlockStatement'
  ) return false

  let invalidComputedCall = false
  traverseAst(consumer.body, (node) => {
    if (
      node.type === 'CallExpression'
      && (node.callee.type === 'MemberExpression' || node.callee.type === 'OptionalMemberExpression')
      && node.callee.computed
    ) invalidComputedCall = true
  })
  if (invalidComputedCall) return false

  const wrapperStatements = wrapper.body.body
  if (wrapperStatements.length !== 4) return false
  const outcomeDeclaration = wrapperStatements[0]
  const outcome = outcomeDeclaration?.type === 'VariableDeclaration'
    && outcomeDeclaration.kind === 'const'
    && outcomeDeclaration.declarations.length === 1
    ? outcomeDeclaration.declarations[0]
    : null
  const helperCall = outcome?.init?.type === 'AwaitExpression' ? outcome.init.argument : null
  const helperCalls = []
  let wrapperNestedFunction = false
  walkAstWithAncestors(wrapper.body, (node, ancestors) => {
    if (ancestors.some((ancestor) => isFunctionNode(ancestor))) wrapperNestedFunction = true
    if (
      node.type === 'CallExpression'
      && node.callee.type === 'Identifier'
      && node.callee.name === 'runActiveThreadRPC'
    ) helperCalls.push(node)
  })
  if (
    wrapperNestedFunction
    || outcome?.id.type !== 'Identifier' || outcome.id.name !== 'outcome'
    || helperCall?.type !== 'CallExpression'
    || helperCall.callee.type !== 'Identifier' || helperCall.callee.name !== 'runActiveThreadRPC'
    || helperCall.arguments.length !== 2
    || helperCall.arguments[0].type !== 'Identifier' || helperCall.arguments[0].name !== 'action'
    || helperCall.arguments[1].type !== 'Identifier' || helperCall.arguments[1].name !== 'rpc'
    || helperCalls.length !== 1
    || countRuntimeProofBindings(wrapper, 'action') !== 1
    || countRuntimeProofBindings(wrapper, 'rpc') !== 1
    || countRuntimeProofBindings(wrapper, 'outcome') !== 1
    || !isExactOutcomeFailureGate(wrapperStatements[1])
    || !isExactRuntimeSuccessStatement(wrapperStatements[2], wrapper)
    || wrapperStatements[3].type !== 'ReturnStatement'
    || wrapperStatements[3].argument?.type !== 'BooleanLiteral'
    || wrapperStatements[3].argument.value !== true
  ) return false

  const rpcCalls = []
  const handlerCalls = []
  let helperNestedFunction = false
  let resultDeclaration = null
  let resultBlock = null
  const helperStatements = helper.body.body
  if (
    helperStatements.length !== 6
    || !isExactRuntimeCurrentStateDeclaration(helperStatements[0])
    || !isExactRuntimeRequiresActiveTurnDeclaration(helperStatements[1])
    || !isExactRuntimeActiveTurnTargetDeclaration(helperStatements[2])
    || !isExactRuntimeThreadIdDeclaration(helperStatements[3])
    || !isExactRuntimeNoThreadGuard(helperStatements[4])
    || helperStatements[5].type !== 'TryStatement'
  ) return false
  const directTryStatements = helper.body.body.filter((statement) => statement.type === 'TryStatement')
  if (
    directTryStatements.length !== 1
    || directTryStatements[0] !== helperStatements[5]
    || directTryStatements[0].finalizer !== null
  ) return false
  const [resultTry] = directTryStatements
  walkAstWithAncestors(helper.body, (node, ancestors) => {
    if (ancestors.some((ancestor) => isFunctionNode(ancestor))) helperNestedFunction = true
    if (node.type === 'CallExpression' && node.callee.type === 'Identifier') {
      if (node.callee.name === 'rpc') rpcCalls.push(node)
      if (node.callee.name === handlerName) handlerCalls.push(node)
    }
    if (node.type !== 'VariableDeclarator' || node.id.type !== 'Identifier' || node.id.name !== 'result') return
    const declaration = ancestors.at(-1)
    const block = ancestors.at(-2)
    const rpcCall = node.init?.type === 'AwaitExpression' ? node.init.argument : null
    if (
      declaration?.type === 'VariableDeclaration'
      && declaration.kind === 'const'
      && declaration.declarations.length === 1
      && block?.type === 'BlockStatement'
      && block === resultTry.block
      && rpcCall?.type === 'CallExpression'
      && rpcCall.callee.type === 'Identifier' && rpcCall.callee.name === 'rpc'
      && rpcCall.arguments.length === 1
    ) {
      resultDeclaration = declaration
      resultBlock = block
    }
  })
  if (
    helperNestedFunction
    || rpcCalls.length !== 1
    || handlerCalls.length !== 1
    || countRuntimeProofBindings(helper, 'action') !== 1
    || countRuntimeProofBindings(helper, 'rpc') !== 1
    || countRuntimeProofBindings(helper, 'result') !== 1
    || !resultDeclaration
    || !resultBlock
  ) return false
  const resultIndex = resultBlock.body.indexOf(resultDeclaration)
  const failureGate = resultBlock.body[resultIndex + 1]
  const successReturn = resultBlock.body[resultIndex + 2]
  if (
    resultIndex !== 3
    || !isExactRuntimeCwdDeclaration(resultBlock.body[0])
    || !isExactRuntimePayloadDeclaration(resultBlock.body[1])
    || !isExactRuntimePayloadFailureGuard(resultBlock.body[2])
    || !isExactHandlerFailureGate(failureGate, handlerName)
    || !isExactRuntimeOutcomeReturn(successReturn, true)
    || successReturn !== resultBlock.body.at(-1)
  ) return false

  const trueReturns = []
  const handledFalseReturns = []
  traverseAst(helper.body, (node) => {
    if (node.type !== 'ReturnStatement') return
    if (isExactRuntimeOutcomeReturn(node, true)) trueReturns.push(node)
    if (isExactRuntimeOutcomeReturn(node, false)) handledFalseReturns.push(node)
  })
  if (
    trueReturns.length !== 1
    || trueReturns[0] !== successReturn
    || handledFalseReturns.length !== 1
  ) return false

  let exposures = 0
  for (const statement of consumer.body.body) {
    const call = statement.type === 'ExpressionStatement' ? statement.expression : null
    if (
      call?.type !== 'CallExpression'
      || call.callee.type !== 'MemberExpression' || call.callee.computed
      || call.callee.object.type !== 'Identifier' || call.callee.object.name !== 'Object'
      || call.callee.property.type !== 'Identifier' || call.callee.property.name !== 'assign'
      || call.arguments[0]?.type !== 'Identifier' || call.arguments[0].name !== 'runtime'
      || call.arguments[1]?.type !== 'ObjectExpression'
    ) continue
    for (const property of call.arguments[1].properties) {
      if (
        property.type === 'ObjectProperty'
        && !property.computed
        && staticPropertyKeyName(property) === 'activeThreadRPC'
        && property.value.type === 'Identifier'
        && property.value.name === 'activeThreadRPC'
      ) exposures += 1
    }
  }
  return exposures === 1
}

function countRuntimeProofBindings(node, name) {
  let count = 0
  traverseAst(node, (candidate) => {
    if (candidate.type === 'VariableDeclarator' && bindingPatternContainsName(candidate.id, name)) count += 1
    if (
      (candidate.type === 'FunctionDeclaration' || candidate.type === 'FunctionExpression')
      && candidate.id?.name === name
    ) count += 1
    if (
      isFunctionNode(candidate)
      && candidate.params.some((parameter) => bindingPatternContainsName(parameter, name))
    ) count += 1
    if (candidate.type === 'CatchClause' && bindingPatternContainsName(candidate.param, name)) count += 1
  })
  return count
}

function hasRuntimeProofParameters(fn, allowOptions) {
  if (
    fn?.params[0]?.type !== 'Identifier' || fn.params[0].name !== 'action'
    || fn.params[1]?.type !== 'Identifier' || fn.params[1].name !== 'rpc'
  ) return false
  if (fn.params.length === 2) return true
  const options = fn.params[2]
  return allowOptions
    && fn.params.length === 3
    && options.type === 'AssignmentPattern'
    && options.left.type === 'Identifier' && options.left.name === 'options'
    && options.right.type === 'ObjectExpression' && options.right.properties.length === 0
}

function isExactOutcomeFailureGate(statement) {
  if (statement?.type !== 'IfStatement' || statement.alternate) return false
  const returned = statement.consequent.type === 'ReturnStatement'
    ? statement.consequent
    : statement.consequent.type === 'BlockStatement' && statement.consequent.body.length === 1
      ? statement.consequent.body[0]
      : null
  return statement.test.type === 'UnaryExpression'
    && statement.test.operator === '!'
    && statement.test.argument.type === 'MemberExpression'
    && !statement.test.argument.computed
    && statement.test.argument.object.type === 'Identifier'
    && statement.test.argument.object.name === 'outcome'
    && statement.test.argument.property.type === 'Identifier'
    && statement.test.argument.property.name === 'ok'
    && returned?.type === 'ReturnStatement'
    && returned.argument?.type === 'BooleanLiteral'
    && returned.argument.value === false
}

function isExactRuntimeCwdDeclaration(statement) {
  if (statement?.type !== 'VariableDeclaration' || statement.kind !== 'const' || statement.declarations.length !== 1) return false
  const declaration = statement.declarations[0]
  const call = declaration.init
  return declaration.id.type === 'Identifier' && declaration.id.name === 'cwd'
    && call?.type === 'CallExpression'
    && call.callee.type === 'Identifier' && call.callee.name === 'requireCwd'
    && call.arguments.length === 1
    && call.arguments[0].type === 'Identifier' && call.arguments[0].name === 'action'
}

function isExactRuntimeCurrentStateDeclaration(statement) {
  if (statement?.type !== 'VariableDeclaration' || statement.kind !== 'const' || statement.declarations.length !== 1) return false
  const declaration = statement.declarations[0]
  const call = declaration.init
  return declaration.id.type === 'Identifier' && declaration.id.name === 'currentState'
    && call?.type === 'CallExpression'
    && call.callee.type === 'Identifier' && call.callee.name === 'get'
    && call.arguments.length === 0
}

function isExactRuntimeRequiresActiveTurnDeclaration(statement) {
  if (statement?.type !== 'VariableDeclaration' || statement.kind !== 'const' || statement.declarations.length !== 1) return false
  const declaration = statement.declarations[0]
  const call = declaration.init
  return declaration.id.type === 'Identifier' && declaration.id.name === 'requiresActiveTurn'
    && call?.type === 'CallExpression'
    && call.callee.type === 'Identifier' && call.callee.name === 'threadActionRequiresActiveTurn'
    && call.arguments.length === 1
    && call.arguments[0].type === 'Identifier' && call.arguments[0].name === 'action'
}

function isExactRuntimeActiveTurnTargetDeclaration(statement) {
  if (statement?.type !== 'VariableDeclaration' || statement.kind !== 'const' || statement.declarations.length !== 1) return false
  const declaration = statement.declarations[0]
  const conditional = declaration.init
  const targetCall = conditional?.type === 'ConditionalExpression' ? conditional.consequent : null
  return declaration.id.type === 'Identifier' && declaration.id.name === 'activeTurnTarget'
    && conditional?.type === 'ConditionalExpression'
    && conditional.test.type === 'Identifier' && conditional.test.name === 'requiresActiveTurn'
    && targetCall.type === 'CallExpression'
    && targetCall.callee.type === 'Identifier' && targetCall.callee.name === 'activeThreadInterruptTarget'
    && targetCall.arguments.length === 1
    && targetCall.arguments[0].type === 'Identifier' && targetCall.arguments[0].name === 'currentState'
    && conditional.alternate.type === 'NullLiteral'
}

function isExactRuntimeThreadIdDeclaration(statement) {
  if (statement?.type !== 'VariableDeclaration' || statement.kind !== 'const' || statement.declarations.length !== 1) return false
  const declaration = statement.declarations[0]
  const outer = declaration.init
  const inner = outer?.type === 'LogicalExpression' ? outer.left : null
  const optionsThreadId = inner?.type === 'LogicalExpression' ? inner.left : null
  const activeThreadId = inner?.type === 'LogicalExpression' ? inner.right : null
  const fallbackCall = outer?.type === 'LogicalExpression' ? outer.right : null
  const activeStateId = fallbackCall?.type === 'CallExpression' ? fallbackCall.arguments[1] : null
  return declaration.id.type === 'Identifier' && declaration.id.name === 'threadId'
    && outer?.type === 'LogicalExpression' && outer.operator === '||'
    && inner?.type === 'LogicalExpression' && inner.operator === '||'
    && optionsThreadId?.type === 'MemberExpression' && !optionsThreadId.computed
    && optionsThreadId.object.type === 'Identifier' && optionsThreadId.object.name === 'options'
    && optionsThreadId.property.type === 'Identifier' && optionsThreadId.property.name === 'threadId'
    && activeThreadId?.type === 'OptionalMemberExpression' && !activeThreadId.computed && activeThreadId.optional
    && activeThreadId.object.type === 'Identifier' && activeThreadId.object.name === 'activeTurnTarget'
    && activeThreadId.property.type === 'Identifier' && activeThreadId.property.name === 'threadId'
    && fallbackCall?.type === 'CallExpression'
    && fallbackCall.callee.type === 'Identifier' && fallbackCall.callee.name === 'backendThreadIdForState'
    && fallbackCall.arguments.length === 2
    && fallbackCall.arguments[0].type === 'Identifier' && fallbackCall.arguments[0].name === 'currentState'
    && activeStateId?.type === 'MemberExpression' && !activeStateId.computed
    && activeStateId.object.type === 'Identifier' && activeStateId.object.name === 'currentState'
    && activeStateId.property.type === 'Identifier' && activeStateId.property.name === 'activeThreadId'
}

function isExactRuntimeNoThreadGuard(statement) {
  if (
    statement?.type !== 'IfStatement'
    || statement.alternate
    || statement.test.type !== 'UnaryExpression'
    || statement.test.operator !== '!'
    || statement.test.argument.type !== 'Identifier'
    || statement.test.argument.name !== 'threadId'
    || statement.consequent.type !== 'BlockStatement'
    || statement.consequent.body.length !== 2
  ) return false
  const notice = statement.consequent.body[0]
  const noticeCall = notice.type === 'ExpressionStatement' ? notice.expression : null
  if (
    noticeCall?.type !== 'CallExpression'
    || noticeCall.callee.type !== 'Identifier' || noticeCall.callee.name !== 'notifyAction'
    || noticeCall.arguments.length !== 2
    || noticeCall.arguments[0].type !== 'StringLiteral'
    || noticeCall.arguments[0].value !== '当前没有可操作的后端线程'
    || noticeCall.arguments[1].type !== 'StringLiteral'
    || noticeCall.arguments[1].value !== 'warning'
  ) return false
  const returned = statement.consequent.body[1]
  const node = returned.type === 'ReturnStatement' ? returned.argument : null
  if (node?.type !== 'ObjectExpression' || node.properties.length !== 3) return false
  const okProperty = node.properties.find((property) => (
    property.type === 'ObjectProperty' && !property.computed && staticPropertyKeyName(property) === 'ok'
  ))
  const threadProperty = node.properties.find((property) => (
    property.type === 'ObjectProperty' && !property.computed && staticPropertyKeyName(property) === 'threadId'
  ))
  const resultProperty = node.properties.find((property) => (
    property.type === 'ObjectProperty' && !property.computed && staticPropertyKeyName(property) === 'result'
  ))
  return okProperty?.value.type === 'BooleanLiteral' && okProperty.value.value === false
    && threadProperty?.value.type === 'StringLiteral' && threadProperty.value.value === ''
    && resultProperty?.value.type === 'NullLiteral'
}

function isExactRuntimePayloadDeclaration(statement) {
  if (statement?.type !== 'VariableDeclaration' || statement.kind !== 'const' || statement.declarations.length !== 1) return false
  const declaration = statement.declarations[0]
  const call = declaration.init
  const names = [
    'action',
    'activeThreadInterruptTarget',
    'activeTurnTarget',
    'cleanObject',
    'createRequestId',
    'currentState',
    'cwd',
    'notifyAction',
    'threadId',
  ]
  if (
    declaration.id.type !== 'Identifier' || declaration.id.name !== 'payload'
    || call?.type !== 'CallExpression'
    || call.callee.type !== 'Identifier' || call.callee.name !== 'threadActionPayload'
    || call.arguments.length !== 1
    || call.arguments[0].type !== 'ObjectExpression'
    || call.arguments[0].properties.length !== names.length
  ) return false
  return names.every((name) => {
    const property = call.arguments[0].properties.find((candidate) => (
      candidate.type === 'ObjectProperty'
      && !candidate.computed
      && staticPropertyKeyName(candidate) === name
    ))
    return property?.value.type === 'Identifier' && property.value.name === name
  })
}

function isExactRuntimePayloadFailureGuard(statement) {
  if (
    statement?.type !== 'IfStatement'
    || statement.alternate
    || statement.test.type !== 'UnaryExpression'
    || statement.test.operator !== '!'
    || statement.test.argument.type !== 'Identifier'
    || statement.test.argument.name !== 'payload'
    || statement.consequent.type !== 'ReturnStatement'
  ) return false
  const node = statement.consequent.argument
  if (node?.type !== 'ObjectExpression' || node.properties.length !== 3) return false
  const okProperty = node.properties.find((property) => (
    property.type === 'ObjectProperty' && !property.computed && staticPropertyKeyName(property) === 'ok'
  ))
  const threadProperty = node.properties.find((property) => (
    property.type === 'ObjectProperty' && !property.computed && staticPropertyKeyName(property) === 'threadId'
  ))
  const resultProperty = node.properties.find((property) => (
    property.type === 'ObjectProperty' && !property.computed && staticPropertyKeyName(property) === 'result'
  ))
  return okProperty?.value.type === 'BooleanLiteral' && okProperty.value.value === false
    && threadProperty?.value.type === 'Identifier' && threadProperty.value.name === 'threadId'
    && resultProperty?.value.type === 'NullLiteral'
}

function isExactRuntimeSuccessStatement(statement, wrapper) {
  if (countRuntimeProofBindings(wrapper.body, 'notifyAction') !== 0) return false
  const call = statement?.type === 'ExpressionStatement' ? statement.expression : null
  return call?.type === 'CallExpression'
    && call.callee.type === 'Identifier' && call.callee.name === 'notifyAction'
    && call.arguments.length >= 2
    && call.arguments[1].type === 'StringLiteral' && call.arguments[1].value === 'success'
}

function isExactHandlerFailureGate(statement, handlerName) {
  if (
    statement?.type !== 'IfStatement'
    || statement.alternate
    || statement.test.type !== 'CallExpression'
    || statement.test.callee.type !== 'Identifier'
    || statement.test.callee.name !== handlerName
    || statement.test.arguments.length !== 1
    || !isExactRuntimeHandlerArgument(statement.test.arguments[0])
  ) return false
  const returned = statement.consequent.type === 'ReturnStatement'
    ? statement.consequent
    : statement.consequent.type === 'BlockStatement' && statement.consequent.body.length === 1
      ? statement.consequent.body[0]
      : null
  return isExactRuntimeOutcomeReturn(returned, false)
}

function isExactRuntimeHandlerArgument(node) {
  const names = ['action', 'addWarning', 'notifyAction', 'result', 'threadId']
  if (node?.type !== 'ObjectExpression' || node.properties.length !== names.length) return false
  return names.every((name) => {
    const property = node.properties.find((candidate) => (
      candidate.type === 'ObjectProperty'
      && !candidate.computed
      && staticPropertyKeyName(candidate) === name
    ))
    return property?.value.type === 'Identifier' && property.value.name === name
  })
}

function isExactRuntimeOutcomeReturn(statement, ok) {
  const node = statement?.type === 'ReturnStatement' ? statement.argument : null
  if (node?.type !== 'ObjectExpression' || node.properties.length !== 3) return false
  const okProperty = node.properties.find((property) => (
    property.type === 'ObjectProperty' && !property.computed && staticPropertyKeyName(property) === 'ok'
  ))
  const threadProperty = node.properties.find((property) => (
    property.type === 'ObjectProperty' && !property.computed && staticPropertyKeyName(property) === 'threadId'
  ))
  const resultProperty = node.properties.find((property) => (
    property.type === 'ObjectProperty' && !property.computed && staticPropertyKeyName(property) === 'result'
  ))
  return okProperty?.value.type === 'BooleanLiteral' && okProperty.value.value === ok
    && threadProperty?.value.type === 'Identifier' && threadProperty.value.name === 'threadId'
    && resultProperty?.value.type === 'Identifier' && resultProperty.value.name === 'result'
}

function consumerPassesFacadeResultToHandler(ast, consumerSymbol, facadeCall, consumerPath, handlerLocator) {
  if (!facadeCall) return false
  if (consumerSymbol.body?.type !== 'BlockStatement') return false
  const body = consumerSymbol.body
  let resultBinding = null
  let resultStatementIndex = -1
  walkAstWithAncestors(body, (node, ancestors) => {
    if (node !== facadeCall) return
    const awaited = ancestors.at(-1)
    const declarator = ancestors.at(-2)
    const declaration = ancestors.at(-3)
    const statementParent = ancestors.at(-4)
    if (
      awaited?.type === 'AwaitExpression'
      && declarator?.type === 'VariableDeclarator'
      && declarator.id.type === 'Identifier'
      && declaration?.type === 'VariableDeclaration'
      && declaration.kind === 'const'
      && statementParent === body
    ) {
      resultBinding = declarator
      resultStatementIndex = body.body.indexOf(declaration)
    }
  })
  if (!resultBinding || resultStatementIndex < 0) return false
  const handlerAliases = new Set()
  for (const statement of ast.program.body) {
    if (
      statement.type !== 'ImportDeclaration'
      || !moduleSpecifierResolvesTo(consumerPath, statement.source.value, handlerLocator.path)
    ) continue
    for (const specifier of statement.specifiers) {
      if (
        specifier.type === 'ImportSpecifier'
        && moduleExportName(specifier.imported) === handlerLocator.symbol
      ) handlerAliases.add(specifier.local.name)
    }
  }
  if (handlerAliases.size !== 1) return false
  let matched = false
  walkAstWithAncestors(body, (node, ancestors) => {
    if (
      node.type !== 'CallExpression'
      || node.start <= facadeCall.end
      || node.callee.type !== 'Identifier'
      || !handlerAliases.has(node.callee.name)
      || bindingShadowsNameAt([consumerSymbol, ...ancestors], node.callee.name)
      || node.arguments.length !== 1
      || node.arguments[0].type !== 'Identifier'
      || node.arguments[0].name !== resultBinding.id.name
    ) return
    const parent = ancestors.at(-1)
    const statementParent = ancestors.at(-2)
    const handlerStatementIndex = parent?.type === 'ReturnStatement' && statementParent === body
      ? body.body.indexOf(parent)
      : -1
    if (handlerStatementIndex > resultStatementIndex) {
      matched = true
    }
  })
  return matched
}

function handlerDirectlyInspectsEnvelope(handlerSymbol, rpcMethod = '', ast = null) {
  const parameter = handlerSymbol.params?.length === 1 && handlerSymbol.params[0].type === 'Identifier'
    ? handlerSymbol.params[0].name
    : ''
  const statements = handlerSymbol.body?.type === 'BlockStatement' ? handlerSymbol.body.body : []
  if (!parameter) return false
  const destructured = new Set()
  for (const statement of statements) {
    if (statement.type !== 'VariableDeclaration') continue
    for (const item of statement.declarations) {
      if (item.init?.type !== 'Identifier' || item.init.name !== parameter || item.id.type !== 'ObjectPattern') continue
      for (const property of item.id.properties) {
        if (property.type === 'ObjectProperty' && property.value.type === 'Identifier') destructured.add(property.value.name)
      }
    }
  }
  if (destructured.has('action') && destructured.has('result')) {
    return statements.some((statement) => {
      if (statement.type !== 'IfStatement' || isAlwaysFalseExpression(statement.test)) return false
      if (!isExactInterruptFailurePredicate(statement.test, rpcMethod)) return false
      let validatesRawEnvelope = false
      let notifyWarning = false
      let addWarning = false
      let returnsTrue = false
      walkAstWithAncestors(statement.consequent, (node, ancestors) => {
        if (ancestors.some((ancestor) => isFunctionNode(ancestor))) return
        if (
          node.type === 'CallExpression'
          && node.callee.type === 'Identifier'
          && node.arguments.length === 1
          && node.arguments[0].type === 'Identifier' && node.arguments[0].name === 'result'
          && moduleHelperReturnsResultError(ast, node.callee.name)
        ) validatesRawEnvelope = true
        if (node.type === 'ReturnStatement' && node.argument?.type === 'BooleanLiteral' && node.argument.value) returnsTrue = true
        if (node.type === 'CallExpression' && node.callee.type === 'Identifier' && node.callee.name === 'notifyAction') {
          const message = node.arguments[0]
          const severity = node.arguments[1]
          if (
            message?.type === 'StringLiteral' && message.value === '中断当前执行失败，请重试。'
            && severity?.type === 'StringLiteral' && severity.value === 'warning'
          ) notifyWarning = true
        }
        if (node.type === 'CallExpression' && node.callee.type === 'Identifier' && node.callee.name === 'addWarning') {
          const severity = node.arguments[0]
          const fields = node.arguments[2]
          const error = fields?.type === 'ObjectExpression'
            ? fields.properties.find((property) => property.type === 'ObjectProperty' && staticPropertyKeyName(property) === 'error')
            : null
          if (
            severity?.type === 'StringLiteral' && severity.value === 'warn'
            && error?.value.type === 'StringLiteral'
            && error.value.value === 'action failure; see Health diagnostic ID'
          ) addWarning = true
        }
      })
      return validatesRawEnvelope && notifyWarning && addWarning && returnsTrue
    })
  }
  return statements.some((statement) => {
    if (statement.type !== 'IfStatement' || isAlwaysFalseExpression(statement.test)) return false
    let outcomeInspected = false
    traverseAst(statement.test, (node) => {
      if (
        node.type === 'MemberExpression'
        && !node.computed
        && node.object.type === 'Identifier'
        && node.object.name === parameter
        && node.property.type === 'Identifier'
        && (node.property.name === 'ok' || node.property.name === 'error')
      ) outcomeInspected = true
    })
    if (!outcomeInspected) return false
    let behavior = false
    walkAstWithAncestors(statement.consequent, (node, ancestors) => {
      if (ancestors.some((ancestor) => isFunctionNode(ancestor))) return
      if (node.type === 'ThrowStatement' || node.type === 'ReturnStatement') behavior = true
      if (
        node.type === 'CallExpression'
        && node.callee.type === 'MemberExpression'
        && !node.callee.computed
        && node.callee.object.type === 'Identifier'
        && node.callee.object.name === 'console'
        && node.callee.property.type === 'Identifier'
        && node.callee.property.name === 'warn'
      ) behavior = true
    })
    return behavior
  })
}

function isExactInterruptFailurePredicate(node, rpcMethod) {
  if (node?.type !== 'LogicalExpression' || node.operator !== '&&') return false
  const isActionMatch = (candidate) => (
    candidate?.type === 'BinaryExpression'
    && candidate.operator === '==='
    && (
      (
        candidate.left.type === 'Identifier' && candidate.left.name === 'action'
        && candidate.right.type === 'StringLiteral' && candidate.right.value === rpcMethod
      )
      || (
        candidate.right.type === 'Identifier' && candidate.right.name === 'action'
        && candidate.left.type === 'StringLiteral' && candidate.left.value === rpcMethod
      )
    )
  )
  const isFailureMatch = (candidate) => (
    candidate?.type === 'BinaryExpression'
    && candidate.operator === '==='
    && (
      (
        candidate.left.type === 'OptionalMemberExpression'
        && candidate.left.object.type === 'Identifier' && candidate.left.object.name === 'result'
        && candidate.left.property.type === 'Identifier' && candidate.left.property.name === 'ok'
        && candidate.right.type === 'BooleanLiteral' && candidate.right.value === false
      )
      || (
        candidate.right.type === 'OptionalMemberExpression'
        && candidate.right.object.type === 'Identifier' && candidate.right.object.name === 'result'
        && candidate.right.property.type === 'Identifier' && candidate.right.property.name === 'ok'
        && candidate.left.type === 'BooleanLiteral' && candidate.left.value === false
      )
    )
  )
  return (isActionMatch(node.left) && isFailureMatch(node.right))
    || (isFailureMatch(node.left) && isActionMatch(node.right))
}

function moduleHelperReturnsResultError(ast, helperName) {
  if (!ast) return false
  const helper = findModuleLevelSymbol(ast, helperName)
  if (
    !helper || helper.params?.length !== 1 || helper.params[0].type !== 'Identifier'
    || helper.body?.type !== 'BlockStatement' || helper.body.body.length !== 2
  ) return false
  const parameter = helper.params[0].name
  const [loop, failure] = helper.body.body
  if (
    loop.type !== 'ForOfStatement' || loop.await || loop.right.type !== 'ArrayExpression'
    || loop.left.type !== 'VariableDeclaration' || loop.left.kind !== 'const'
    || loop.left.declarations.length !== 1 || loop.left.declarations[0].id.type !== 'Identifier'
    || loop.left.declarations[0].init !== null || loop.body.type !== 'BlockStatement'
    || loop.body.body.length !== 2
  ) return false
  const allowedFields = ['error', 'message', 'reason', 'status', 'mode']
  if (
    loop.right.elements.length !== allowedFields.length
    || !loop.right.elements.every((element, index) => (
      element?.type === 'OptionalMemberExpression'
      && element.object.type === 'Identifier' && element.object.name === parameter
      && element.property.type === 'Identifier' && element.property.name === allowedFields[index]
    ))
  ) return false
  const valueName = loop.left.declarations[0].id.name
  const [messageDeclaration, messageReturn] = loop.body.body
  if (
    messageDeclaration.type !== 'VariableDeclaration' || messageDeclaration.kind !== 'const'
    || messageDeclaration.declarations.length !== 1
  ) return false
  const messageItem = messageDeclaration.declarations[0]
  if (
    messageItem.id.type !== 'Identifier' || messageItem.init?.type !== 'CallExpression'
    || messageItem.init.callee.type !== 'Identifier' || messageItem.init.callee.name !== 'normalizeOptionalTextField'
    || messageItem.init.arguments.length !== 1
    || messageItem.init.arguments[0].type !== 'Identifier' || messageItem.init.arguments[0].name !== valueName
  ) return false
  const messageName = messageItem.id.name
  if (
    messageReturn.type !== 'IfStatement' || messageReturn.alternate !== null
    || messageReturn.test.type !== 'Identifier' || messageReturn.test.name !== messageName
    || messageReturn.consequent.type !== 'ReturnStatement'
    || messageReturn.consequent.argument?.type !== 'Identifier'
    || messageReturn.consequent.argument.name !== messageName
  ) return false
  return failure.type === 'ThrowStatement'
    && failure.argument.type === 'NewExpression'
    && failure.argument.callee.type === 'Identifier' && failure.argument.callee.name === 'Error'
    && failure.argument.arguments.length === 1
    && failure.argument.arguments[0].type === 'StringLiteral'
    && failure.argument.arguments[0].value === 'thread.interrupt ok:false response message is required'
}

function hasExecutableShapeNarrowing(symbolNode, ast = null) {
  const statements = symbolNode.body?.type === 'BlockStatement' ? symbolNode.body.body : []
  const taintedNames = new Set(
    (symbolNode.params ?? []).filter((parameter) => parameter.type === 'Identifier').map((parameter) => parameter.name),
  )
  for (const statement of statements) {
    if (statement.type === 'VariableDeclaration') {
      for (const declarator of statement.declarations) {
        if (
          declarator.id.type === 'Identifier'
          && declarator.init?.type === 'Identifier'
          && taintedNames.has(declarator.init.name)
        ) taintedNames.add(declarator.id.name)
        const call = declarator.init
        if (
          call?.type === 'CallExpression'
          && parserCallProvesNarrowing(call, [symbolNode, symbolNode.body, statement, declarator], taintedNames, ast, symbolNode)
        ) return true
      }
    }
    if (
      statement.type === 'IfStatement'
      && !isAlwaysFalseExpression(statement.test)
      && containsDirectThrow(statement.consequent)
      && isSupportedInvalidPredicate(statement.test, taintedNames)
    ) return true
    const call = statement.type === 'ExpressionStatement' ? statement.expression : null
    if (
      call?.type === 'CallExpression'
      && parserCallProvesNarrowing(call, [symbolNode, symbolNode.body, statement], taintedNames, ast, symbolNode)
    ) return true
  }
  return false
}

function isAlwaysFalseExpression(node) {
  if (node?.type === 'BooleanLiteral') return node.value === false
  return node?.type === 'LogicalExpression'
    && node.operator === '&&'
    && isAlwaysFalseExpression(node.left)
}

function containsDirectThrow(node) {
  return node?.type === 'ThrowStatement'
    || (node?.type === 'BlockStatement' && node.body.some((statement) => statement.type === 'ThrowStatement'))
}

function isSupportedInvalidPredicate(node, taintedNames) {
  if (node.type === 'LogicalExpression' && node.operator === '||') {
    return isSupportedInvalidPredicate(node.left, taintedNames)
      && isSupportedInvalidPredicate(node.right, taintedNames)
  }
  if (node.type === 'UnaryExpression' && node.operator === '!') {
    return expressionRootsInTaint(node.argument, taintedNames)
  }
  if (node.type !== 'BinaryExpression' || (node.operator !== '!==' && node.operator !== '!=')) return false
  const typeOfSide = node.left.type === 'UnaryExpression' && node.left.operator === 'typeof' ? node.left : null
  const literalSide = node.right.type === 'StringLiteral' ? node.right : null
  return Boolean(typeOfSide && literalSide && ['object', 'string', 'number', 'boolean'].includes(literalSide.value)
    && expressionRootsInTaint(typeOfSide.argument, taintedNames))
}

function expressionRootsInTaint(node, taintedNames) {
  let current = node
  while (current?.type === 'MemberExpression' && !current.computed) current = current.object
  return current?.type === 'Identifier' && taintedNames.has(current.name)
}

function parserCallProvesNarrowing(call, ancestors, taintedNames, ast, shapeSymbol) {
  if (!ast || call.callee.type !== 'MemberExpression' || call.callee.computed) return false
  if (call.callee.object.type !== 'Identifier' || call.callee.property.type !== 'Identifier') return false
  if (!call.arguments.some((argument) => argument.type === 'Identifier' && taintedNames.has(argument.name))) return false
  if (bindingShadowsNameAt(ancestors, call.callee.object.name)) return false
  const method = resolveLocalSchemaMethod(ast, call.callee.object.name, call.callee.property.name)
  if (!method) return false
  if (call.callee.property.name === 'parse') return hasExecutableShapeNarrowing(method)
  if (call.callee.property.name !== 'safeParse' || !safeParseImplementationIsProven(method)) return false
  const parent = ancestors.at(-1)
  if (parent?.type !== 'VariableDeclarator' || parent.id.type !== 'Identifier') return false
  return safeParseFailureDominates(shapeSymbol, parent.id.name, parent)
}

function resolveLocalSchemaMethod(ast, schemaName, methodName) {
  for (const statement of ast.program.body) {
    const declaration = statement.type === 'ExportNamedDeclaration' ? statement.declaration : statement
    if (declaration?.type !== 'VariableDeclaration') continue
    for (const item of declaration.declarations) {
      if (item.id.type !== 'Identifier' || item.id.name !== schemaName || item.init?.type !== 'ObjectExpression') continue
      return item.init.properties.find((property) => property.type === 'ObjectMethod' && staticPropertyKeyName(property) === methodName) ?? null
    }
  }
  return null
}

function safeParseImplementationIsProven(method) {
  const taintedNames = new Set(method.params.filter((item) => item.type === 'Identifier').map((item) => item.name))
  let invalidReturn = false
  let successReturn = false
  for (const statement of method.body.body) {
    if (
      statement.type === 'IfStatement'
      && !isAlwaysFalseExpression(statement.test)
      && isSupportedInvalidPredicate(statement.test, taintedNames)
    ) {
      const returns = statement.consequent.type === 'ReturnStatement'
        ? [statement.consequent]
        : statement.consequent.type === 'BlockStatement'
          ? statement.consequent.body.filter((child) => child.type === 'ReturnStatement')
          : []
      if (returns.some((child) => objectBooleanProperty(child.argument, 'success') === false)) invalidReturn = true
    }
    if (statement.type === 'ReturnStatement' && objectBooleanProperty(statement.argument, 'success') === true) {
      successReturn = true
    }
  }
  return invalidReturn && successReturn
}

function objectBooleanProperty(node, name) {
  if (node?.type !== 'ObjectExpression') return null
  const property = node.properties.find((item) => item.type === 'ObjectProperty' && staticPropertyKeyName(item) === name)
  return property?.value.type === 'BooleanLiteral' ? property.value.value : null
}

function safeParseFailureDominates(shapeSymbol, resultName, declarator) {
  const statements = shapeSymbol.body?.type === 'BlockStatement' ? shapeSymbol.body.body : []
  const declarationIndex = statements.findIndex((statement) => (
    statement.type === 'VariableDeclaration' && statement.declarations.includes(declarator)
  ))
  if (declarationIndex < 0) return false
  for (const statement of statements.slice(declarationIndex + 1)) {
    if (statement.type !== 'IfStatement' || !containsDirectThrow(statement.consequent)) {
      if (nodeContainsIdentifier(statement, resultName)) return false
      continue
    }
    const test = statement.test
    const member = test.type === 'UnaryExpression' && test.operator === '!' ? test.argument : test.left
    const explicitFalse = test.type === 'BinaryExpression' && test.operator === '==='
      && test.right.type === 'BooleanLiteral' && test.right.value === false
    if ((test.type === 'UnaryExpression' && test.operator === '!') || explicitFalse) {
      return member?.type === 'MemberExpression' && !member.computed
        && member.object.type === 'Identifier' && member.object.name === resultName
        && member.property.type === 'Identifier' && member.property.name === 'success'
    }
    if (nodeContainsIdentifier(statement, resultName)) return false
  }
  return false
}

function nodeContainsIdentifier(node, name) {
  let found = false
  traverseAst(node, (candidate) => {
    if (candidate.type === 'Identifier' && candidate.name === name) found = true
  })
  return found
}

function shapeDominatesConsumerUse(ast, consumerSymbol, facadeCall, consumerPath, shapePath, shapeSymbol) {
  const body = consumerSymbol.body?.type === 'BlockStatement' ? consumerSymbol.body.body : null
  if (!body) return false
  const shapeAliases = new Set()
  if (consumerPath === shapePath) {
    if (symbolBindsName(consumerSymbol, shapeSymbol)) return false
    shapeAliases.add(shapeSymbol)
  } else {
    for (const statement of ast.program.body) {
      if (
        statement.type !== 'ImportDeclaration'
        || !moduleSpecifierResolvesTo(consumerPath, statement.source.value, shapePath)
      ) {
        continue
      }
      for (const specifier of statement.specifiers) {
        if (
          specifier.type === 'ImportSpecifier'
          && moduleExportName(specifier.imported) === shapeSymbol
        ) {
          shapeAliases.add(specifier.local.name)
        }
      }
    }
  }
  if (shapeAliases.size === 0) return false
  let resultName = ''
  let callStatementIndex = -1
  for (let index = 0; index < body.length; index += 1) {
    walkAstWithAncestors(body[index], (node, ancestors) => {
      if (node !== facadeCall || resultName) return
      if (ancestors.some((ancestor) => isFunctionNode(ancestor))) return
      const parent = ancestors.at(-1)
      const declarator = parent?.type === 'AwaitExpression' ? ancestors.at(-2) : parent
      if (declarator?.type === 'VariableDeclarator' && declarator.id.type === 'Identifier') {
        resultName = declarator.id.name
        callStatementIndex = index
      }
    })
  }
  if (!resultName) return false
  const taintedNames = new Set([resultName])
  for (let index = callStatementIndex + 1; index < body.length; index += 1) {
    const statement = body[index]
    if (statement.type === 'VariableDeclaration') {
      let propagated = false
      for (const declarator of statement.declarations) {
        if (
          declarator.id.type === 'Identifier'
          && declarator.init?.type === 'Identifier'
          && taintedNames.has(declarator.init.name)
        ) {
          taintedNames.add(declarator.id.name)
          propagated = true
        }
      }
      if (propagated) continue
    }
    const validation = statement.type === 'ExpressionStatement'
      && statement.expression.type === 'CallExpression'
      ? statement.expression
      : null
    if (
      validation
      && validation.callee.type === 'Identifier'
      && shapeAliases.has(validation.callee.name)
      && !bindingShadowsNameAt([consumerSymbol, consumerSymbol.body, statement], validation.callee.name)
      && validation.arguments.some((argument) => argument.type === 'Identifier' && taintedNames.has(argument.name))
    ) return true
    if ([...taintedNames].some((name) => nodeContainsIdentifier(statement, name))) return false
  }
  return false
}

function collectUnusedPolicyFindings(auditContext, entry) {
  const filePath = auditContext.productionFacadeReferenceIndex.get(entry.key)
  if (!filePath) return []
  return [{
    key: entry.key,
    kind: entry.responsePolicy.kind,
    field: 'productionScanRoots',
    path: filePath,
    symbol: entry.facade.split('.').at(-1),
    reason: 'production facade reference exists',
  }]
}

async function buildProductionFacadeReferenceIndex(auditContext, entries, backendFacadeRpcKeys) {
  auditContext.auditStats.productionFacadeReferenceIndexBuilds += 1
  const files = await listJavaScriptSourceFiles(join(auditContext.repoRoot, 'frontend-app/src'))
  const excludedPaths = new Set([
    RPC_MATRIX_PATH,
    RPC_FACADE_PATH,
    ...BACKEND_API_FACTORY_PATHS,
    ...SERVICE_FACADE_LOCATORS.values(),
    ...[...DIRECT_FACADE_RPC_LOCATORS.values()].flatMap((locator) => [
      locator.implementationPath,
      locator.methodPath,
    ]),
  ])
  const productionFilePaths = files
    .map((absolutePath) => relative(auditContext.repoRoot, absolutePath).replaceAll('\\', '/'))
    .filter((filePath) => !isExcludedProductionScanPath(filePath))
  const astEntries = []
  const readBatchSize = 64
  for (let start = 0; start < productionFilePaths.length; start += readBatchSize) {
    astEntries.push(...await Promise.all(
      productionFilePaths.slice(start, start + readBatchSize).map(async (filePath) => (
        [filePath, await readAuditAst(auditContext, filePath)]
      )),
    ))
  }
  const astByFilePath = new Map(astEntries)
  auditContext.auditStats.productionSourceFilesIndexed = astByFilePath.size
  const index = new Map()
  const reExportStatementsByPath = new Map()
  for (const [filePath, ast] of astByFilePath) {
    const statements = ast.program.body.filter((statement) => (
      statement.source
      && (statement.type === 'ExportAllDeclaration' || statement.type === 'ExportNamedDeclaration')
    ))
    if (statements.length > 0) reExportStatementsByPath.set(filePath, statements)
  }
  const facadeModulePathsByKey = new Map()
  for (const entry of entries) {
    facadeModulePathsByKey.set(
      entry.key,
      collectFacadeReExportPaths(reExportStatementsByPath, entry),
    )
  }
  for (const [filePath, ast] of astByFilePath) {
    if (excludedPaths.has(filePath)) continue
    for (const entry of entries) {
      if (
        !index.has(entry.key)
        && astReferencesFacade(
          ast,
          filePath,
          entry,
          backendFacadeRpcKeys,
          facadeModulePathsByKey.get(entry.key),
        )
      ) {
        index.set(entry.key, filePath)
      }
    }
  }
  return index
}

function collectFacadeReExportPaths(reExportStatementsByPath, entry) {
  const facadeParts = entry.facade.split('.')
  const exportsByPath = new Map([[RPC_FACADE_PATH, new Set([facadeParts[0]])]])
  if (facadeParts.length !== 1) return exportsByPath
  let changed = true
  while (changed) {
    changed = false
    for (const [filePath, statements] of reExportStatementsByPath) {
      const exportedNames = new Set(exportsByPath.get(filePath))
      for (const statement of statements) {
        const sourceNames = exportsByPath.get(moduleSpecifierResolvedPath(filePath, statement.source.value))
        if (!sourceNames) continue
        if (statement.type === 'ExportAllDeclaration' && !statement.exported) {
          for (const name of sourceNames) exportedNames.add(name)
        }
        if (statement.type === 'ExportAllDeclaration' && statement.exported) {
          const namespaceName = moduleExportName(statement.exported)
          for (const name of sourceNames) exportedNames.add(`${namespaceName}.${name}`)
        }
        if (statement.type === 'ExportNamedDeclaration') {
          for (const specifier of statement.specifiers) {
            if (specifier.type === 'ExportNamespaceSpecifier') {
              const namespaceName = moduleExportName(specifier.exported)
              for (const name of sourceNames) exportedNames.add(`${namespaceName}.${name}`)
              continue
            }
            if (specifier.type !== 'ExportSpecifier') continue
            const localName = moduleExportName(specifier.local)
            const exportedName = moduleExportName(specifier.exported)
            if (sourceNames.has(localName)) exportedNames.add(exportedName)
            const namespacePrefix = `${localName}.`
            for (const name of sourceNames) {
              if (name.startsWith(namespacePrefix)) {
                exportedNames.add(`${exportedName}.${name.slice(namespacePrefix.length)}`)
              }
            }
          }
        }
      }
      const previousSize = exportsByPath.get(filePath)?.size ?? 0
      if (exportedNames.size > previousSize) {
        exportsByPath.set(filePath, exportedNames)
        changed = true
      }
    }
  }
  return exportsByPath
}

async function listJavaScriptSourceFiles(directory) {
  const entries = await readdir(directory, { withFileTypes: true })
  const groups = await Promise.all(entries.map(async (entry) => {
    const filePath = join(directory, entry.name)
    if (entry.isSymbolicLink()) {
      throw new Error(`production scan tree must not contain symbolic links: ${filePath}`)
    }
    if (entry.isDirectory()) {
      if (entry.name === 'dist' || entry.name === 'generated' || entry.name === 'node_modules') return []
      return listJavaScriptSourceFiles(filePath)
    }
    return entry.isFile() && /\.(?:js|jsx|mjs)$/.test(entry.name) ? [filePath] : []
  }))
  return groups.flat().sort()
}

function isExcludedProductionScanPath(filePath) {
  return (
    /\.(?:test|spec|stories)\.(?:js|jsx|mjs)$/.test(filePath)
    || filePath.includes('/__fixtures__/')
    || /\.(?:fixture|mock)\.(?:js|jsx|mjs)$/.test(filePath)
  )
}

function staticMemberExpressionParts(node) {
  if (node?.type === 'Identifier') return [node.name]
  if (node?.type !== 'MemberExpression') return null
  const objectParts = staticMemberExpressionParts(node.object)
  if (!objectParts) return null
  const propertyName = node.computed
    ? node.property.type === 'StringLiteral' ? node.property.value : ''
    : node.property.type === 'Identifier' ? node.property.name : ''
  return propertyName ? [...objectParts, propertyName] : null
}

function astReferencesFacade(ast, filePath, entry, backendFacadeRpcKeys, facadeModulePaths) {
  const bindings = collectFacadeCallBindings(
    ast,
    filePath,
    entry,
    backendFacadeRpcKeys,
    facadeModulePaths,
  )
  const namespaceMemberPaths = bindings.namespaceMemberPaths ?? new Map()
  if (
    bindings.identifierAliases.size === 0
    && bindings.namespaceAliases.size === 0
    && namespaceMemberPaths.size === 0
  ) {
    return false
  }
  const addNamespaceAliasPaths = (name, paths) => {
    const existing = namespaceMemberPaths.get(name) ?? new Set()
    const previousSize = existing.size
    for (const path of paths) existing.add(path)
    if (existing.size === previousSize) return false
    namespaceMemberPaths.set(name, existing)
    bindings.namespaceAliases.add(name)
    return true
  }
  const propagateMemberAlias = (localName, expression) => {
    const parts = staticMemberExpressionParts(expression)
    if (!parts || parts.length < 2) return false
    const sourcePaths = namespaceMemberPaths.get(parts[0])
    if (!sourcePaths) return false
    const consumedPath = parts.slice(1).join('.')
    const remainingPaths = []
    let exact = false
    for (const targetPath of sourcePaths) {
      if (targetPath === consumedPath) exact = true
      else if (targetPath.startsWith(`${consumedPath}.`)) {
        remainingPaths.push(targetPath.slice(consumedPath.length + 1))
      }
    }
    let propagated = false
    if (exact && !bindings.identifierAliases.has(localName)) {
      bindings.identifierAliases.add(localName)
      propagated = true
    }
    return addNamespaceAliasPaths(localName, remainingPaths) || propagated
  }
  let changed = true
  while (changed) {
    changed = false
    traverseAst(ast, (node) => {
      if (
        node.type === 'VariableDeclarator'
        && node.id.type === 'Identifier'
        && node.init?.type === 'Identifier'
        && bindings.identifierAliases.has(node.init.name)
        && !bindings.identifierAliases.has(node.id.name)
      ) {
        bindings.identifierAliases.add(node.id.name)
        changed = true
      }
      if (
        node.type === 'VariableDeclarator'
        && node.id.type === 'Identifier'
        && node.init?.type === 'Identifier'
        && namespaceMemberPaths.has(node.init.name)
      ) {
        changed = addNamespaceAliasPaths(
          node.id.name,
          namespaceMemberPaths.get(node.init.name),
        ) || changed
      }
      if (
        node.type === 'VariableDeclarator'
        && node.id.type === 'Identifier'
        && node.init?.type === 'MemberExpression'
      ) {
        changed = propagateMemberAlias(node.id.name, node.init) || changed
      }
      if (
        node.type === 'VariableDeclarator'
        && node.id.type === 'Identifier'
        && node.init?.type === 'MemberExpression'
        && !node.init.computed
        && node.init.object.type === 'Identifier'
        && bindings.namespaceAliases.has(node.init.object.name)
        && node.init.property.type === 'Identifier'
        && (
          node.init.property.name === bindings.memberName
          || bindings.namespaceMemberNames?.has(node.init.property.name)
        )
        && !bindings.identifierAliases.has(node.id.name)
      ) {
        bindings.identifierAliases.add(node.id.name)
        changed = true
      }
      if (
        node.type === 'VariableDeclarator'
        && node.id.type === 'ObjectPattern'
        && node.init?.type === 'Identifier'
        && bindings.namespaceAliases.has(node.init.name)
      ) {
        for (const property of node.id.properties) {
          if (
            property.type === 'ObjectProperty'
            && (
              staticPropertyKeyName(property) === bindings.memberName
              || bindings.namespaceMemberNames?.has(staticPropertyKeyName(property))
            )
            && property.value.type === 'Identifier'
            && !bindings.identifierAliases.has(property.value.name)
          ) {
            bindings.identifierAliases.add(property.value.name)
            changed = true
          }
        }
      }
    })
  }
  let found = false
  walkAstWithAncestors(ast, (node, ancestors) => {
    if (found) return
    if (
      node.type === 'Identifier'
      && bindings.identifierAliases.has(node.name)
      && isReferencedIdentifierAt(node, ancestors)
      && !bindingShadowsNameAt(ancestors, node.name)
    ) {
      found = true
      return
    }
    const memberParts = staticMemberExpressionParts(node)
    if (memberParts?.length > 1) {
      const targetPaths = namespaceMemberPaths.get(memberParts[0])
      if (
        targetPaths?.has(memberParts.slice(1).join('.'))
        && !bindingShadowsNameAt(ancestors, memberParts[0])
      ) {
        found = true
        return
      }
    }
    if (
      node.type === 'MemberExpression'
      && !node.computed
      && node.object.type === 'Identifier'
      && node.property.type === 'Identifier'
      && (
        node.property.name === bindings.memberName
        || bindings.namespaceMemberNames?.has(node.property.name)
      )
      && bindings.namespaceAliases.has(node.object.name)
      && !bindingShadowsNameAt(ancestors, node.object.name)
    ) {
      found = true
    }
  })
  return found
}

function isReferencedIdentifierAt(node, ancestors) {
  const parent = ancestors.at(-1)
  return !(
    parent?.type === 'ImportSpecifier'
    || parent?.type === 'ImportNamespaceSpecifier'
    || (parent?.type === 'VariableDeclarator' && parent.id === node)
    || (
      (parent?.type === 'FunctionDeclaration' || parent?.type === 'FunctionExpression')
      && (parent.id === node || parent.params.includes(node))
    )
    || (
      (parent?.type === 'ObjectProperty' || parent?.type === 'ObjectMethod')
      && parent.key === node
      && !parent.computed
    )
    || (parent?.type === 'MemberExpression' && parent.property === node && !parent.computed)
  )
}

function collectNamedExports(source) {
  const names = new Set()
  for (const statement of parseFrontendAst(source).program.body) {
    if (statement.type !== 'ExportNamedDeclaration') continue
    for (const specifier of statement.specifiers) {
      const exportedName = moduleExportName(specifier.exported)
      if (exportedName) names.add(exportedName)
    }
    collectDeclarationBindingNames(statement.declaration, names)
  }
  return names
}

function collectDeclarationBindingNames(declaration, names) {
  if (!declaration) return
  if (declaration.type === 'VariableDeclaration') {
    for (const entry of declaration.declarations) collectBindingNames(entry.id, names)
    return
  }
  if (
    (declaration.type === 'FunctionDeclaration' || declaration.type === 'ClassDeclaration')
    && declaration.id?.name
  ) {
    names.add(declaration.id.name)
  }
}

function collectBindingNames(pattern, names) {
  if (!pattern) return
  if (pattern.type === 'Identifier') {
    names.add(pattern.name)
    return
  }
  if (pattern.type === 'AssignmentPattern') return collectBindingNames(pattern.left, names)
  if (pattern.type === 'RestElement') return collectBindingNames(pattern.argument, names)
  if (pattern.type === 'ArrayPattern') {
    for (const entry of pattern.elements) collectBindingNames(entry, names)
    return
  }
  if (pattern.type === 'ObjectPattern') {
    for (const entry of pattern.properties) {
      collectBindingNames(entry.type === 'RestElement' ? entry.argument : entry.value, names)
    }
  }
}

async function collectBackendFacadeRpcKeys(auditContext) {
  const facadeRpcKeys = new Map()
  for (const filePath of BACKEND_API_FACTORY_PATHS) {
    const ast = await readAuditAst(auditContext, filePath)
    traverseAst(ast, (node) => {
      if (
        node.type !== 'FunctionDeclaration'
        || !/^create[A-Za-z0-9]+Api$/.test(node.id?.name ?? '')
      ) {
        return
      }
      for (const statement of node.body.body) {
        if (statement.type !== 'ReturnStatement' || statement.argument?.type !== 'ObjectExpression') continue
        for (const property of statement.argument.properties) {
          if (property.type !== 'ObjectMethod' && property.type !== 'ObjectProperty') continue
          const facade = staticPropertyKeyName(property)
          if (!facade) continue
          const rpcKeys = collectRpcMethodReferenceKeysWithHelpers(property, ast)
          if (rpcKeys.size !== 1) continue
          const [rpcKey] = rpcKeys
          const existing = facadeRpcKeys.get(facade)
          if (existing && existing !== rpcKey) {
            throw new Error(`backend API facade ${facade} maps both ${existing} and ${rpcKey}`)
          }
          facadeRpcKeys.set(facade, rpcKey)
        }
      }
    })
  }
  for (const [rpcKey, locator] of DIRECT_FACADE_RPC_LOCATORS.entries()) {
    const implementationSource = await readAuditSource(auditContext, locator.implementationPath)
    const methodSource = locator.methodPath === locator.implementationPath
      ? implementationSource
      : await readAuditSource(auditContext, locator.methodPath)
    if (
      !collectNamedExports(implementationSource).has(locator.facade)
      || !sourceDeclaresFunction(implementationSource, locator.facade)
      || !sourceContainsStringLiteral(methodSource, locator.method)
    ) {
      throw new Error(`${locator.facade} must trace to ${locator.method} for ${rpcKey}`)
    }
    facadeRpcKeys.set(locator.facade, rpcKey)
  }
  return facadeRpcKeys
}

function collectRpcMethodReferenceKeysWithHelpers(node, ast) {
  const keys = collectRpcMethodReferenceKeys(node)
  const helperNames = new Set()
  traverseAst(node, (candidate) => {
    if (candidate.type === 'CallExpression' && candidate.callee.type === 'Identifier') {
      helperNames.add(candidate.callee.name)
    }
  })
  if (helperNames.size === 0) return keys
  traverseAst(ast, (candidate) => {
    if (candidate.type === 'FunctionDeclaration' && helperNames.has(candidate.id?.name ?? '')) {
      for (const key of collectRpcMethodReferenceKeys(candidate)) keys.add(key)
    }
  })
  return keys
}

function collectRpcMethodReferenceKeys(node) {
  const keys = new Set()
  traverseAst(node, (candidate) => {
    if (
      candidate.type === 'MemberExpression'
      && !candidate.computed
      && candidate.object.type === 'Identifier'
      && candidate.object.name === 'RPC_METHODS'
      && candidate.property.type === 'Identifier'
    ) {
      keys.add(candidate.property.name)
    }
  })
  return keys
}

function staticPropertyKeyName(property) {
  if (property.computed) return ''
  if (property.key.type === 'Identifier') return property.key.name
  if (property.key.type === 'StringLiteral') return property.key.value
  return ''
}

function sourceDeclaresFunction(source, functionName) {
  let found = false
  traverseAst(parseFrontendAst(source), (node) => {
    if (node.type === 'FunctionDeclaration' && node.id?.name === functionName) found = true
  })
  return found
}

function sourceContainsStringLiteral(source, value) {
  let found = false
  traverseAst(parseFrontendAst(source), (node) => {
    if (node.type === 'StringLiteral' && node.value === value) found = true
  })
  return found
}

function serviceFacadeMemberRpcKey(source, serviceName, memberName, backendFacadeRpcKeys) {
  if (!serviceName || !memberName || !collectNamedExports(source).has(serviceName)) return ''
  const ast = parseFrontendAst(source)
  let factoryName = ''
  traverseAst(ast, (node) => {
    if (
      factoryName
      || node.type !== 'VariableDeclarator'
      || node.id.type !== 'Identifier'
      || node.id.name !== serviceName
      || node.init?.type !== 'CallExpression'
      || node.init.callee.type !== 'Identifier'
    ) {
      return
    }
    factoryName = node.init.callee.name
  })
  if (!factoryName) return ''

  let factory = null
  traverseAst(ast, (node) => {
    if (!factory && node.type === 'FunctionDeclaration' && node.id?.name === factoryName) factory = node
  })
  if (!factory) return ''
  const apiParameterName = factory.params[0]?.type === 'AssignmentPattern'
    ? factory.params[0].left?.name
    : factory.params[0]?.name
  if (!apiParameterName) return ''

  const returnedObjects = []
  for (const statement of factory.body.body) {
    if (statement.type !== 'ReturnStatement') continue
    if (statement.argument?.type === 'ObjectExpression') {
      returnedObjects.push(statement.argument)
      continue
    }
    if (statement.argument?.type !== 'Identifier') continue
    const returnedBinding = factory.body.body.find((candidate) => (
      candidate.type === 'VariableDeclaration'
      && candidate.declarations.some((declaration) => (
        declaration.id.type === 'Identifier'
        && declaration.id.name === statement.argument.name
        && declaration.init?.type === 'ObjectExpression'
      ))
    ))
    if (!returnedBinding) continue
    const declarator = returnedBinding.declarations.find((declaration) => (
      declaration.id.type === 'Identifier' && declaration.id.name === statement.argument.name
    ))
    returnedObjects.push(declarator.init)
  }

  for (const objectExpression of returnedObjects) {
    const member = objectExpression.properties
      .filter((property) => property.type === 'ObjectMethod' || property.type === 'ObjectProperty')
      .find((property) => staticPropertyKeyName(property) === memberName)
    if (!member) continue
    const backendFacades = new Set()
    traverseAst(member, (node) => {
      if (
        node.type === 'MemberExpression'
        && !node.computed
        && node.object.type === 'Identifier'
        && node.object.name === apiParameterName
        && node.property.type === 'Identifier'
      ) {
        backendFacades.add(node.property.name)
      }
    })
    if (backendFacades.size !== 1) return ''
    return backendFacadeRpcKeys.get([...backendFacades][0]) ?? ''
  }
  return ''
}

function assertRpcMethodsFacadeReExport(source) {
  const ast = parseFrontendAst(source)
  let exactReExportCount = 0
  let conflictingBindingCount = 0

  for (const statement of ast.program.body) {
    if (statement.type === 'ImportDeclaration') {
      conflictingBindingCount += statement.specifiers
        .filter((specifier) => specifier.local?.name === 'RPC_METHODS')
        .length
      continue
    }
    if (statement.type === 'ExportNamedDeclaration') {
      if (declarationBindsName(statement.declaration, 'RPC_METHODS')) {
        conflictingBindingCount += 1
      }
      for (const specifier of statement.specifiers) {
        if (specifier.type !== 'ExportSpecifier') continue
        const localName = moduleExportName(specifier.local)
        const exportedName = moduleExportName(specifier.exported)
        if (localName !== 'RPC_METHODS' && exportedName !== 'RPC_METHODS') continue
        if (
          statement.source?.value === RPC_FACADE_REEXPORT_SOURCE
          && localName === 'RPC_METHODS'
          && exportedName === 'RPC_METHODS'
        ) {
          exactReExportCount += 1
        } else {
          conflictingBindingCount += 1
        }
      }
      continue
    }
    if (declarationBindsName(statement, 'RPC_METHODS')) {
      conflictingBindingCount += 1
    }
  }

  if (exactReExportCount !== 1 || conflictingBindingCount !== 0) {
    throw new Error(`backendApi.js must named re-export RPC_METHODS from ${RPC_FACADE_REEXPORT_SOURCE} exactly once`)
  }
}

function moduleExportName(node) {
  if (node?.type === 'Identifier' || node?.type === 'StringLiteral') return node.name ?? node.value
  return ''
}

function declarationBindsName(declaration, name) {
  if (!declaration) return false
  if (declaration.type === 'VariableDeclaration') {
    return declaration.declarations.some((entry) => bindingPatternContainsName(entry.id, name))
  }
  if (declaration.type === 'FunctionDeclaration' || declaration.type === 'ClassDeclaration') {
    return declaration.id?.name === name
  }
  return false
}

function bindingPatternContainsName(pattern, name) {
  if (!pattern) return false
  if (pattern.type === 'Identifier') return pattern.name === name
  if (pattern.type === 'AssignmentPattern') return bindingPatternContainsName(pattern.left, name)
  if (pattern.type === 'RestElement') return bindingPatternContainsName(pattern.argument, name)
  if (pattern.type === 'ArrayPattern') {
    return pattern.elements.some((entry) => bindingPatternContainsName(entry, name))
  }
  if (pattern.type === 'ObjectPattern') {
    return pattern.properties.some((entry) => (
      entry.type === 'RestElement'
        ? bindingPatternContainsName(entry.argument, name)
        : bindingPatternContainsName(entry.value, name)
    ))
  }
  return false
}

function findFrozenObjectExport(source, exportName) {
  let found = null
  traverseAst(parseFrontendAst(source), (node) => {
    if (found || node.type !== 'ExportNamedDeclaration' || node.declaration?.type !== 'VariableDeclaration') {
      return
    }
    for (const declarator of node.declaration.declarations) {
      if (declarator.id.type !== 'Identifier' || declarator.id.name !== exportName) {
        continue
      }
      found = unwrapObjectFreezeObject(declarator.init)
      if (!found) {
        throw new Error(`${exportName} must be assigned Object.freeze({...})`)
      }
    }
  })
  return found
}

function unwrapObjectFreezeObject(node) {
  if (
    node?.type === 'CallExpression'
    && node.callee.type === 'MemberExpression'
    && node.callee.object.type === 'Identifier'
    && node.callee.object.name === 'Object'
    && node.callee.property.type === 'Identifier'
    && node.callee.property.name === 'freeze'
    && node.arguments[0]?.type === 'ObjectExpression'
  ) {
    return node.arguments[0]
  }
  return null
}

/**
 * @param {string} source
 * @param {{ errorRecovery?: boolean }} [options]
 */
function parseFrontendAst(source, options = {}) {
  return parseJavaScriptSource(source, {
    sourceType: 'module',
    plugins: ['jsx', 'typescript'],
    errorRecovery: options.errorRecovery ?? false,
  })
}

function traverseAst(node, visit) {
  if (!node || typeof node.type !== 'string') return
  visit(node)
  for (const value of Object.values(node)) {
    if (!value) continue
    if (Array.isArray(value)) {
      for (const child of value) {
        traverseAst(child, visit)
      }
    } else if (typeof value.type === 'string') {
      traverseAst(value, visit)
    }
  }
}

function objectPropertiesOnly(objectExpression, label) {
  return objectExpression.properties.map((property) => {
    if (property.type !== 'ObjectProperty') {
      throw new Error(`${label} entries must be object properties`)
    }
    return property
  })
}

function propertyKeyName(property) {
  if (property.key.type === 'Identifier' && !property.computed) return property.key.name
  if (property.key.type === 'StringLiteral') return property.key.value
  throw new Error('Object property key must be an identifier or string literal')
}

function stringLiteralValue(node, label) {
  if (node?.type !== 'StringLiteral') {
    throw new Error(`${label} must be a string literal`)
  }
  return node.value
}

function collectFrontendResponseValidators(source) {
  const validators = new Map()
  traverseAst(parseFrontendAst(source), (node) => {
    if (node.type !== 'ReturnStatement') return
    const objectExpression = unwrapObjectFreezeObject(node.argument)
    if (!objectExpression) return
    for (const property of objectPropertiesOnly(objectExpression, 'backend response validators')) {
      if (
        !property.computed
        || property.key.type !== 'MemberExpression'
        || property.key.computed
        || property.key.object.type !== 'Identifier'
        || property.key.object.name !== 'methods'
        || property.key.property.type !== 'Identifier'
        || property.value.type !== 'Identifier'
      ) {
        continue
      }
      const key = property.key.property.name
      if (validators.has(key)) {
        throw new Error(`backendResponseValidators.js maps ${key} more than once`)
      }
      validators.set(key, property.value.name)
    }
  })
  return validators
}

async function collectGoPayloadKeys(auditContext) {
  const out = new Map()
  for (const [method, locators] of GO_PAYLOAD_STRUCTS.entries()) {
    const keys = []
    for (const locator of locators) {
      const [filePath, symbol] = locator.split(':')
      const source = await readAuditSource(auditContext, filePath)
      keys.push(...parseGoStructJSONTags(source, symbol))
    }
    out.set(method, uniqueSorted(keys))
  }
  return out
}

function parseGoStructJSONTags(source, symbol) {
  const structMatch = source.match(new RegExp(`type\\s+${symbol}\\s+struct\\s*\\{([\\s\\S]*?)\\n\\}`))
  if (!structMatch) {
    throw new Error(`${symbol} struct was not found in Go DTO source`)
  }
  const keys = []
  for (const line of structMatch[1].split('\n')) {
    const tag = line.match(/`[^`]*json:"([^"]*)"[^`]*`/)
    if (!tag) continue
    const name = tag[1].split(',')[0]
    if (!name || name === '-') {
      continue
    }
    keys.push(name)
  }
  return keys
}

async function collectHardcodedPayloadGuardFindings(auditContext, frontendSource) {
  const inspectedFiles = uniqueSorted([
    ...new Set([...GO_PAYLOAD_STRUCTS.values()].flat().map((locator) => locator.split(':')[0])),
  ])
  const goSources = new Map()
  await Promise.all(inspectedFiles.map(async (filePath) => {
    goSources.set(filePath, await readAuditSource(auditContext, filePath))
  }))
  return collectHardcodedPayloadGuardFindingsFromSources({
    frontendPath: FRONTEND_PAYLOAD_BUILDERS_PATH,
    frontendSource,
    goSources,
  })
}

export function collectHardcodedPayloadGuardFindingsFromSources({
  frontendPath = RPC_FACADE_PATH,
  frontendSource = '',
  goSources = new Map(),
} = {}) {
  const findings = []
  const frontendAst = parseFrontendAst(frontendSource)
  for (const statement of frontendAst.program.body) {
    const declaration = statement.type === 'ExportNamedDeclaration' ? statement.declaration : statement
    if (declaration?.type !== 'VariableDeclaration') continue
    for (const declarator of declaration.declarations) {
      const name = declarator.id.type === 'Identifier' ? declarator.id.name : ''
      const isPayloadGuardName = (
        name === 'RPC_ALLOWED_PAYLOAD_KEYS'
        || /^[A-Z0-9_]+_ALLOWED_KEYS$/.test(name)
      )
      const isSetOfArray = (
        declarator.init?.type === 'NewExpression'
        && declarator.init.callee.type === 'Identifier'
        && declarator.init.callee.name === 'Set'
        && declarator.init.arguments[0]?.type === 'ArrayExpression'
      )
      if (isPayloadGuardName && isSetOfArray) {
        findings.push(`${frontendPath}:${name}`)
      }
    }
  }
  for (const [filePath, source] of goSources.entries()) {
    const goMapPattern = /^\s*var\s+([A-Za-z0-9_]*(?:Param|Payload)[A-Za-z0-9_]*(?:Fields|Keys))\s*=\s*map\[string\]struct\{\}/gm
    let goMapMatch
    while ((goMapMatch = goMapPattern.exec(source)) !== null) {
      findings.push(`${filePath}:${goMapMatch[1]}`)
    }
  }
  return findings
}

export function collectFrontendPayloadKeysFromSource(source) {
  const ast = parseFrontendAst(source, { errorRecovery: true })
  const functionDeclarationsByMethod = new Map()

  for (const [method, functionName] of FRONTEND_PAYLOAD_BUILDERS.entries()) {
    const declarations = ast.program.body.filter((node) => (
      node.type === 'FunctionDeclaration' && node.id?.name === functionName
    ))
    if (declarations.length !== 1) {
      throw new Error(
        `${functionName} must have exactly one top-level FunctionDeclaration in ${FRONTEND_PAYLOAD_BUILDERS_PATH}; found ${declarations.length}`,
      )
    }
    const [{ start, end }] = declarations
    if (!Number.isInteger(start) || !Number.isInteger(end)) {
      throw new Error(`${functionName} FunctionDeclaration is missing source offsets in ${FRONTEND_PAYLOAD_BUILDERS_PATH}`)
    }
    functionDeclarationsByMethod.set(method, declarations[0])
  }

  const [parseError] = ast.errors ?? []
  if (parseError) throw parseError

  const out = new Map()
  for (const [method, functionDeclaration] of functionDeclarationsByMethod.entries()) {
    out.set(method, extractConsumedPayloadKeys(functionDeclaration))
  }
  return out
}

export function collectPayloadRegistryDrift(goPayloadKeysByMethod, frontendPayloadKeysByMethod) {
  const drift = []
  for (const [method, goKeys] of goPayloadKeysByMethod.entries()) {
    if (FRONTEND_PAYLOAD_METHOD_EXEMPTIONS.has(method)) {
      continue
    }
    const frontendKeys = frontendPayloadKeysByMethod.get(method) ?? []
    const goKeySet = new Set(goKeys)
    const frontendKeySet = new Set(frontendKeys)
    const facadeOnlyKeys = new Set(FRONTEND_FACADE_ONLY_PAYLOAD_KEYS.get(method) ?? [])
    const missingFrontendKeys = goKeys.filter((key) => !frontendKeySet.has(key))
    const extraFrontendKeys = frontendKeys.filter((key) => !goKeySet.has(key) && !facadeOnlyKeys.has(key))
    if (missingFrontendKeys.length > 0 || extraFrontendKeys.length > 0) {
      drift.push({
        method,
        missingFrontendKeys,
        extraFrontendKeys,
      })
    }
  }
  return drift.sort((a, b) => a.method.localeCompare(b.method))
}

const NESTED_PAYLOAD_SCOPE_NODE_TYPES = new Set([
  'ArrowFunctionExpression',
  'ClassMethod',
  'ClassPrivateMethod',
  'FunctionDeclaration',
  'FunctionExpression',
  'ObjectMethod',
])

const CLASS_FIELD_NODE_TYPES = new Set([
  'ClassAccessorProperty',
  'ClassPrivateProperty',
  'ClassProperty',
])

function extractConsumedPayloadKeys(functionDeclaration) {
  const keys = []
  traversePayloadBuilderRootScope(functionDeclaration, (node) => {
    if (node.type !== 'CallExpression' || node.callee.type !== 'Identifier') return
    const [payload, keySource] = node.arguments
    if (payload?.type !== 'Identifier' || payload.name !== 'unused') return

    if (node.callee.name === 'takePayloadField') {
      if (keySource?.type === 'StringLiteral') keys.push(keySource.value)
      return
    }
    if (node.callee.name !== 'takePayloadFields' || keySource?.type !== 'ArrayExpression') return
    for (const element of keySource.elements) {
      if (element?.type === 'StringLiteral') keys.push(element.value)
    }
  })
  return uniqueSorted(keys)
}

function traversePayloadBuilderRootScope(node, visit, isRootFunction = true) {
  if (!node || typeof node.type !== 'string') return
  if (!isRootFunction && NESTED_PAYLOAD_SCOPE_NODE_TYPES.has(node.type)) return
  if (CLASS_FIELD_NODE_TYPES.has(node.type)) {
    visit(node)
    if (node.computed) traversePayloadBuilderRootScope(node.key, visit, false)
    if (node.static) traversePayloadBuilderRootScope(node.value, visit, false)
    return
  }
  visit(node)
  for (const value of Object.values(node)) {
    if (!value) continue
    if (Array.isArray(value)) {
      for (const child of value) {
        traversePayloadBuilderRootScope(child, visit, false)
      }
    } else if (typeof value.type === 'string') {
      traversePayloadBuilderRootScope(value, visit, false)
    }
  }
}

function uniqueSorted(values) {
  return [...new Set(values)].sort((a, b) => a.localeCompare(b))
}

async function collectGoRpcHandlers(auditContext) {
  const { repoRoot } = auditContext
  const constants = await collectGoRpcConstants(auditContext)
  const goFiles = []
  for (const root of GO_HANDLER_ROOTS) {
    goFiles.push(...await collectGoFiles(join(repoRoot, root)))
  }

  const sources = []
  for (const filePath of goFiles) {
    const auditPath = relative(repoRoot, filePath).replaceAll('\\', '/')
    const source = auditContext.sourcePromiseByPath.has(auditPath)
      ? await readAuditSource(auditContext, auditPath)
      : readAuditSourceSync(auditContext, auditPath)
    sources.push({ filePath, source })
  }

  const handlers = []
  for (const { filePath, source } of sources) {
    const sourceHandlers = [
      ...parseLiteralHandlerRegistrations(source, filePath, repoRoot),
      ...parseConstantHandlerRegistrations(source, filePath, repoRoot, constants),
    ]
    handlers.push(...sourceHandlers)
  }
  return uniqueHandlers(handlers)
}

async function collectGoRpcConstants(auditContext) {
  const source = await readAuditSource(auditContext, GO_RPC_CONSTANTS_PATH)
  const constants = new Map()
  const constPattern = /^\s*([A-Za-z0-9_]+)\s*=\s*"([^"]+\/[^"]+)"/gm
  let match
  while ((match = constPattern.exec(source)) !== null) {
    constants.set(match[1], match[2])
  }
  return constants
}

async function collectGoFiles(root) {
  const entries = await readdir(root, { withFileTypes: true })
  const groups = await Promise.all(entries.map(async (entry) => {
    const fullPath = join(root, entry.name)
    if (entry.isDirectory()) return collectGoFiles(fullPath)
    return entry.name.endsWith('.go') && !entry.name.endsWith('_test.go') ? [fullPath] : []
  }))
  return groups.flat()
}

function parseLiteralHandlerRegistrations(source, filePath, repoRoot) {
  const handlerNames = GO_HANDLER_CALLS.join('|')
  const registrations = []
  const patterns = [
    /"([^"]+\/[^"]+)"\s*:/g,
    new RegExp(`"([^"]+/[^"]+)"\\s*:\\s*(?:[a-zA-Z0-9_]+\\.)?(?:${handlerNames})\\b`, 'g'),
    new RegExp(`\\[\\s*"([^"]+/[^"]+)"\\s*\\]\\s*=\\s*(?:[a-zA-Z0-9_]+\\.)?(?:${handlerNames})\\b`, 'g'),
  ]

  for (const pattern of patterns) {
    let match
    while ((match = pattern.exec(source)) !== null) {
      registrations.push(handlerEntry(match[1], filePath, repoRoot))
    }
  }
  return registrations
}

function parseConstantHandlerRegistrations(source, filePath, repoRoot, constants) {
  const handlerNames = GO_HANDLER_CALLS.join('|')
  const registrations = []
  const patterns = [
    /contract\.([A-Za-z0-9_]+)\s*:/g,
    new RegExp(`(?:[a-zA-Z0-9_]+\\.)?(?:${handlerNames})\\(\\s*contract\\.([A-Za-z0-9_]+)`, 'g'),
  ]

  for (const pattern of patterns) {
    let match
    while ((match = pattern.exec(source)) !== null) {
      const method = constants.get(match[1])
      if (method) {
        registrations.push(handlerEntry(method, filePath, repoRoot))
      }
    }
  }
  return registrations
}

function handlerEntry(method, filePath, repoRoot) {
  return {
    method,
    file: relative(repoRoot, filePath).replaceAll('\\', '/'),
  }
}

function uniqueHandlers(handlers) {
  const byMethod = new Map()
  for (const handler of handlers) {
    if (!byMethod.has(handler.method)) {
      byMethod.set(handler.method, handler)
    }
  }
  return [...byMethod.values()].sort((a, b) => a.method.localeCompare(b.method))
}

if (process.argv[1] === fileURLToPath(import.meta.url)) {
  const report = await auditRpcContracts()
  console.log(formatRpcAuditReport(report))

  const failures = [
    ['Missing registry keys', report.missingRegistryKeys],
    ['Registry entries without RPC_METHODS', report.registryWithoutRpcMethods],
    ['Mismatched registry methods', report.mismatchedRegistryMethods],
    ['P0 methods missing Go handlers', report.p0MissingBackendHandlers],
    ['Allowed payload registry drift', report.allowedPayloadRegistryDrift],
    ['Hardcoded payload guards', report.hardcodedPayloadGuardFindings],
    ['Missing response policies', report.missingResponsePolicies],
    ['Missing frontend response validators', report.missingFrontendResponseValidators],
    ['Invalid facade locators', report.invalidFacadeLocators],
    ['Invalid response policy evidence', report.invalidResponsePolicyEvidence],
  ].filter(([, values]) => values.length > 0)

  if (failures.length > 0) {
    for (const [title, values] of failures) {
      console.error(`\n${title}:`)
      console.error(JSON.stringify(values, null, 2))
    }
    process.exit(1)
  }
}
