import * as audit from "../rpc-contract-audit.mjs";

function uniqueSorted(values) {
  return [...new Set(values)].sort((a, b) => a.localeCompare(b));
}

async function collectGoRpcHandlers(auditContext) {
  const { repoRoot } = auditContext;
  const constants = await collectGoRpcConstants(auditContext);
  const goFiles = [];
  for (const root of audit.GO_HANDLER_ROOTS) {
    goFiles.push(...(await collectGoFiles(audit.join(repoRoot, root))));
  }

  const sources = [];
  for (const filePath of goFiles) {
    const auditPath = audit.relative(repoRoot, filePath).replaceAll("\\", "/");
    const source = auditContext.sourcePromiseByPath.has(auditPath)
      ? await audit.readAuditSource(auditContext, auditPath)
      : audit.readAuditSourceSync(auditContext, auditPath);
    sources.push({ filePath, source });
  }

  const handlers = [];
  for (const { filePath, source } of sources) {
    const sourceHandlers = [
      ...parseLiteralHandlerRegistrations(source, filePath, repoRoot),
      ...parseConstantHandlerRegistrations(source, filePath, repoRoot, constants),
    ];
    handlers.push(...sourceHandlers);
  }
  return uniqueHandlers(handlers);
}

async function collectGoRpcConstants(auditContext) {
  const source = await audit.readAuditSource(auditContext, audit.GO_RPC_CONSTANTS_PATH);
  const constants = new Map();
  const constPattern = /^\s*([A-Za-z0-9_]+)\s*=\s*"([^"]+\/[^"]+)"/gm;
  let match;
  while ((match = constPattern.exec(source)) !== null) {
    constants.set(match[1], match[2]);
  }
  return constants;
}

async function collectGoFiles(root) {
  const entries = await audit.readdir(root, { withFileTypes: true });
  const groups = await Promise.all(
    entries.map(async (entry) => {
      const fullPath = audit.join(root, entry.name);
      if (entry.isDirectory()) return collectGoFiles(fullPath);
      return entry.name.endsWith(".go") && !entry.name.endsWith("_test.go") ? [fullPath] : [];
    }),
  );
  return groups.flat();
}

function parseLiteralHandlerRegistrations(source, filePath, repoRoot) {
  const handlerNames = audit.GO_HANDLER_CALLS.join("|");
  const registrations = [];
  const patterns = [
    /"([^"\r\n]+\/[^"\r\n]+)"\s*:\s*(?:[a-zA-Z0-9_]+\.)*[a-zA-Z_][a-zA-Z0-9_]*(?:\s*\(|\s*,)/g,
    new RegExp(`"([^"]+/[^"]+)"\\s*:\\s*(?:[a-zA-Z0-9_]+\\.)?(?:${handlerNames})\\b`, "g"),
    new RegExp(
      `\\[\\s*"([^"]+/[^"]+)"\\s*\\]\\s*=\\s*(?:[a-zA-Z0-9_]+\\.)?(?:${handlerNames})\\b`,
      "g",
    ),
  ];

  for (const pattern of patterns) {
    let match;
    while ((match = pattern.exec(source)) !== null) {
      registrations.push(handlerEntry(match[1], filePath, repoRoot));
    }
  }
  return registrations;
}

function parseConstantHandlerRegistrations(source, filePath, repoRoot, constants) {
  const handlerNames = audit.GO_HANDLER_CALLS.join("|");
  const registrations = [];
  const patterns = [
    /contract\.([A-Za-z0-9_]+)\s*:/g,
    new RegExp(`(?:[a-zA-Z0-9_]+\\.)?(?:${handlerNames})\\(\\s*contract\\.([A-Za-z0-9_]+)`, "g"),
  ];

  for (const pattern of patterns) {
    let match;
    while ((match = pattern.exec(source)) !== null) {
      const method = constants.get(match[1]);
      if (method) {
        registrations.push(handlerEntry(method, filePath, repoRoot));
      }
    }
  }
  return registrations;
}

function handlerEntry(method, filePath, repoRoot) {
  return {
    method,
    file: audit.relative(repoRoot, filePath).replaceAll("\\", "/"),
  };
}

function uniqueHandlers(handlers) {
  const byMethod = new Map();
  for (const handler of handlers) {
    if (!byMethod.has(handler.method)) {
      byMethod.set(handler.method, handler);
    }
  }
  return [...byMethod.values()].sort((a, b) => a.method.localeCompare(b.method));
}

export {
  uniqueSorted,
  collectGoRpcHandlers,
  collectGoRpcConstants,
  collectGoFiles,
  parseLiteralHandlerRegistrations,
  parseConstantHandlerRegistrations,
  handlerEntry,
  uniqueHandlers,
};
