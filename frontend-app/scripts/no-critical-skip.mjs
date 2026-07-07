import fs from 'node:fs';
import path from 'node:path';
import process from 'node:process';
import { fileURLToPath } from 'node:url';

const appRoot = path.resolve(new URL('..', import.meta.url).pathname);

export const CRITICAL_SKIP_ROOTS = Object.freeze(['src', 'scripts']);
export const criticalPattern = /\b(provider|thread|turn|workflow|rpc|contract|desktop|smoke)\b/i;

function walkTestFiles(dir) {
  if (!fs.existsSync(dir)) {
    throw new Error(`critical skip root does not exist: ${dir}`);
  }
  if (!fs.statSync(dir).isDirectory()) {
    throw new Error(`critical skip root is not a directory: ${dir}`);
  }
  const entries = fs.readdirSync(dir, { withFileTypes: true });
  const files = [];
  for (const entry of entries) {
    if (entry.name === 'node_modules' || entry.name === 'dist') continue;
    const fullPath = path.join(dir, entry.name);
    if (entry.isDirectory()) {
      files.push(...walkTestFiles(fullPath));
      continue;
    }
    if (/\.(test|spec)\.[cm]?[jt]sx?$/.test(entry.name)) {
      files.push(fullPath);
    }
  }
  return files;
}

function lineNumberAt(source, index) {
  let line = 1;
  for (let i = 0; i < index; i += 1) {
    if (source.charCodeAt(i) === 10) line += 1;
  }
  return line;
}

function readStringLiteral(source, start) {
  const quote = source[start];
  if (!['"', "'", '`'].includes(quote)) return null;
  let value = '';
  for (let i = start + 1; i < source.length; i += 1) {
    const char = source[i];
    if (char === '\\') {
      value += source[i + 1] || '';
      i += 1;
      continue;
    }
    if (char === quote) return { value, end: i + 1 };
    if (quote === '`' && char === '$' && source[i + 1] === '{') return null;
    value += char;
  }
  return null;
}

function skipJSSyntaxComment(source, start) {
  if (source.startsWith('//', start)) {
    const end = source.indexOf('\n', start + 2);
    return end === -1 ? source.length : end;
  }
  if (source.startsWith('/*', start)) {
    const end = source.indexOf('*/', start + 2);
    return end === -1 ? source.length : end + 2;
  }
  return start;
}

function skipJSSyntaxTrivia(source, start) {
  const commentEnd = skipJSSyntaxComment(source, start);
  if (commentEnd !== start) return commentEnd;
  const literal = readStringLiteral(source, start);
  return literal?.end ?? start;
}

function isInsideJSSyntaxTrivia(source, target) {
  for (let cursor = 0; cursor < target; cursor += 1) {
    const skipped = skipJSSyntaxTrivia(source, cursor);
    if (skipped === cursor) continue;
    if (target < skipped) return true;
    cursor = skipped - 1;
  }
  return false;
}

export function skippedTestsInSource(relFile, source) {
  const skips = [];
  const skipPattern = /\b(?:it|test|describe)\.skip\s*\(/g;
  let match;
  while ((match = skipPattern.exec(source)) !== null) {
    if (isInsideJSSyntaxTrivia(source, match.index)) continue;
    const literalStart = skipPattern.lastIndex;
    const literal = readStringLiteral(source, literalStart);
    if (!literal) {
      skips.push({
        file: relFile,
        line: lineNumberAt(source, match.index),
        name: '<unparseable>',
        parseError: true,
      });
      continue;
    }
    skips.push({
      file: relFile,
      line: lineNumberAt(source, match.index),
      name: literal.value,
      parseError: false,
    });
  }
  return skips;
}

export function criticalSkipViolationsFromSources(sources) {
  const violations = [];
  for (const [relFile, source] of sources.entries()) {
    for (const skip of skippedTestsInSource(relFile, source)) {
      if (skip.parseError || criticalPattern.test(skip.name) || criticalPattern.test(skip.file)) {
        violations.push(skip);
      }
    }
  }
  return violations;
}

export function collectCriticalSkipViolations({ root = appRoot, roots = CRITICAL_SKIP_ROOTS } = {}) {
  const sources = new Map();
  for (const sourceRootName of roots) {
    const sourceRoot = path.join(root, sourceRootName);
    for (const file of walkTestFiles(sourceRoot)) {
      const relFile = path.relative(root, file).split(path.sep).join('/');
      sources.set(relFile, fs.readFileSync(file, 'utf8'));
    }
  }
  return criticalSkipViolationsFromSources(sources);
}

if (process.argv[1] === fileURLToPath(import.meta.url)) {
  const violations = collectCriticalSkipViolations();
  if (violations.length > 0) {
    console.error('critical .skip guard failed:');
    for (const violation of violations) {
      console.error(`- ${violation.file}:${violation.line} :: ${violation.name}`);
    }
    process.exit(1);
  }

  console.log('critical .skip guard passed: no critical skips (0 found)');
}
