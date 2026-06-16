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

const GO_HANDLER_CALLS = [
  'StrictHandler',
  'LoggedStrictHandler',
  'ThreadHandler',
  'CapabilityThreadHandler',
]

export async function auditRpcContracts({ repoRoot = DEFAULT_REPO_ROOT } = {}) {
  const rpcMethods = parseRpcMethods(
    await readFile(join(repoRoot, RPC_METHODS_PATH), 'utf8'),
  )
  const methodsByKey = new Map(rpcMethods.map((entry) => [entry.key, entry]))
  const registryEntries = parseContractMatrix(
    await readFile(join(repoRoot, RPC_MATRIX_PATH), 'utf8'),
  ).map((entry) => ({
    ...entry,
    method: methodsByKey.get(entry.key)?.method ?? '',
  }))
  const backendHandlers = await collectGoRpcHandlers(repoRoot)

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

  return {
    rpcMethods,
    registryEntries,
    backendHandlers,
    missingRegistryKeys,
    registryWithoutRpcMethods,
    mismatchedRegistryMethods,
    p0MissingBackendHandlers,
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
  ].filter(([, values]) => values.length > 0)

  if (failures.length > 0) {
    for (const [title, values] of failures) {
      console.error(`\n${title}:`)
      console.error(JSON.stringify(values, null, 2))
    }
    process.exit(1)
  }
}
