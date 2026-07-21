import * as audit from "../rpc-contract-audit.mjs";
import {
  isAlwaysFalseExpression,
  containsDirectThrow,
  isSupportedInvalidPredicate,
} from "./source-index.mjs";

function safeParseImplementationIsProven(method) {
  const taintedNames = new Set(
    method.params.filter((item) => item.type === "Identifier").map((item) => item.name),
  );
  let invalidReturn = false;
  let successReturn = false;
  for (const statement of method.body.body) {
    if (
      statement.type === "IfStatement" &&
      !isAlwaysFalseExpression(statement.test) &&
      isSupportedInvalidPredicate(statement.test, taintedNames)
    ) {
      const returns =
        statement.consequent.type === "ReturnStatement"
          ? [statement.consequent]
          : statement.consequent.type === "BlockStatement"
            ? statement.consequent.body.filter((child) => child.type === "ReturnStatement")
            : [];
      if (returns.some((child) => objectBooleanProperty(child.argument, "success") === false))
        invalidReturn = true;
    }
    if (
      statement.type === "ReturnStatement" &&
      objectBooleanProperty(statement.argument, "success") === true
    ) {
      successReturn = true;
    }
  }
  return invalidReturn && successReturn;
}

function objectBooleanProperty(node, name) {
  if (node?.type !== "ObjectExpression") return null;
  const property = node.properties.find(
    (item) => item.type === "ObjectProperty" && audit.staticPropertyKeyName(item) === name,
  );
  return property?.value.type === "BooleanLiteral" ? property.value.value : null;
}

function safeParseFailureDominates(shapeSymbol, resultName, declarator) {
  const statements = shapeSymbol.body?.type === "BlockStatement" ? shapeSymbol.body.body : [];
  const declarationIndex = statements.findIndex(
    (statement) =>
      statement.type === "VariableDeclaration" && statement.declarations.includes(declarator),
  );
  if (declarationIndex < 0) return false;
  for (const statement of statements.slice(declarationIndex + 1)) {
    if (statement.type !== "IfStatement" || !containsDirectThrow(statement.consequent)) {
      if (nodeContainsIdentifier(statement, resultName)) return false;
      continue;
    }
    const test = statement.test;
    const member =
      test.type === "UnaryExpression" && test.operator === "!" ? test.argument : test.left;
    const explicitFalse =
      test.type === "BinaryExpression" &&
      test.operator === "===" &&
      test.right.type === "BooleanLiteral" &&
      test.right.value === false;
    if ((test.type === "UnaryExpression" && test.operator === "!") || explicitFalse) {
      return (
        member?.type === "MemberExpression" &&
        !member.computed &&
        member.object.type === "Identifier" &&
        member.object.name === resultName &&
        member.property.type === "Identifier" &&
        member.property.name === "success"
      );
    }
    if (nodeContainsIdentifier(statement, resultName)) return false;
  }
  return false;
}

function nodeContainsIdentifier(node, name) {
  let found = false;
  audit.traverseAst(node, (candidate) => {
    if (candidate.type === "Identifier" && candidate.name === name) found = true;
  });
  return found;
}

function shapeDominatesConsumerUse(
  ast,
  consumerSymbol,
  facadeCall,
  consumerPath,
  shapePath,
  shapeSymbol,
) {
  const body = consumerSymbol.body?.type === "BlockStatement" ? consumerSymbol.body.body : null;
  if (!body) return false;
  const shapeAliases = new Set();
  if (consumerPath === shapePath) {
    if (audit.symbolBindsName(consumerSymbol, shapeSymbol)) return false;
    shapeAliases.add(shapeSymbol);
  } else {
    for (const statement of ast.program.body) {
      if (
        statement.type !== "ImportDeclaration" ||
        !audit.moduleSpecifierResolvesTo(consumerPath, statement.source.value, shapePath)
      ) {
        continue;
      }
      for (const specifier of statement.specifiers) {
        if (
          specifier.type === "ImportSpecifier" &&
          audit.moduleExportName(specifier.imported) === shapeSymbol
        ) {
          shapeAliases.add(specifier.local.name);
        }
      }
    }
  }
  if (shapeAliases.size === 0) return false;
  let resultName = "";
  let callStatementIndex = -1;
  for (let index = 0; index < body.length; index += 1) {
    audit.walkAstWithAncestors(body[index], (node, ancestors) => {
      if (node !== facadeCall || resultName) return;
      if (ancestors.some((ancestor) => audit.isFunctionNode(ancestor))) return;
      const parent = ancestors.at(-1);
      const declarator = parent?.type === "AwaitExpression" ? ancestors.at(-2) : parent;
      if (declarator?.type === "VariableDeclarator" && declarator.id.type === "Identifier") {
        resultName = declarator.id.name;
        callStatementIndex = index;
      }
    });
  }
  if (!resultName) return false;
  const taintedNames = new Set([resultName]);
  for (let index = callStatementIndex + 1; index < body.length; index += 1) {
    const statement = body[index];
    if (statement.type === "VariableDeclaration") {
      let propagated = false;
      for (const declarator of statement.declarations) {
        if (
          declarator.id.type === "Identifier" &&
          declarator.init?.type === "Identifier" &&
          taintedNames.has(declarator.init.name)
        ) {
          taintedNames.add(declarator.id.name);
          propagated = true;
        }
      }
      if (propagated) continue;
    }
    const validation =
      statement.type === "ExpressionStatement" && statement.expression.type === "CallExpression"
        ? statement.expression
        : null;
    if (
      validation &&
      validation.callee.type === "Identifier" &&
      shapeAliases.has(validation.callee.name) &&
      !audit.bindingShadowsNameAt(
        [consumerSymbol, consumerSymbol.body, statement],
        validation.callee.name,
      ) &&
      validation.arguments.some(
        (argument) => argument.type === "Identifier" && taintedNames.has(argument.name),
      )
    )
      return true;
    if ([...taintedNames].some((name) => nodeContainsIdentifier(statement, name))) return false;
  }
  return false;
}

function collectUnusedPolicyFindings(auditContext, entry) {
  const filePath = auditContext.productionFacadeReferenceIndex.get(entry.key);
  if (!filePath) return [];
  return [
    {
      key: entry.key,
      kind: entry.responsePolicy.kind,
      field: "productionScanRoots",
      path: filePath,
      symbol: entry.facade.split(".").at(-1),
      reason: "production facade reference exists",
    },
  ];
}

export {
  safeParseImplementationIsProven,
  objectBooleanProperty,
  safeParseFailureDominates,
  nodeContainsIdentifier,
  shapeDominatesConsumerUse,
  collectUnusedPolicyFindings,
};
