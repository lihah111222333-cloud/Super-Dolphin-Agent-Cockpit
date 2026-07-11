import { readFile, readdir } from 'node:fs/promises'
import { dirname, join, relative, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'
import { parse as parseJavaScriptSource } from '@babel/parser'

const SCRIPT_DIR = dirname(fileURLToPath(import.meta.url))
const DEFAULT_FRONTEND_ROOT = resolve(SCRIPT_DIR, '..')
const DEFAULT_REPO_ROOT = resolve(DEFAULT_FRONTEND_ROOT, '..')

const FRONTEND_FACADE_PATH = 'frontend-app/src/shared/api/backendApi.js'
const RPC_METHODS_PATH = 'frontend-app/src/shared/api/backend/backendRpcMethods.js'
const FRONTEND_PAYLOAD_BUILDERS_PATH = 'frontend-app/src/shared/api/backend/backendApiFactoryThread.js'
const RPC_RESPONSE_VALIDATORS_PATH = 'frontend-app/src/shared/api/backendResponseValidators.js'
const RPC_MATRIX_PATH = 'frontend-app/src/shared/api/backendApi.contractMatrix.js'
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
])

const FRONTEND_PAYLOAD_BUILDERS = new Map([
  ['thread/start', 'threadStartPayload'],
  ['turn/start', 'turnStartPayload'],
])

const REQUIRED_RESPONSE_POLICY_KEYS = new Set([
  'UI_STATE_GET',
  'THREAD_START',
  'THREAD_MESSAGES',
  'THREAD_RESOLVE',
  'TURN_START',
  'TURN_INTERRUPT',
])

const FRONTEND_RESPONSE_VALIDATOR_KEYS = new Set([
  'UI_STATE_GET',
  'THREAD_START',
  'THREAD_MESSAGES',
  'THREAD_RESOLVE',
  'TURN_START',
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
])

const GO_HANDLER_CALLS = [
  'StrictHandler',
  'LoggedStrictHandler',
  'ThreadHandler',
  'CapabilityThreadHandler',
]

export async function auditRpcContracts({ repoRoot = DEFAULT_REPO_ROOT } = {}) {
  const facadeSource = await readFile(join(repoRoot, FRONTEND_FACADE_PATH), 'utf8')
  const rpcMethodsSource = await readFile(join(repoRoot, RPC_METHODS_PATH), 'utf8')
  const payloadBuildersSource = await readFile(join(repoRoot, FRONTEND_PAYLOAD_BUILDERS_PATH), 'utf8')
  const rpcMethods = parseRpcMethods(rpcMethodsSource)
  const methodsByKey = new Map(rpcMethods.map((entry) => [entry.key, entry]))
  const registryEntries = parseContractMatrix(
    await readFile(join(repoRoot, RPC_MATRIX_PATH), 'utf8'),
  ).map((entry) => ({
    ...entry,
    method: methodsByKey.get(entry.key)?.method ?? '',
  }))
  const backendHandlers = await collectGoRpcHandlers(repoRoot)
  const goPayloadKeysByMethod = await collectGoPayloadKeys(repoRoot)
  const frontendPayloadKeysByMethod = collectFrontendPayloadKeysFromSource(payloadBuildersSource)
  const hardcodedPayloadGuardFindings = await collectHardcodedPayloadGuardFindings(repoRoot, facadeSource)

  const registryByKey = new Map(registryEntries.map((entry) => [entry.key, entry]))
  const handlerMethods = new Set(backendHandlers.map((entry) => entry.method))
  const responseValidatorSource = await readFile(join(repoRoot, RPC_RESPONSE_VALIDATORS_PATH), 'utf8')
  const frontendResponseValidatorKeys = collectFrontendResponseValidatorKeys([
    facadeSource,
    responseValidatorSource,
  ])

  const missingRegistryKeys = rpcMethods
    .filter((entry) => !registryByKey.has(entry.key))
    .map((entry) => entry.key)
    .sort()
  const registryWithoutRpcMethods = registryEntries
    .filter((entry) => !methodsByKey.has(entry.key))
    .map((entry) => entry.key)
    .sort()
  const mismatchedRegistryMethods = []
  const p0MissingBackendHandlers = registryEntries
    .filter((entry) => entry.level === 'P0' && !handlerMethods.has(entry.method))
    .map((entry) => ({
      key: entry.key,
      method: entry.method,
    }))
  const allowedPayloadRegistryDrift = collectPayloadRegistryDrift(goPayloadKeysByMethod, frontendPayloadKeysByMethod)
  const missingResponsePolicies = registryEntries
    .filter((entry) => REQUIRED_RESPONSE_POLICY_KEYS.has(entry.key))
    .filter((entry) => !entry.responseValidator && !entry.responsePassthroughReason)
    .map((entry) => ({
      key: entry.key,
      method: entry.method,
    }))
  const missingFrontendResponseValidators = [...FRONTEND_RESPONSE_VALIDATOR_KEYS]
    .filter((key) => !frontendResponseValidatorKeys.has(key))
    .map((key) => ({
      key,
      method: methodsByKey.get(key)?.method ?? '',
    }))
    .sort((a, b) => a.key.localeCompare(b.key))

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
    missingFrontendResponseValidators,
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
  ].join('\n')
}

