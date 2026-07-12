import fs from 'node:fs';
import path from 'node:path';
import process from 'node:process';
import { fileURLToPath } from 'node:url';
import postcss from 'postcss';

const TOKEN_SOURCE_FILE = 'src/shared/styles/LayerTokens.css';
const EXPECTED_TOKENS = Object.freeze([
  '--z-local-behind',
  '--z-local-raised',
  '--z-local-handle',
  '--z-local-sticky',
  '--z-shell-control',
  '--z-overlay-popover',
  '--z-overlay-dialog',
  '--z-overlay-lightbox',
  '--z-overlay-critical',
]);
const EXPECTED_TOKEN_SET = new Set(EXPECTED_TOKENS);
const GLOBAL_OVERLAY_TOKENS = Object.freeze([
  '--z-overlay-popover',
  '--z-overlay-dialog',
  '--z-overlay-lightbox',
  '--z-overlay-critical',
]);
const GLOBAL_OVERLAY_TOKEN_SET = new Set(GLOBAL_OVERLAY_TOKENS);
const STRICT_NUMBER = /^-?(?:\d+|\d*\.\d+)$/;
const BARE_Z_INDEX_NUMBER = /^-?(?:\d+|\d*\.\d+)$/;
const EXACT_TOKEN_REFERENCE = /^var\((--z-[a-z0-9-]+)\)$/;

function codedError(code, message, cause) {
  const error = new Error(message, cause ? { cause } : undefined);
  error.code = code;
  return error;
}

function parseSource(file, source) {
  if (typeof source !== 'string') {
    throw codedError('z-index-source-invalid', `z-index source must be text: ${file}`);
  }
  try {
    return postcss.parse(source, { from: file });
  } catch (cause) {
    throw codedError('z-index-source-parse-failed', `z-index source parse failed: ${file}`, cause);
  }
}

function splitSelectors(selector) {
  const selectors = [];
  let current = '';
  let depth = 0;
  for (const char of selector) {
    if (char === '(') depth += 1;
    if (char === ')') depth = Math.max(0, depth - 1);
    if (char === ',' && depth === 0) {
      selectors.push(current.trim());
      current = '';
      continue;
    }
    current += char;
  }
  if (current.trim()) selectors.push(current.trim());
  return selectors;
}

function topLevelOverlayRootIndexes(selector) {
  const marker = '#overlay-root';
  const indexes = [];
  let parentheses = 0;
  let brackets = 0;
  let quote = '';
  let escaped = false;

  for (let index = 0; index < selector.length; index += 1) {
    const char = selector[index];
    if (escaped) {
      escaped = false;
      continue;
    }
    if (char === '\\') {
      escaped = true;
      continue;
    }
    if (quote) {
      if (char === quote) quote = '';
      continue;
    }
    if (char === '"' || char === "'") {
      quote = char;
      continue;
    }
    if (char === '[') {
      brackets += 1;
      continue;
    }
    if (char === ']') {
      if (brackets === 0) return [];
      brackets -= 1;
      continue;
    }
    if (brackets > 0) continue;
    if (char === '(') {
      parentheses += 1;
      continue;
    }
    if (char === ')') {
      if (parentheses === 0) return [];
      parentheses -= 1;
      continue;
    }
    if (parentheses === 0 && selector.startsWith(marker, index)) indexes.push(index);
  }

  if (escaped || quote || parentheses !== 0 || brackets !== 0) return [];
  return indexes;
}

function attributeSelectorEnd(selector, start) {
  let quote = '';
  let escaped = false;
  for (let index = start + 1; index < selector.length; index += 1) {
    const char = selector[index];
    if (escaped) {
      escaped = false;
      continue;
    }
    if (char === '\\') {
      escaped = true;
      continue;
    }
    if (quote) {
      if (char === quote) quote = '';
      continue;
    }
    if (char === '"' || char === "'") {
      quote = char;
      continue;
    }
    if (char === '[') return -1;
    if (char === ']') return index + 1;
  }
  return -1;
}

function isSelectorBoundary(char) {
  return char === ' ' || char === '\n' || char === '\r' || char === '\t' || char === '\f' || char === '>';
}

function hasExplicitOverlayRootAncestor(selector) {
  const marker = '#overlay-root';
  return topLevelOverlayRootIndexes(selector).some((markerIndex) => {
    let suffixIndex = markerIndex + marker.length;
    while (selector[suffixIndex] === '[') {
      suffixIndex = attributeSelectorEnd(selector, suffixIndex);
      if (suffixIndex < 0) return false;
    }
    return isSelectorBoundary(selector[suffixIndex]);
  });
}

function declarationLine(declaration) {
  return declaration.source?.start?.line || 1;
}

function tokenDefinitions(root, violations) {
  const rootRules = root.nodes.filter(
    (node) => node.type === 'rule' && node.selector.trim() === ':root',
  );
  if (rootRules.length !== 1) {
    violations.push({ code: 'token-source-root-count', count: rootRules.length });
  }
  const canonicalRoot = rootRules.length === 1 ? rootRules[0] : null;
  const definitions = new Map();
  root.walkDecls((declaration) => {
    if (!declaration.prop.startsWith('--z-')) return;
    if (declaration.parent !== canonicalRoot) {
      violations.push({
        code: 'token-definition-outside-root',
        line: declarationLine(declaration),
        token: declaration.prop,
      });
      if (!EXPECTED_TOKEN_SET.has(declaration.prop)) {
        violations.push({ code: 'token-set-mismatch', token: declaration.prop });
      }
      return;
    }
    const entries = definitions.get(declaration.prop) || [];
    entries.push(declaration);
    definitions.set(declaration.prop, entries);
  });
  return definitions;
}

