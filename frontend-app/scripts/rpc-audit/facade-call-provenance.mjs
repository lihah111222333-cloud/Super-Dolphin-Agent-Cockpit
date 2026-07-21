import * as audit from "../rpc-contract-audit.mjs";

function moduleSpecifierResolvesTo(fromPath, specifier, targetPath) {
  return moduleSpecifierResolvedPath(fromPath, specifier) === targetPath;
}

function moduleSpecifierResolvedPath(fromPath, specifier) {
  if (typeof specifier !== "string" || !specifier.startsWith(".")) return false;
  return audit.normalize(audit.join(audit.dirname(fromPath), specifier)).replaceAll("\\", "/");
}

async function findFacadeCalls(
  auditContext,
  ast,
  symbolNode,
  filePath,
  entry,
  backendFacadeRpcKeys,
) {
  const bindings = audit.collectFacadeCallBindings(ast, filePath, entry, backendFacadeRpcKeys);
  const directLocator = audit.DIRECT_FACADE_RPC_LOCATORS.get(entry.key);
  const exactDirectConsumer =
    directLocator &&
    filePath === directLocator.implementationPath &&
    entry.responsePolicy?.consumer?.symbol === directLocator.facade;
  const candidates = [];
  audit.walkAstWithAncestors(symbolNode, (node, ancestors) => {
    if (node.type === "CallExpression") candidates.push({ node, ancestors });
  });
  const calls = [];
  for (const call of candidates) {
    let provenance = null;
    if (exactDirectConsumer && directFacadeRuntimeCallMatches(entry, filePath, call.node)) {
      provenance = directFacadeCallProvenance(call, filePath);
    } else if (
      !exactDirectConsumer &&
      audit.facadeCallMatchesBindings(call.node, bindings, call.ancestors)
    ) {
      provenance = directFacadeCallProvenance(call, filePath);
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
      );
    }
    if (provenance) {
      const effectiveCall = await promoteTransparentPromiseWrapperCall(
        auditContext,
        ast,
        filePath,
        call,
      );
      calls.push({ ...effectiveCall, provenance });
    }
  }
  return calls;
}

function directFacadeCallProvenance(call, filePath) {
  return {
    facadeCall: call.node,
    facadeAncestors: call.ancestors,
    layers: [{ path: filePath, node: call.node, ancestors: call.ancestors }],
  };
}

async function promoteTransparentPromiseWrapperCall(auditContext, ast, filePath, call) {
  const wrapperCall = call.ancestors.at(-1);
  if (wrapperCall?.type !== "CallExpression") return call;
  const argumentIndex = wrapperCall.arguments.indexOf(call.node);
  if (argumentIndex < 0) return call;
  const wrapperTarget = audit.resolveImportedCallTarget(
    ast,
    filePath,
    wrapperCall,
    call.ancestors.slice(0, -1),
  );
  if (!wrapperTarget) return call;
  const wrapperAst = await audit.readAuditAst(auditContext, wrapperTarget.path);
  const wrapperNode = audit.findExportedSymbolPath(wrapperAst, wrapperTarget.symbol);
  if (!transparentPromiseWrapperAt(wrapperNode, argumentIndex)) return call;
  return { node: wrapperCall, ancestors: call.ancestors.slice(0, -1) };
}

function transparentPromiseWrapperAt(node, argumentIndex) {
  const parameter = node?.params?.[argumentIndex];
  if (
    parameter?.type !== "Identifier" ||
    node.body?.type !== "BlockStatement" ||
    node.body.body.length !== 1
  ) {
    return false;
  }
  const statement = node.body.body[0];
  if (statement.type !== "ReturnStatement" || statement.argument?.type !== "CallExpression")
    return false;
  let references = 0;
  audit.traverseAst(statement.argument, (candidate) => {
    if (candidate.type === "Identifier" && candidate.name === parameter.name) references += 1;
  });
  return (
    references === 1 &&
    statement.argument.arguments.some(
      (argument) => argument.type === "Identifier" && argument.name === parameter.name,
    )
  );
}

function directFacadeRuntimeCallMatches(entry, filePath, call) {
  const locator = audit.DIRECT_FACADE_RPC_LOCATORS.get(entry.key);
  if (
    !locator ||
    filePath !== locator.implementationPath ||
    entry.responsePolicy?.consumer?.symbol !== locator.facade
  )
    return false;
  return call.arguments.some(
    (argument) => argument.type === "StringLiteral" && argument.value === locator.method,
  );
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
  const target = audit.resolveImportedCallTarget(ast, filePath, call, ancestors);
  if (!target) return null;
  const visitKey = `${target.path}#${target.symbol}`;
  if (visited.has(visitKey)) return null;
  const nextVisited = new Set(visited);
  nextVisited.add(visitKey);
  const targetAst = await audit.readAuditAst(auditContext, target.path);
  const targetNode = audit.findExportedSymbolPath(targetAst, target.symbol);
  if (!targetNode) return null;
  const bindings = audit.collectFacadeCallBindings(
    targetAst,
    target.path,
    entry,
    backendFacadeRpcKeys,
  );
  const nestedCalls = [];
  audit.walkAstWithAncestors(targetNode, (node, nestedAncestors) => {
    if (node.type === "CallExpression") nestedCalls.push({ node, ancestors: nestedAncestors });
  });
  const resolved = [];
  for (const nestedCall of nestedCalls) {
    let nestedProvenance = null;
    if (
      audit.facadeCallMatchesBindings(nestedCall.node, bindings, nestedCall.ancestors) ||
      directFacadeRuntimeCallMatches(entry, target.path, nestedCall.node)
    ) {
      nestedProvenance = directFacadeCallProvenance(nestedCall, target.path);
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
      );
    }
    if (nestedProvenance) resolved.push({ call: nestedCall, provenance: nestedProvenance });
  }
  if (resolved.length !== 1) return null;
  const [match] = resolved;
  if (!audit.wrapperTransparentlyReturnsCall(targetNode, match.call.node, match.call.ancestors))
    return null;
  return {
    facadeCall: match.provenance.facadeCall,
    facadeAncestors: match.provenance.facadeAncestors,
    layers: [
      { path: target.path, symbol: target.symbol, node: targetNode, call: match.call.node },
      ...match.provenance.layers,
    ],
  };
}

export {
  moduleSpecifierResolvesTo,
  moduleSpecifierResolvedPath,
  findFacadeCalls,
  directFacadeCallProvenance,
  promoteTransparentPromiseWrapperCall,
  transparentPromiseWrapperAt,
  directFacadeRuntimeCallMatches,
  resolveImportedWrapperProvenance,
};
