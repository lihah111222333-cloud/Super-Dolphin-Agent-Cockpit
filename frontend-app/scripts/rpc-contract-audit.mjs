import { readFile, readdir } from 'node:fs/promises'
import { dirname, join, relative, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'

const SCRIPT_DIR = dirname(fileURLToPath(import.meta.url))
const DEFAULT_FRONTEND_ROOT = resolve(SCRIPT_DIR, '..')
const DEFAULT_REPO_ROOT = resolve(DEFAULT_FRONTEND_ROOT, '..')

const RPC_METHODS_PATH = 'frontend-app/src/shared/api/backendApi.js'
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
  const frontendSource = await readFile(join(repoRoot, RPC_METHODS_PATH), 'utf8')
  const rpcMethods = parseRpcMethods(
    frontendSource,
  )
  const methodsByKey = new Map(rpcMethods.map((entry) => [entry.key, entry]))
  const registryEntries = parseContractMatrix(
    await readFile(join(repoRoot, RPC_MATRIX_PATH), 'utf8'),
  ).map((entry) => ({
    ...entry,
    method: methodsByKey.get(entry.key)?.method ?? '',
  }))
  const backendHandlers = await collectGoRpcHandlers(repoRoot)
  const goPayloadKeysByMethod = await collectGoPayloadKeys(repoRoot)
  const frontendPayloadKeysByMethod = collectFrontendPayloadKeysFromSource(frontendSource)
  const hardcodedPayloadGuardFindings = await collectHardcodedPayloadGuardFindings(repoRoot, frontendSource)

  const registryByKey = new Map(registryEntries.map((entry) => [entry.key, entry]))
  const handlerMethods = new Set(backendHandlers.map((entry) => entry.method))

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
  ].join('\n')
}

function parseRpcMethods(source) {
  const objectMatch = source.match(/export const RPC_METHODS = Object\.freeze\(\{([\s\S]*?)\n\}\)/)
  if (!objectMatch) {
    throw new Error('RPC_METHODS object was not found in backendApi.js')
  }

  const methods = []
  const entryPattern = /^\s*([A-Z0-9_]+):\s*'([^']+)',/gm
  let match
  while ((match = entryPattern.exec(objectMatch[1])) !== null) {
    methods.push({
      key: match[1],
      method: match[2],
    })
  }
  return methods
}

