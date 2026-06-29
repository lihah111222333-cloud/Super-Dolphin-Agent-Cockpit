import fs from 'node:fs';
import path from 'node:path';
import process from 'node:process';

const appRoot = path.resolve(new URL('..', import.meta.url).pathname);
const sourceRoot = path.join(appRoot, 'src');
const criticalPattern = /\b(provider|thread|turn|workflow)\b/i;

function walkTestFiles(dir) {
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

function skippedTestsInSource(relFile, source) {
  const skips = [];
  const skipPattern = /\b(?:it|test|describe)\.skip\s*\(/g;
  let match;
  while ((match = skipPattern.exec(source)) !== null) {
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

const violations = [];
let skipped = 0;

for (const file of walkTestFiles(sourceRoot)) {
  const relFile = path.relative(appRoot, file).split(path.sep).join('/');
  const source = fs.readFileSync(file, 'utf8');
  for (const skip of skippedTestsInSource(relFile, source)) {
    if (skip.parseError || criticalPattern.test(skip.name) || criticalPattern.test(skip.file)) {
      skipped += 1;
      violations.push(skip);
    }
  }
}

if (violations.length > 0) {
  console.error('critical .skip guard failed:');
  for (const violation of violations) {
    console.error(`- ${violation.file}:${violation.line} :: ${violation.name}`);
  }
  process.exit(1);
}

console.log(`critical .skip guard passed: no critical skips (${skipped} found)`);
