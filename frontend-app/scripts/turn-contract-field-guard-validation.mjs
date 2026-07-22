import {
  calleeName,
  findFunction,
  memberPropertyName,
  parseModule,
  staticPropertyName,
  stringLiteralValue,
  walkFunctionBody,
  walkNode,
} from "./turn-contract-field-guard-ast.mjs";
import {
  readRepositorySource,
  assertExactSet,
  isRecord,
  validateLocatorShape,
} from "./turn-contract-field-guard-utils.mjs";
import { validatorBindings } from "./turn-contract-field-guard-evidence.mjs";
import {
  assertValidatorBindingsSafe,
  validatorBindingTarget,
} from "./turn-contract-field-guard-bindings.mjs";

const requiredTerminalChainNames = [
  "terminal-runtime-dispatch",
  "terminal-public-error-projection",
  "terminal-public-error-notice",
  "terminal-timeline-render",
  "terminal-public-error-clipboard-sink",
  "terminal-public-error-diagnostic-projection",
  "terminal-public-error-diagnostic-schema-sanitizer",
];

export function validateJSTerminalChains(repoRoot, sourceOverrides, chains) {
  if (!Array.isArray(chains) || chains.length === 0)
    throw new Error("consumer registry must contain JS terminal chains");
  assertExactSet(
    "JS terminal chain registry",
    [...requiredTerminalChainNames].sort(),
    chains.map((chain) => chain?.name).sort(),
  );
  const names = new Set();
  for (const chain of chains) {
    if (
      !isRecord(chain) ||
      typeof chain.name !== "string" ||
      !chain.name ||
      names.has(chain.name)
    )
      throw new Error(
        `JS terminal chain has blank or duplicate name ${chain?.name ?? ""}`,
      );
    names.add(chain.name);
    validateJavaScriptLocator(repoRoot, chain);
    const evidence = functionEvidence(
      resolveJSFunction(repoRoot, sourceOverrides, chain).fn,
    );
    validateRequiredEvidence(chain, "call", chain.calls, evidence.calls);
    validateRequiredEvidence(
      chain,
      "call argument",
      chain.callArguments,
      evidence.callArguments,
    );
    validateForbiddenEvidence(
      chain,
      "member path",
      chain.forbiddenMemberPaths,
      evidence.memberPaths,
    );
    validateRequiredEvidence(
      chain,
      "member path",
      chain.memberPaths,
      evidence.memberPaths,
    );
    validateForbiddenEvidence(
      chain,
      "projection",
      chain.forbiddenProjections,
      evidence.projections,
    );
    validateRequiredEvidence(
      chain,
      "call member path",
      chain.callMemberPaths,
      evidence.callMemberPaths,
    );
    validateExactProjections(chain, evidence.projections);
    validateRequiredEvidence(
      chain,
      "JSX prop",
      chain.jsxProps,
      evidence.jsxProps,
    );
  }
}

function validateForbiddenEvidence(chain, kind, forbidden, actual) {
  if (forbidden === undefined) return;
  if (!Array.isArray(forbidden))
    throw new Error(
      `JS terminal chain ${chain.name} forbidden ${kind} registration must be an array`,
    );
  for (const value of forbidden) {
    if (typeof value !== "string" || !value)
      throw new Error(
        `JS terminal chain ${chain.name} forbidden ${kind} registration contains a blank value`,
      );
    if (actual.has(value))
      throw new Error(
        `JS terminal chain ${chain.name} retains forbidden ${kind} ${value}`,
      );
  }
}
function validateRequiredEvidence(chain, kind, expected, actual) {
  if (expected === undefined) return;
  if (!Array.isArray(expected))
    throw new Error(
      `JS terminal chain ${chain.name} ${kind} registration must be an array`,
    );
  for (const value of expected)
    if (typeof value !== "string" || !value || !actual.has(value))
      throw new Error(
        `JS terminal chain ${chain.name} missing ${kind} ${value}`,
      );
}
function validateExactProjections(chain, actual) {
  if (chain.projections === undefined) return;
  if (
    !isRecord(chain.projections) ||
    Object.keys(chain.projections).length === 0
  )
    throw new Error(
      `JS terminal chain ${chain.name} projections registration must be a non-empty object`,
    );
  for (const [target, source] of Object.entries(chain.projections))
    if (!actual.has(`${target}=${source}`))
      throw new Error(
        `JS terminal chain ${chain.name} missing projection ${target}=${source}`,
      );
}

export function validateMapperSource(source, mapper) {
  if (!isRecord(mapper.fields) || Object.keys(mapper.fields).length === 0)
    throw new Error(`JS mapper ${mapper.name} has no registered fields`);
  const fn = findFunction(
    parseModule(source, mapper.path),
    mapper.symbol,
    mapper.path,
  );
  const derived = deriveRequiredAliasMappings(fn);
  assertExactSet(
    `JS mapper ${mapper.name} fields`,
    Object.keys(mapper.fields).sort(),
    [...derived.keys()].sort(),
  );
  const returned = deriveReturnedProperties(fn);
  for (const [field, mapping] of Object.entries(mapper.fields)) {
    if (
      !isRecord(mapping) ||
      !Array.isArray(mapping.aliases) ||
      typeof mapping.wire !== "string"
    )
      throw new Error(
        `JS mapper ${mapper.name}.${field} registration is incomplete`,
      );
    assertExactSet(
      `JS mapper ${mapper.name}.${field} aliases`,
      [...mapping.aliases].sort(),
      [...derived.get(field)].sort(),
    );
    if (returned.get(mapping.wire) !== field)
      throw new Error(
        `JS mapper ${mapper.name}.${field} does not map to wire field ${mapping.wire}`,
      );
  }
}

