import * as audit from "../rpc-contract-audit.mjs";

function wrapperTransparentlyReturnsCall(wrapperNode, call, ancestors) {
  if (!wrapperNode || !isFunctionNode(wrapperNode)) return false;
  if (wrapperNode.body.type !== "BlockStatement") {
    return (
      wrapperNode.body === call ||
      (wrapperNode.body.type === "AwaitExpression" && wrapperNode.body.argument === call)
    );
  }
  const parent = ancestors.at(-1);
  const callValue = parent?.type === "AwaitExpression" && parent.argument === call ? parent : call;
  const valueParent = parent?.type === "AwaitExpression" ? ancestors.at(-2) : parent;
  if (valueParent?.type === "ReturnStatement" && valueParent.argument === callValue) return true;
  if (
    valueParent?.type !== "VariableDeclarator" ||
    valueParent.init !== callValue ||
    valueParent.id.type !== "Identifier"
  )
    return false;
  const declaration = ancestors.at(parent?.type === "AwaitExpression" ? -3 : -2);
  if (declaration?.type !== "VariableDeclaration" || declaration.declarations.length !== 1)
    return false;
  const declarationIndex = wrapperNode.body.body.indexOf(declaration);
  if (declarationIndex < 0 || declarationIndex !== wrapperNode.body.body.length - 2) return false;
  const returnStatement = wrapperNode.body.body.at(-1);
  if (
    returnStatement.type !== "ReturnStatement" ||
    returnStatement.argument?.type !== "Identifier" ||
    returnStatement.argument.name !== valueParent.id.name
  )
    return false;
  let references = 0;
  for (const statement of wrapperNode.body.body.slice(declarationIndex)) {
    audit.traverseAst(statement, (candidate) => {
      if (candidate.type === "Identifier" && candidate.name === valueParent.id.name)
        references += 1;
    });
  }
  return references === 2;
}

function resolveImportedCallTarget(ast, filePath, call, ancestors) {
  if (call.callee.type !== "Identifier") return null;
  const localName = call.callee.name;
  if (bindingShadowsNameAt(ancestors, localName)) return null;
  for (const statement of ast.program.body) {
    if (statement.type === "ImportDeclaration") {
      for (const specifier of statement.specifiers) {
        if (specifier.local.name !== localName) continue;
        const symbol =
          specifier.type === "ImportSpecifier"
            ? audit.moduleExportName(specifier.imported)
            : specifier.type === "ImportDefaultSpecifier"
              ? "default"
              : "";
        const path = audit.moduleSpecifierResolvedPath(filePath, statement.source.value);
        if (symbol && path) return { path, symbol };
      }
      continue;
    }
    const declaration =
      statement.type === "ExportNamedDeclaration" ? statement.declaration : statement;
    if (declaration?.type !== "VariableDeclaration") continue;
    for (const item of declaration.declarations) {
      if (item.id.type !== "ObjectPattern" || item.init?.type !== "Identifier") continue;
      const property = item.id.properties.find(
        (candidate) =>
          candidate.type === "ObjectProperty" &&
          candidate.value.type === "Identifier" &&
          candidate.value.name === localName,
      );
      if (!property) continue;
      const imported = findImportedBinding(ast, filePath, item.init.name);
      const member = audit.staticPropertyKeyName(property);
      if (imported && member)
        return { path: imported.path, symbol: `${imported.symbol}.${member}` };
    }
  }
  return null;
}

function findImportedBinding(ast, filePath, localName) {
  for (const statement of ast.program.body) {
    if (statement.type !== "ImportDeclaration") continue;
    for (const specifier of statement.specifiers) {
      if (specifier.local.name !== localName) continue;
      const symbol =
        specifier.type === "ImportSpecifier"
          ? audit.moduleExportName(specifier.imported)
          : specifier.type === "ImportDefaultSpecifier"
            ? "default"
            : "";
      const path = audit.moduleSpecifierResolvedPath(filePath, statement.source.value);
      if (symbol && path) return { path, symbol };
    }
  }
  return null;
}

function findExportedSymbolPath(ast, symbolPath) {
  const [root, ...members] = symbolPath.split(".");
  let current = audit.findProductionSymbol(ast, root);
  for (const member of members) {
    if (current?.type === "CallExpression" && current.arguments.length === 1)
      current = current.arguments[0];
    if (current?.type !== "ObjectExpression") return null;
    const property = current.properties.find(
      (candidate) => audit.staticPropertyKeyName(candidate) === member,
    );
    if (!property) return null;
    current = property.type === "ObjectMethod" ? property : property.value;
  }
  return current;
}