function validateTokenDefinitions(root, violations) {
  const definitions = tokenDefinitions(root, violations);
  for (const token of EXPECTED_TOKENS) {
    if (!definitions.has(token)) violations.push({ code: 'token-set-mismatch', token });
  }
  for (const [token, entries] of definitions) {
    if (!EXPECTED_TOKEN_SET.has(token)) violations.push({ code: 'token-set-mismatch', token });
    if (entries.length > 1) violations.push({ code: 'token-duplicate-definition', token });
  }

  const overlayValues = GLOBAL_OVERLAY_TOKENS.map((token) => {
    const value = definitions.get(token)?.[0]?.value?.trim() || '';
    return STRICT_NUMBER.test(value) ? Number(value) : Number.NaN;
  });
  if (
    overlayValues.some((value) => !Number.isFinite(value))
    || overlayValues.some((value, index) => index > 0 && value <= overlayValues[index - 1])
  ) {
    violations.push({ code: 'overlay-order-invalid' });
  }
}

function validateProductionSource(file, root, usedTokens, violations) {
  root.walkDecls((declaration) => {
    if (declaration.prop.startsWith('--z-')) {
      violations.push({
        code: 'token-definition-outside-source',
        file,
        line: declarationLine(declaration),
        token: declaration.prop,
      });
    }
    if (declaration.prop.toLowerCase() !== 'z-index') return;

    const value = declaration.value.trim();
    if (BARE_Z_INDEX_NUMBER.test(value)) {
      violations.push({ code: 'z-index-bare-number', file, line: declarationLine(declaration), value });
      return;
    }
    const reference = EXACT_TOKEN_REFERENCE.exec(value);
    if (!reference) {
      violations.push({ code: 'z-index-invalid-value', file, line: declarationLine(declaration), value });
      return;
    }

    const token = reference[1];
    if (!EXPECTED_TOKEN_SET.has(token)) {
      violations.push({ code: 'z-index-unknown-token', file, line: declarationLine(declaration), token });
      return;
    }
    usedTokens.add(token);

    if (GLOBAL_OVERLAY_TOKEN_SET.has(token)) {
      const selectors = splitSelectors(declaration.parent?.selector || '');
      for (const selector of selectors) {
        if (hasExplicitOverlayRootAncestor(selector)) continue;
        violations.push({
          code: 'global-token-outside-overlay-root',
          file,
          line: declarationLine(declaration),
          selector,
          token,
        });
      }
    }
  });
}

export function validateZIndexContract({ tokenSource, cssSources, ...policy }) {
  if (!(cssSources instanceof Map)) {
    throw codedError('z-index-sources-invalid', 'z-index cssSources must be a Map');
  }
  const violations = Object.keys(policy).map((option) => ({ code: 'policy-bypass-option', option }));
  const tokenRoot = parseSource(TOKEN_SOURCE_FILE, tokenSource);
  validateTokenDefinitions(tokenRoot, violations);

  const usedTokens = new Set();
  for (const [file, source] of cssSources) {
    validateProductionSource(file, parseSource(file, source), usedTokens, violations);
  }
  for (const token of EXPECTED_TOKENS) {
    if (!usedTokens.has(token)) violations.push({ code: 'token-unused', token });
  }
  return violations;
}

function collectCssFiles(directory) {
  const files = [];
  for (const entry of fs.readdirSync(directory, { withFileTypes: true })) {
    const file = path.join(directory, entry.name);
    if (entry.isDirectory()) files.push(...collectCssFiles(file));
    else if (entry.isFile() && entry.name.endsWith('.css')) files.push(file);
  }
  return files.sort();
}

function relativeFile(appRoot, file) {
  return path.relative(appRoot, file).split(path.sep).join('/');
}

export function runZIndexGuard({ appRoot = process.cwd() } = {}) {
  const tokenPath = path.join(appRoot, TOKEN_SOURCE_FILE);
  const tokenSource = fs.readFileSync(tokenPath, 'utf8');
  const cssSources = new Map();
  for (const file of collectCssFiles(path.join(appRoot, 'src'))) {
    const relative = relativeFile(appRoot, file);
    if (relative === TOKEN_SOURCE_FILE) continue;
    cssSources.set(relative, fs.readFileSync(file, 'utf8'));
  }
  return validateZIndexContract({ tokenSource, cssSources });
}

function isDirectExecution() {
  if (!process.argv[1]) return false;
  try {
    return fs.realpathSync(process.argv[1]) === fs.realpathSync(fileURLToPath(import.meta.url));
  } catch {
    return path.resolve(process.argv[1]) === path.resolve(fileURLToPath(import.meta.url));
  }
}

function formatViolation(violation) {
  const location = violation.file ? ` ${violation.file}:${violation.line || 1}` : '';
  const detail = violation.token || violation.option || violation.value || violation.selector || '';
  return `- [${violation.code}]${location}${detail ? ` ${detail}` : ''}`;
}

if (isDirectExecution()) {
  try {
    const violations = runZIndexGuard();
    if (violations.length > 0) {
      console.error('frontend z-index token guard failed:');
      for (const violation of violations) console.error(formatViolation(violation));
      process.exitCode = 1;
    } else {
      console.log('frontend z-index token guard passed');
    }
  } catch (error) {
    console.error(`frontend z-index token guard failed: ${error.message}`);
    process.exitCode = 1;
  }
}
