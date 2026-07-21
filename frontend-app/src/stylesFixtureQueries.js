import {
  cssSources,
  FORBIDDEN_HOST_STACKING_PROPERTIES,
  root,
} from "./stylesFixtureSources.js";
import postcss from "postcss";

export function splitSelectors(selector) {
  const selectors = [];
  let current = "";
  let depth = 0;
  for (const char of selector) {
    if (char === "(") depth += 1;
    if (char === ")") depth = Math.max(0, depth - 1);
    if (char === "," && depth === 0) {
      selectors.push(current.trim());
      current = "";
      continue;
    }
    current += char;
  }
  if (current.trim()) selectors.push(current.trim());
  return selectors;
}

function requiredDeclarationSelector(declaration) {
  const selector = declaration.parent?.selector;
  if (typeof selector !== "string")
    throw new Error("z-index declaration must belong to a CSS rule");
  return selector;
}

export function activeZIndexDeclarations() {
  const declarations = [];
  for (const [file, source] of cssSources) {
    postcssDeclarations(file, source, declarations);
  }
  return declarations;
}

function postcssDeclarations(file, source, declarations) {
  postcss.parse(source, { from: file }).walkDecls("z-index", (declaration) => {
    declarations.push({
      file,
      selector: requiredDeclarationSelector(declaration),
      value: declaration.value,
    });
  });
}

export function selectorOccurrences(selector) {
  let count = 0;
  root.walkRules((rule) => {
    if (splitSelectors(rule.selector).includes(selector)) count += 1;
  });
  return count;
}

export function indexHostViolations(source) {
  const parsed = new DOMParser().parseFromString(source, "text/html");
  const roots = [...parsed.querySelectorAll("#root")];
  const overlayRoots = [...parsed.querySelectorAll("#overlay-root")];
  const violations = [];
  if (roots.length !== 1) violations.push("root-count");
  if (overlayRoots.length !== 1) violations.push("overlay-root-count");
  if (roots.length !== 1 || overlayRoots.length !== 1) return violations;
  const [appRoot] = roots;
  const [overlayRoot] = overlayRoots;
  if (
    appRoot.parentElement !== parsed.body ||
    overlayRoot.parentElement !== parsed.body
  )
    violations.push("host-sibling");
  const children = [...parsed.body.children];
  const appIndex = children.indexOf(appRoot);
  const overlayIndex = children.indexOf(overlayRoot);
  const scriptIndex = children.findIndex((node) => node.tagName === "SCRIPT");
  if (!(appIndex < overlayIndex && overlayIndex < scriptIndex))
    violations.push("host-order");
  return violations;
}

export function forbiddenHostStackingDeclarations() {
  const violations = [];
  const hosts = new Set(["html", "body", "#overlay-root"]);
  root.walkRules((rule) => {
    const selectors = splitSelectors(rule.selector).filter((selector) =>
      hosts.has(selector),
    );
    if (selectors.length > 0)
      appendForbiddenDeclarations(violations, rule, selectors);
  });
  return violations;
}

function appendForbiddenDeclarations(violations, rule, selectors) {
  for (const declaration of rule.nodes) {
    if (
      declaration.type !== "decl" ||
      !FORBIDDEN_HOST_STACKING_PROPERTIES.has(declaration.prop)
    )
      continue;
    for (const selector of selectors)
      violations.push({
        selector,
        property: declaration.prop,
        value: declaration.value,
      });
  }
}

export function declarationsFor(selector) {
  return declarationsInRules(root, selector);
}
export function firstDeclarationsFor(selector) {
  const declarations = {};
  let found = false;
  root.walkRules(selector, (rule) => {
    if (!found) {
      found = true;
      collectDeclarations(rule, declarations);
    }
  });
  return declarations;
}
export function topLevelDeclarationsFor(selector) {
  const declarations = {};
  root.walkRules((rule) => {
    if (
      rule.parent?.type === "root" &&
      splitSelectors(rule.selector).includes(selector)
    )
      collectDeclarations(rule, declarations);
  });
  return declarations;
}
function declarationsInRules(owner, selector) {
  const declarations = {};
  owner.walkRules((rule) => {
    if (splitSelectors(rule.selector).includes(selector))
      collectDeclarations(rule, declarations);
  });
  return declarations;
}
function collectDeclarations(rule, declarations) {
  rule.walkDecls((decl) => {
    declarations[decl.prop] = decl.value;
  });
}
export function mediaDeclarationsFor(params, selector) {
  return declarationsForAtRule("media", params, selector);
}
export function mediaDeclarationFor(params, selector, property) {
  const declaration = mediaDeclarationsFor(params, selector).find((item) =>
    Object.prototype.hasOwnProperty.call(item, property),
  );
  if (declaration === undefined) {
    throw new Error(
      `missing media declaration: params=${params}, selector=${selector}, property=${property}`,
    );
  }
  return declaration;
}
export function containerDeclarationsFor(params, selector) {
  return declarationsForAtRule("container", params, selector);
}
function declarationsForAtRule(name, params, selector) {
  const matches = [];
  root.walkAtRules(name, (atRule) => {
    if (atRule.params === params)
      matches.push(...declarationsInAtRule(atRule, selector));
  });
  return matches;
}
function declarationsInAtRule(atRule, selector) {
  const matches = [];
  atRule.walkRules((rule) => {
    if (splitSelectors(rule.selector).includes(selector)) {
      const declarations = {};
      collectDeclarations(rule, declarations);
      matches.push(declarations);
    }
  });
  return matches;
}