function collectFacadeCallBindings(
  ast,
  filePath,
  entry,
  backendFacadeRpcKeys,
  facadeExportsByPath = new Map([[audit.RPC_FACADE_PATH, new Set([entry.facade.split(".")[0]])]]),
) {
  const facadeParts = entry.facade.split(".");
  const identifierAliases = new Set();
  const namespaceAliases = new Set();
  const namespaceMemberNames = new Set();
  const namespaceMemberPaths = new Map();
  const addNamespaceMemberPath = (localName, memberPath) => {
    if (!namespaceMemberPaths.has(localName)) namespaceMemberPaths.set(localName, new Set());
    namespaceMemberPaths.get(localName).add(memberPath);
  };
  if (facadeParts.length === 1) {
    const facade = facadeParts[0];
    if (backendFacadeRpcKeys.get(facade) !== entry.key) {
      return { identifierAliases, namespaceAliases, memberName: facade };
    }
    for (const statement of ast.program.body) {
      const importedNames =
        statement.type === "ImportDeclaration"
          ? facadeExportsByPath.get(
              audit.moduleSpecifierResolvedPath(filePath, statement.source.value),
            )
          : null;
      if (statement.type !== "ImportDeclaration" || !importedNames) {
        continue;
      }
      for (const specifier of statement.specifiers) {
        const importedName =
          specifier.type === "ImportSpecifier"
            ? audit.moduleExportName(specifier.imported)
            : specifier.type === "ImportDefaultSpecifier"
              ? "default"
              : "";
        if (importedName && importedNames.has(importedName)) {
          identifierAliases.add(specifier.local.name);
        }
        if (importedName) {
          const namespacePrefix = `${importedName}.`;
          for (const name of importedNames) {
            if (!name.startsWith(namespacePrefix)) continue;
            namespaceAliases.add(specifier.local.name);
            const memberPath = name.slice(namespacePrefix.length);
            namespaceMemberNames.add(memberPath);
            addNamespaceMemberPath(specifier.local.name, memberPath);
          }
        }
        if (specifier.type === "ImportNamespaceSpecifier") {
          namespaceAliases.add(specifier.local.name);
          for (const name of importedNames) {
            namespaceMemberNames.add(name);
            addNamespaceMemberPath(specifier.local.name, name);
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
    };
  }
  if (facadeParts.length !== 2 || !audit.SERVICE_FACADE_LOCATORS.has(entry.key)) {
    return { identifierAliases, namespaceAliases, memberName: "" };
  }
  const [serviceName, memberName] = facadeParts;
  const servicePath = audit.SERVICE_FACADE_LOCATORS.get(entry.key);
  for (const statement of ast.program.body) {
    if (
      statement.type !== "ImportDeclaration" ||
      !audit.moduleSpecifierResolvesTo(filePath, statement.source.value, servicePath)
    ) {
      continue;
    }
    for (const specifier of statement.specifiers) {
      if (
        specifier.type === "ImportSpecifier" &&
        audit.moduleExportName(specifier.imported) === serviceName
      ) {
        namespaceAliases.add(specifier.local.name);
      }
    }
  }
  return { identifierAliases, namespaceAliases, memberName };
}

function symbolBindsName(symbolNode, name) {
  if (!symbolNode) return false;
  let binds = false;
  audit.traverseAst(symbolNode.body ?? symbolNode, (node) => {
    if (
      (node.type === "VariableDeclarator" && audit.bindingPatternContainsName(node.id, name)) ||
      ((node.type === "FunctionDeclaration" || node.type === "ClassDeclaration") &&
        node.id?.name === name)
    ) {
      binds = true;
    }
  });
  return binds;
}

function facadeCallMatchesBindings(call, bindings, ancestors = []) {
  if (call.callee.type === "Identifier") {
    return (
      bindings.identifierAliases.has(call.callee.name) &&
      !bindingShadowsNameAt(ancestors, call.callee.name)
    );
  }
  return (
    call.callee.type === "MemberExpression" &&
    !call.callee.computed &&
    call.callee.object.type === "Identifier" &&
    call.callee.property.type === "Identifier" &&
    (call.callee.property.name === bindings.memberName ||
      bindings.namespaceMemberNames?.has(call.callee.property.name)) &&
    bindings.namespaceAliases.has(call.callee.object.name) &&
    !bindingShadowsNameAt(ancestors, call.callee.object.name)
  );
}

function bindingShadowsNameAt(ancestors, name) {
  for (let index = ancestors.length - 1; index >= 0; index -= 1) {
    const scope = ancestors[index];
    if (scope.type === "CatchClause" && audit.bindingPatternContainsName(scope.param, name))
      return true;
    if (
      isFunctionNode(scope) &&
      scope.params.some((parameter) => audit.bindingPatternContainsName(parameter, name))
    )
      return true;
    if (scope.type === "BlockStatement" && blockDirectlyBindsName(scope, name)) return true;
  }
  return false;
}

function isFunctionNode(node) {
  return (
    node.type === "FunctionDeclaration" ||
    node.type === "FunctionExpression" ||
    node.type === "ArrowFunctionExpression" ||
    node.type === "ObjectMethod" ||
    node.type === "ClassMethod"
  );
}

function blockDirectlyBindsName(block, name) {
  return block.body.some((statement) => {
    const declaration =
      statement.type === "ExportNamedDeclaration" ? statement.declaration : statement;
    if (declaration?.type === "VariableDeclaration") {
      return declaration.declarations.some((item) =>
        audit.bindingPatternContainsName(item.id, name),
      );
    }
    return (
      (declaration?.type === "FunctionDeclaration" || declaration?.type === "ClassDeclaration") &&
      declaration.id?.name === name
    );
  });
}

function walkAstWithAncestors(node, visit, ancestors = []) {
  if (!node || typeof node.type !== "string") return;
  visit(node, ancestors);
  const nextAncestors = [...ancestors, node];
  for (const value of Object.values(node)) {
    if (!value) continue;
    if (Array.isArray(value)) {
      for (const child of value) walkAstWithAncestors(child, visit, nextAncestors);
    } else if (typeof value.type === "string") {
      walkAstWithAncestors(value, visit, nextAncestors);
    }
  }
}

function isIgnoredCallResult(ancestors) {
  const parent = ancestors.at(-1);
  if (parent?.type === "ExpressionStatement") return true;
  return parent?.type === "AwaitExpression" && ancestors.at(-2)?.type === "ExpressionStatement";
}

function isExactTurnInterruptPolicy(entry) {
  const policy = entry.responsePolicy;
  return (
    entry.key === "TURN_INTERRUPT" &&
    entry.facade === "interruptTurn" &&
    policy?.kind === "result-handled" &&
    policy.consumer.path === audit.TURN_INTERRUPT_RUNTIME_PATH &&
    policy.consumer.symbol === "attachActiveThreadRpcRuntime" &&
    policy.handler.path === audit.TURN_INTERRUPT_RUNTIME_PATH &&
    policy.handler.symbol === "notifyThreadActionFailure" &&
    policy.regressionTest.path === audit.TURN_INTERRUPT_REGRESSION_PATH &&
    policy.regressionTest.symbol === "reports interrupt ok:false as warning without showing success"
  );
}

function hasExactTurnInterruptTimeoutHandler(handlerSymbol, ast) {
  if (handlerSymbol?.body?.type !== "BlockStatement") return false;
  const messageBinding = ast?.program?.body?.find(
    (statement) =>
      statement.type === "VariableDeclaration" &&
      statement.kind === "const" &&
      statement.declarations.length === 1 &&
      statement.declarations[0].id.type === "Identifier" &&
      statement.declarations[0].id.name === "INTERRUPT_UNCONFIRMED_MESSAGE",
  )?.declarations[0].init;
  if (
    messageBinding?.type !== "StringLiteral" ||
    messageBinding.value !== "停止未确认，任务可能仍在运行"
  )
    return false;
  return handlerSymbol.body.body.some(
    (outer) =>
      outer.type === "IfStatement" &&
      isExactTurnInterruptTimeoutPredicate(outer.test) &&
      outer.consequent.type === "BlockStatement" &&
      outer.consequent.body.some((branch) => isExactInterruptTimeoutBranch(branch)),
  );
}

function isExactTurnInterruptTimeoutPredicate(node) {
  if (node?.type !== "LogicalExpression" || node.operator !== "&&") return false;
  const [actionCheck, okCheck] = [node.left, node.right];
  return (
    actionCheck.type === "BinaryExpression" &&
    actionCheck.operator === "===" &&
    actionCheck.left.type === "Identifier" &&
    actionCheck.left.name === "action" &&
    actionCheck.right.type === "StringLiteral" &&
    actionCheck.right.value === "thread.interrupt" &&
    okCheck.type === "BinaryExpression" &&
    okCheck.operator === "===" &&
    isExactResultMember(okCheck.left, "ok") &&
    okCheck.right.type === "BooleanLiteral" &&
    okCheck.right.value === false
  );
}

function isExactResultMember(node, property) {
  return (
    (node?.type === "MemberExpression" || node?.type === "OptionalMemberExpression") &&
    !node.computed &&
    node.object.type === "Identifier" &&
    node.object.name === "result" &&
    node.property.type === "Identifier" &&
    node.property.name === property
  );
}

function isExactInterruptTimeoutBranch(branch) {
  if (
    branch?.type !== "IfStatement" ||
    branch.test.type !== "BinaryExpression" ||
    branch.test.operator !== "===" ||
    !isExactResultMember(branch.test.left, "mode") ||
    branch.test.right.type !== "StringLiteral" ||
    branch.test.right.value !== "interrupt_timeout" ||
    branch.consequent.type !== "BlockStatement" ||
    branch.consequent.body.length !== 3
  )
    return false;
  const [notice, warning, returned] = branch.consequent.body;
  const noticeCall = notice?.type === "ExpressionStatement" ? notice.expression : null;
  const warningCall = warning?.type === "ExpressionStatement" ? warning.expression : null;
  return (
    isExactInterruptTimeoutNotice(noticeCall) &&
    isExactInterruptTimeoutWarning(warningCall) &&
    returned?.type === "ReturnStatement" &&
    returned.argument?.type === "BooleanLiteral" &&
    returned.argument.value === true
  );
}

function isExactInterruptTimeoutNotice(call) {
  return (
    call?.type === "CallExpression" &&
    call.callee.type === "Identifier" &&
    call.callee.name === "notifyAction" &&
    call.arguments.length === 3 &&
    ((call.arguments[0].type === "Identifier" &&
      call.arguments[0].name === "INTERRUPT_UNCONFIRMED_MESSAGE") ||
      (call.arguments[0].type === "StringLiteral" &&
        call.arguments[0].value === "停止未确认，任务可能仍在运行")) &&
    call.arguments[1].type === "StringLiteral" &&
    call.arguments[1].value === "warning" &&
    isExactThreadIdObject(call.arguments[2], false)
  );
}

function isExactInterruptTimeoutWarning(call) {
  if (
    call?.type !== "CallExpression" ||
    call.callee.type !== "Identifier" ||
    call.callee.name !== "addWarning" ||
    call.arguments.length !== 3 ||
    call.arguments[0].type !== "StringLiteral" ||
    call.arguments[0].value !== "warn" ||
    !isExactActionUnconfirmedTemplate(call.arguments[1])
  )
    return false;
  return isExactThreadIdObject(call.arguments[2], true);
}

function isExactActionUnconfirmedTemplate(node) {
  return (
    node?.type === "TemplateLiteral" &&
    node.expressions.length === 1 &&
    node.expressions[0].type === "Identifier" &&
    node.expressions[0].name === "action" &&
    node.quasis.length === 2 &&
    node.quasis[0].value.cooked === "" &&
    node.quasis[1].value.cooked === ".unconfirmed"
  );
}

function isExactThreadIdObject(node, withSanitizedError) {
  const expectedLength = withSanitizedError ? 2 : 1;
  if (node?.type !== "ObjectExpression" || node.properties.length !== expectedLength) return false;
  const threadId = node.properties.find(
    (property) =>
      property.type === "ObjectProperty" && audit.staticPropertyKeyName(property) === "threadId",
  );
  if (threadId?.value.type !== "Identifier" || threadId.value.name !== "threadId") return false;
  if (!withSanitizedError) return true;
  const error = node.properties.find(
    (property) =>
      property.type === "ObjectProperty" && audit.staticPropertyKeyName(property) === "error",
  );
  return (
    error?.value.type === "StringLiteral" &&
    error.value.value === "stop confirmation timed out; see Health diagnostic ID"
  );
}

export {
  wrapperTransparentlyReturnsCall,
  resolveImportedCallTarget,
  findImportedBinding,
  findExportedSymbolPath,
  collectFacadeCallBindings,
  symbolBindsName,
  facadeCallMatchesBindings,
  bindingShadowsNameAt,
  isFunctionNode,
  blockDirectlyBindsName,
  walkAstWithAncestors,
  isIgnoredCallResult,
  isExactTurnInterruptPolicy,
  hasExactTurnInterruptTimeoutHandler,
  isExactTurnInterruptTimeoutPredicate,
  isExactResultMember,
  isExactInterruptTimeoutBranch,
  isExactInterruptTimeoutNotice,
  isExactInterruptTimeoutWarning,
  isExactActionUnconfirmedTemplate,
  isExactThreadIdObject,
};
export {
  provesTurnInterruptInjection,
  runtimePassesAwaitedResultToHandler,
} from "./turn-interrupt-injection-evidence.mjs";