function parseRpcMethods(source) {
  const objectExpression = findFrozenObjectExport(source, 'RPC_METHODS')
  if (!objectExpression) {
    throw new Error(`RPC_METHODS object was not found in ${RPC_METHODS_PATH}`)
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

function parseContractRegistryProperty(property) {
  const key = propertyKeyName(property)
  if (property.value.type !== 'CallExpression' || property.value.callee.type !== 'Identifier' || property.value.callee.name !== 'contract') {
    throw new Error(`RPC_CONTRACT_REGISTRY.${key} must call contract(...)`)
  }
  const args = property.value.arguments
  const options = args[7]?.type === 'ObjectExpression' ? args[7] : null
  return {
    key,
    declaredKey: stringLiteralValue(args[0], `RPC_CONTRACT_REGISTRY.${key} declared key`),
    facade: stringLiteralValue(args[1], `RPC_CONTRACT_REGISTRY.${key} facade`),
    level: stringLiteralValue(args[2], `RPC_CONTRACT_REGISTRY.${key} level`),
    responseValidator: objectStringPropertyValue(options, 'responseValidator'),
    responsePassthroughReason: objectStringPropertyValue(options, 'responsePassthroughReason'),
  }
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

function parseFrontendAst(source, { errorRecovery = false } = {}) {
  return parseJavaScriptSource(source, {
    sourceType: 'module',
    plugins: ['jsx', 'typescript'],
    errorRecovery,
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

function objectStringPropertyValue(objectExpression, propertyName) {
  if (!objectExpression) return ''
  const property = objectPropertiesOnly(objectExpression, 'contract options')
    .find((candidate) => propertyKeyName(candidate) === propertyName)
  if (!property) return ''
  return stringLiteralValue(property.value, propertyName)
}

function collectFrontendResponseValidatorKeys(frontendSources) {
  const keys = new Set()
  for (const source of frontendSources) {
    for (const body of extractFrontendResponseValidatorBodies(source)) {
      const keyPattern = /\[(?:RPC_METHODS|methods)\.([A-Z0-9_]+)\]\s*:/g
      let keyMatch
      while ((keyMatch = keyPattern.exec(body)) !== null) {
        keys.add(keyMatch[1])
      }
    }
  }
  return keys
}

function extractFrontendResponseValidatorBodies(source) {
  const bodies = []
  const literalMatch = source.match(/const\s+BACKEND_RESPONSE_VALIDATORS\s*=\s*Object\.freeze\(\{([\s\S]*?)\n\}\)/)
  if (literalMatch) {
    bodies.push(literalMatch[1])
  }
  const factoryPattern = /return\s+Object\.freeze\(\{([\s\S]*?)\n\s*\}\)/g
  let factoryMatch
  while ((factoryMatch = factoryPattern.exec(source)) !== null) {
    bodies.push(factoryMatch[1])
  }
  return bodies
}

async function collectGoPayloadKeys(repoRoot) {
  const out = new Map()
  for (const [method, locators] of GO_PAYLOAD_STRUCTS.entries()) {
    const keys = []
    for (const locator of locators) {
      const [filePath, symbol] = locator.split(':')
      const source = await readFile(join(repoRoot, filePath), 'utf8')
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

async function collectHardcodedPayloadGuardFindings(repoRoot, frontendSource) {
  const inspectedFiles = uniqueSorted([
    ...new Set([...GO_PAYLOAD_STRUCTS.values()].flat().map((locator) => locator.split(':')[0])),
  ])
  const goSources = new Map()
  for (const filePath of inspectedFiles) {
    goSources.set(filePath, await readFile(join(repoRoot, filePath), 'utf8'))
  }
  return collectHardcodedPayloadGuardFindingsFromSources({ frontendSource, goSources })
}

export function collectHardcodedPayloadGuardFindingsFromSources({ frontendSource = '', goSources = new Map() } = {}) {
  const findings = []
  if (frontendSource.includes('RPC_ALLOWED_PAYLOAD_KEYS')) {
    findings.push(`${FRONTEND_FACADE_PATH}:RPC_ALLOWED_PAYLOAD_KEYS`)
  }
  const frontendSetPattern = /^\s*const\s+([A-Z0-9_]+_ALLOWED_KEYS)\s*=\s*new Set\(\[/gm
  let frontendSetMatch
  while ((frontendSetMatch = frontendSetPattern.exec(frontendSource)) !== null) {
    findings.push(`${FRONTEND_FACADE_PATH}:${frontendSetMatch[1]}`)
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

async function collectGoRpcHandlers(repoRoot) {
  const constants = await collectGoRpcConstants(repoRoot)
  const goFiles = []
  for (const root of GO_HANDLER_ROOTS) {
    goFiles.push(...await collectGoFiles(join(repoRoot, root)))
  }

  const sources = await Promise.all(goFiles.map(async (filePath) => ({
    filePath,
    source: await readFile(filePath, 'utf8'),
  })))

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

async function collectGoRpcConstants(repoRoot) {
  const source = await readFile(join(repoRoot, GO_RPC_CONSTANTS_PATH), 'utf8')
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
  const files = []
  for (const entry of entries) {
    const fullPath = join(root, entry.name)
    if (entry.isDirectory()) {
      files.push(...await collectGoFiles(fullPath))
      continue
    }
    if (entry.name.endsWith('.go') && !entry.name.endsWith('_test.go')) {
      files.push(fullPath)
    }
  }
  return files
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
  ].filter(([, values]) => values.length > 0)

  if (failures.length > 0) {
    for (const [title, values] of failures) {
      console.error(`\n${title}:`)
      console.error(JSON.stringify(values, null, 2))
    }
    process.exit(1)
  }
}