function parseContractMatrix(source) {
  const entries = []
  const entryPattern = /^\s*([A-Z0-9_]+):\s*contract\('([A-Z0-9_]+)',\s*'([^']+)',\s*'([^']+)'/gm
  let match
  while ((match = entryPattern.exec(source)) !== null) {
    entries.push({
      key: match[1],
      declaredKey: match[2],
      facade: match[3],
      level: match[4],
    })
  }

  const badKey = entries.find((entry) => entry.key !== entry.declaredKey)
  if (badKey) {
    throw new Error(`Contract key mismatch: ${badKey.key} declares ${badKey.declaredKey}`)
  }

  return entries
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
    findings.push(`${RPC_METHODS_PATH}:RPC_ALLOWED_PAYLOAD_KEYS`)
  }
  const frontendSetPattern = /^\s*const\s+([A-Z0-9_]+_ALLOWED_KEYS)\s*=\s*new Set\(\[/gm
  let frontendSetMatch
  while ((frontendSetMatch = frontendSetPattern.exec(frontendSource)) !== null) {
    findings.push(`${RPC_METHODS_PATH}:${frontendSetMatch[1]}`)
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
  const out = new Map()
  for (const [method, functionName] of FRONTEND_PAYLOAD_BUILDERS.entries()) {
    const functionSource = extractFunctionSource(source, functionName)
    if (!functionSource) {
      out.set(method, [])
      continue
    }
    out.set(method, extractConsumedPayloadKeys(functionSource))
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

function extractFunctionSource(source, functionName) {
  const pattern = new RegExp(`^\\s*function\\s+${escapeRegExp(functionName)}\\s*\\(`, 'm')
  const match = pattern.exec(source)
  if (!match) return ''
  const start = match.index
  const rest = source.slice(start + 1)
  const next = rest.search(/\n\s*function\s+[A-Za-z0-9_]+\s*\(/)
  if (next === -1) {
    return source.slice(start)
  }
  return source.slice(start, start + 1 + next)
}

function extractConsumedPayloadKeys(functionSource) {
  const keys = []
  for (const args of findCallArguments(functionSource, 'takePayloadField')) {
    const parts = splitTopLevelArguments(args)
    if (parts[0]?.trim() !== 'unused') continue
    const key = parseStringLiteral(parts[1] ?? '')
    if (key) keys.push(key)
  }

  for (const args of findCallArguments(functionSource, 'takePayloadFields')) {
    const parts = splitTopLevelArguments(args)
    if (parts[0]?.trim() !== 'unused') continue
    keys.push(...extractStringLiterals(parts[1] ?? ''))
  }
  return uniqueSorted(keys)
}

function findCallArguments(source, callee) {
  const calls = []
  for (let index = 0; index < source.length; index += 1) {
    const skipped = skipJSSyntaxTrivia(source, index)
    if (skipped !== index) {
      index = skipped - 1
      continue
    }
    if (!source.startsWith(callee, index) || isIdentifierChar(source[index - 1]) || isIdentifierChar(source[index + callee.length])) {
      continue
    }
    let cursor = skipWhitespace(source, index + callee.length)
    if (source[cursor] !== '(') {
      continue
    }
    const call = readBalancedParens(source, cursor)
    if (!call) {
      continue
    }
    calls.push(call.body)
    index = call.end - 1
  }
  return calls
}

function splitTopLevelArguments(source) {
  const args = []
  let start = 0
  let depth = 0
  for (let index = 0; index < source.length; index += 1) {
    const skipped = skipJSSyntaxTrivia(source, index)
    if (skipped !== index) {
      index = skipped - 1
      continue
    }
    const char = source[index]
    if (char === '(' || char === '[' || char === '{') depth += 1
    if (char === ')' || char === ']' || char === '}') depth -= 1
    if (char === ',' && depth === 0) {
      args.push(source.slice(start, index))
      start = index + 1
    }
  }
  args.push(source.slice(start))
  return args
}

function readBalancedParens(source, start) {
  let depth = 0
  for (let index = start; index < source.length; index += 1) {
    const skipped = skipJSSyntaxTrivia(source, index)
    if (skipped !== index) {
      index = skipped - 1
      continue
    }
    const char = source[index]
    if (char === '(') depth += 1
    if (char !== ')') continue
    depth -= 1
    if (depth === 0) {
      return {
        body: source.slice(start + 1, index),
        end: index + 1,
      }
    }
  }
  return null
}

function parseStringLiteral(source) {
  const trimmed = source.trim()
  const literal = readStringLiteral(trimmed, 0)
  if (!literal || literal.end !== trimmed.length) {
    return ''
  }
  return literal.value
}

function extractStringLiterals(source) {
  const out = []
  for (let index = 0; index < source.length; index += 1) {
    const skipped = skipJSSyntaxComment(source, index)
    if (skipped !== index) {
      index = skipped - 1
      continue
    }
    const literal = readStringLiteral(source, index)
    if (!literal) {
      continue
    }
    out.push(literal.value)
    index = literal.end - 1
  }
  return out
}

function skipJSSyntaxTrivia(source, index) {
  const commentEnd = skipJSSyntaxComment(source, index)
  if (commentEnd !== index) return commentEnd
  const literal = readStringLiteral(source, index)
  return literal?.end ?? index
}

function skipJSSyntaxComment(source, index) {
  if (source.startsWith('//', index)) {
    const end = source.indexOf('\n', index + 2)
    return end === -1 ? source.length : end
  }
  if (source.startsWith('/*', index)) {
    const end = source.indexOf('*/', index + 2)
    return end === -1 ? source.length : end + 2
  }
  return index
}

function readStringLiteral(source, start) {
  const quote = source[start]
  if (quote !== '"' && quote !== '\'' && quote !== '`') {
    return null
  }
  let value = ''
  for (let index = start + 1; index < source.length; index += 1) {
    const char = source[index]
    if (char === '\\') {
      if (index + 1 < source.length) {
        value += source[index + 1]
        index += 1
      }
      continue
    }
    if (char === quote) {
      return { value, end: index + 1 }
    }
    value += char
  }
  return null
}

function skipWhitespace(source, index) {
  let cursor = index
  while (/\s/.test(source[cursor] ?? '')) {
    cursor += 1
  }
  return cursor
}

function isIdentifierChar(char) {
  return typeof char === 'string' && /[A-Za-z0-9_$]/.test(char)
}

function escapeRegExp(value) {
  return value.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')
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
  ].filter(([, values]) => values.length > 0)

  if (failures.length > 0) {
    for (const [title, values] of failures) {
      console.error(`\n${title}:`)
      console.error(JSON.stringify(values, null, 2))
    }
    process.exit(1)
  }
}
