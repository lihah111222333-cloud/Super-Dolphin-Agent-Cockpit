import fs from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { describe, expect, it } from 'vitest';

const sourceRoot = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..');
const scannedDirs = ['pages', 'features', 'entities'];
const rawFacadeNames = new Set(['callAPI', 'callBackend']);

function collectSourceFiles() {
  const files = [];
  for (const dir of scannedDirs) {
    walk(path.join(sourceRoot, dir), files);
  }
  return files;
}

function walk(dir, files) {
  for (const entry of fs.readdirSync(dir, { withFileTypes: true })) {
    const fullPath = path.join(dir, entry.name);
    if (entry.isDirectory()) {
      walk(fullPath, files);
      continue;
    }
    if (!/\.[jt]sx?$/.test(entry.name) || /\.test\.[jt]sx?$/.test(entry.name)) continue;
    files.push(fullPath);
  }
}

function rel(filePath) {
  return path.relative(sourceRoot, filePath).split(path.sep).join('/');
}

function backendApiNamedImports(source) {
  const imports = [];
  const importPattern = /import\s*\{([^}]+)\}\s*from\s*['"]([^'"]*shared\/api\/backendApi\.js)['"]/g;
  for (const match of source.matchAll(importPattern)) {
    for (const part of match[1].split(',')) {
      const imported = part.trim().split(/\s+as\s+/)[0]?.trim();
      if (imported) imports.push(imported);
    }
  }
  return imports;
}

describe('backend API consumer guardrails', () => {
  it('keeps page, feature, and entity consumers on named backend facade imports', () => {
    const violations = [];

    for (const filePath of collectSourceFiles()) {
      const relativePath = rel(filePath);
      const source = fs.readFileSync(filePath, 'utf8');
      for (const imported of backendApiNamedImports(source)) {
        if (rawFacadeNames.has(imported)) {
          violations.push(`${relativePath} imports raw ${imported}`);
        }
      }
    }

    expect(violations).toEqual([]);
  });
});
