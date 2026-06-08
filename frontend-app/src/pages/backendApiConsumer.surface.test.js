import fs from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { describe, expect, it } from 'vitest';

const sourceRoot = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..');
const scannedDirs = ['pages', 'features', 'entities'];
const rawFacadeNames = new Set(['callAPI', 'callBackend']);
const allowedRawConsumerImports = new Map([
  ['pages/settings/SettingsPage.jsx', ['callBackend']],
]);

const namedFacadeConsumerFixtures = [
  {
    file: 'pages/workflows/WorkflowPage.test.jsx',
    methods: ['startDag', 'dispatchDagNode', 'applyDagOps', 'readSharedFile'],
  },
  {
    file: 'features/prompts/PromptPageView.test.jsx',
    methods: ['writePrompt', 'deletePrompt', 'draftPromptIntent', 'commitPromptIntent'],
  },
  {
    file: 'pages/memory/MemoryPage.test.jsx',
    methods: ['upsertMemoryEntry', 'deleteMemoryEntry', 'mergeMemoryEntries'],
  },
  {
    file: 'pages/skills/SkillsPage.test.jsx',
    methods: ['createSkill', 'writeSkill', 'applySkillResolution'],
  },
];

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
      const allowed = new Set(allowedRawConsumerImports.get(relativePath) || []);
      const source = fs.readFileSync(filePath, 'utf8');
      for (const imported of backendApiNamedImports(source)) {
        if (rawFacadeNames.has(imported) && !allowed.has(imported)) {
          violations.push(`${relativePath} imports raw ${imported}`);
        }
      }
    }

    expect(violations).toEqual([]);
  });

  it('keeps high-value consumer tests mocking named facade methods', () => {
    for (const fixture of namedFacadeConsumerFixtures) {
      const source = fs.readFileSync(path.join(sourceRoot, fixture.file), 'utf8');
      expect(source).not.toContain('callBackend');
      for (const method of fixture.methods) {
        expect(source).toContain(method);
      }
    }
  });

  it('keeps temporary consumer escape hatches explicit and branch-owned', () => {
    expect([...allowedRawConsumerImports.entries()]).toEqual([
      ['pages/settings/SettingsPage.jsx', ['callBackend']],
    ]);
  });
});
