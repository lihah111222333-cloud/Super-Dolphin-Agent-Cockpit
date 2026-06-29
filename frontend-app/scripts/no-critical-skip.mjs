import fs from 'node:fs';
import path from 'node:path';
import process from 'node:process';

const appRoot = path.resolve(new URL('..', import.meta.url).pathname);
const sourceRoot = path.join(appRoot, 'src');
const criticalPattern = /\b(provider|thread|turn|workflow)\b/i;

const allowlist = [
  {
    file: 'src/entities/client/model/useClientStore.test.js',
    name: 'hydrates thread providers from sidebar runtime metadata',
    date: '2026-06-30',
    reason: 'Lane J cannot edit useClientStore tests; controller approval required for owning lane cleanup.',
  },
  {
    file: 'src/entities/client/model/useClientStore.test.js',
    name: 'starts a selected-provider thread instead of sending into a failed active session',
    date: '2026-06-30',
    reason: 'Lane J cannot edit useClientStore tests; controller approval required for owning lane cleanup.',
  },
  {
    file: 'src/SettingsPage.test.jsx',
    name: 'loads and saves summary and approval for the active Claude provider',
    date: '2026-06-30',
    reason: 'Lane J cannot edit legacy root Settings tests; controller approval required for owning lane cleanup.',
  },
  {
    file: 'src/SettingsPage.test.jsx',
    name: 'falls back from scoped provider preferences to global values and canonicalizes Claude effort',
    date: '2026-06-30',
    reason: 'Lane J cannot edit legacy root Settings tests; controller approval required for owning lane cleanup.',
  },
  {
    file: 'src/pages/settings/SettingsPage.test.jsx',
    name: 'persists active provider changes immediately before saving provider details',
    date: '2026-06-30',
    reason: 'Lane J cannot edit Settings tests; controller approval required for owning lane cleanup.',
  },
  {
    file: 'src/pages/settings/SettingsPage.test.jsx',
    name: 'ignores stale provider properties loads after switching active provider',
    date: '2026-06-30',
    reason: 'Lane J cannot edit Settings tests; controller approval required for owning lane cleanup.',
  },
  {
    file: 'src/pages/settings/SettingsPage.test.jsx',
    name: 'ignores stale provider preference loads after a newer active provider selection wins',
    date: '2026-06-30',
    reason: 'Lane J cannot edit Settings tests; controller approval required for owning lane cleanup.',
  },
];

function assertAllowlistIsAuditable(entries) {
  const seen = new Set();
  for (const entry of entries) {
    const key = `${entry.file}#${entry.name}`;
    if (seen.has(key)) {
      throw new Error(`duplicate critical-skip allowlist entry: ${key}`);
    }
    seen.add(key);
    if (!entry.file || !entry.name || !entry.reason) {
      throw new Error(`critical-skip allowlist entry is incomplete: ${key}`);
    }
    if (!/^\d{4}-\d{2}-\d{2}$/.test(entry.date || '')) {
      throw new Error(`critical-skip allowlist entry must carry YYYY-MM-DD date: ${key}`);
    }
  }
}

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

function allowlistKey(skip) {
  return `${skip.file}#${skip.name}`;
}

assertAllowlistIsAuditable(allowlist);

const allowlistByKey = new Map(allowlist.map((entry) => [`${entry.file}#${entry.name}`, entry]));
const seenAllowlistKeys = new Set();
const violations = [];
const skipped = [];

for (const file of walkTestFiles(sourceRoot)) {
  const relFile = path.relative(appRoot, file).split(path.sep).join('/');
  const source = fs.readFileSync(file, 'utf8');
  for (const skip of skippedTestsInSource(relFile, source)) {
    if (skip.parseError || criticalPattern.test(skip.name) || criticalPattern.test(skip.file)) {
      skipped.push(skip);
      const key = allowlistKey(skip);
      if (allowlistByKey.has(key)) {
        seenAllowlistKeys.add(key);
      } else {
        violations.push(skip);
      }
    }
  }
}

for (const key of allowlistByKey.keys()) {
  if (!seenAllowlistKeys.has(key)) {
    violations.push({
      file: key.split('#')[0],
      line: 0,
      name: key.split('#').slice(1).join('#'),
      staleAllowlist: true,
    });
  }
}

if (violations.length > 0) {
  console.error('critical .skip guard failed:');
  for (const violation of violations) {
    if (violation.staleAllowlist) {
      console.error(`- stale allowlist entry: ${violation.file} :: ${violation.name}`);
    } else {
      console.error(`- ${violation.file}:${violation.line} :: ${violation.name}`);
    }
  }
  process.exit(1);
}

console.log(`critical .skip guard passed: ${skipped.length} allowlisted critical skip(s), no unallowlisted critical skips`);