export function resolveJSFunction(repoRoot, sourceOverrides, locator) {
  validateJavaScriptLocator(repoRoot, locator);
  const source = readRepositorySource(repoRoot, locator.path, sourceOverrides);
  const ast = parseModule(source, locator.path);
  return { ast, fn: findFunction(ast, locator.symbol, locator.path) };
}

export function validateJavaScriptLocator(repoRoot, locator) {
  validateLocatorShape(repoRoot, locator, pathExtension(locator));
}
function pathExtension(locator) {
  return /\.jsx$/.test(locator?.path) ? ".jsx" : ".js";
}

export function functionHasCall(fn, target, firstStringArgument = "") {
  let found = false;
  walkFunctionBody(fn, (node) => {
    if (
      node.type === "CallExpression" &&
      calleeName(node.callee) === target &&
      (!firstStringArgument ||
        stringLiteralValue(node.arguments[0]) === firstStringArgument)
    )
      found = true;
  });
  return found;
}

export function functionHasValidatorCall(
  repoRoot,
  sourceOverrides,
  resolved,
  locator,
  targetSchemas,
  resolveValidatorExports,
) {
  const bindings = validatorBindings(
    repoRoot,
    sourceOverrides,
    resolved.ast,
    locator.path,
    targetSchemas,
    resolveValidatorExports,
  );
  assertValidatorBindingsSafe(resolved.ast, bindings, locator.path);
  let found = false;
  walkFunctionBody(resolved.fn, (node) => {
    if (
      node.type === "CallExpression" &&
      validatorBindingTarget(node.callee, bindings)?.symbol === locator.calls
    )
      found = true;
  });
  return found;
}

function functionEvidence(fn) {
  const calls = new Set();
  const memberPaths = new Set();
  const callArguments = new Set();
  const callMemberPaths = new Set();
  const projections = new Set();
  const jsxProps = new Set();
  walkNode(fn.body, (node) => {
    if (node.type === "CallExpression") {
      const name = calleeName(node.callee);
      const argument = expressionPath(node.arguments[0]);
      if (name) calls.add(name);
      if (name && argument) callArguments.add(`${name}=${argument}`);
    }
    if (
      node.type === "MemberExpression" ||
      node.type === "OptionalMemberExpression"
    ) {
      const memberPath = expressionPath(node);
      if (memberPath) memberPaths.add(memberPath);
      const sanitized = callMemberPath(node);
      if (sanitized) callMemberPaths.add(sanitized);
    }
    if (node.type === "ObjectProperty" && !node.computed) {
      const target = staticPropertyName(node);
      const source = expressionPath(node.value);
      if (target && source) projections.add(`${target}=${source}`);
    }
    if (node.type === "JSXOpeningElement") {
      const element = jsxElementName(node.name);
      for (const attribute of node.attributes) {
        const expression =
          attribute.value?.type === "JSXExpressionContainer"
            ? attribute.value.expression
            : undefined;
        if (
          element &&
          attribute.type === "JSXAttribute" &&
          attribute.name?.type === "JSXIdentifier" &&
          expression?.type === "Identifier"
        )
          jsxProps.add(`${element}:${attribute.name.name}=${expression.name}`);
      }
    }
  });
  return {
    calls,
    memberPaths,
    callArguments,
    callMemberPaths,
    projections,
    jsxProps,
  };
}
function expressionPath(node) {
  if (node?.type === "Identifier") return node.name;
  if (
    node?.type !== "MemberExpression" &&
    node?.type !== "OptionalMemberExpression"
  )
    return "";
  const object = expressionPath(node.object);
  const property = memberPropertyName(node);
  return object && property ? `${object}.${property}` : "";
}
function callMemberPath(node) {
  if (node.object?.type !== "CallExpression") return "";
  const callee = calleeName(node.object.callee);
  const argument = expressionPath(node.object.arguments[0]);
  const property = memberPropertyName(node);
  return callee && argument && property
    ? `${callee}(${argument}).${property}`
    : "";
}
function jsxElementName(node) {
  if (node?.type === "JSXIdentifier") return node.name;
  if (node?.type === "JSXMemberExpression") {
    const object = jsxElementName(node.object);
    const property = jsxElementName(node.property);
    return object && property ? `${object}.${property}` : "";
  }
  return "";
}
function deriveRequiredAliasMappings(fn) {
  const mappings = new Map();
  walkFunctionBody(fn, (node) => {
    if (
      node.type !== "VariableDeclarator" ||
      node.id?.type !== "Identifier" ||
      node.init?.type !== "CallExpression" ||
      calleeName(node.init.callee) !== "requiredStringAliasValue"
    )
      return;
    const aliases = new Set();
    walkNode(node.init, (child) => {
      if (
        child.type === "CallExpression" &&
        calleeName(child.callee) === "takePayloadField"
      ) {
        const alias = stringLiteralValue(child.arguments[1]);
        if (alias) aliases.add(alias);
      }
    });
    if (aliases.size === 0 || mappings.has(node.id.name))
      throw new Error(
        `mapper variable ${node.id.name} has missing or duplicate aliases`,
      );
    mappings.set(node.id.name, aliases);
  });
  return mappings;
}
function deriveReturnedProperties(fn) {
  const mappings = new Map();
  walkFunctionBody(fn, (node) => {
    if (
      node.type !== "ReturnStatement" ||
      node.argument?.type !== "ObjectExpression"
    )
      return;
    for (const property of node.argument.properties) {
      if (
        property.type === "ObjectProperty" &&
        !property.computed &&
        property.value?.type === "Identifier"
      ) {
        const key =
          property.key.type === "Identifier"
            ? property.key.name
            : stringLiteralValue(property.key);
        if (key) mappings.set(key, property.value.name);
      }
    }
  });
  return mappings;
}
