import { mkdir, readdir, stat, writeFile } from 'node:fs/promises';
import path from 'node:path';

export function agenticE2ESandboxForRun(repoRoot, runID) {
  const rootDir = path.join(repoRoot, '.tmp', 'agentic-e2e', 'sandbox', runID);
  const projectDir = path.join(rootDir, 'project');
  return Object.freeze({
    rootDir,
    homeDir: path.join(rootDir, 'home'),
    projectDir,
    uploadFile: path.join(projectDir, 'files', 'sample.txt'),
  });
}

export async function prepareAgenticE2ESandbox(config = {}) {
  const sandbox = requireSandbox(config);
  const files = new Map([
    ['README.md', '# Agentic E2E Sandbox Project\n\nThis project is created for Desktop UI agentic E2E experiments.\n'],
    ['src/app.js', 'export function fixtureValue() {\n  return "agentic-e2e-fixture";\n}\n'],
    ['docs/note.md', '# Fixture Note\n\nThis file proves the sandbox project has readable docs.\n'],
    ['files/sample.txt', 'agentic e2e sample attachment\n'],
    ['.agents/skills/e2e-fixture/SKILL.md', '---\nname: agentic-e2e-fixture\ndescription: Fixture skill for agentic E2E sandbox tests.\n---\n\n# agentic-e2e-fixture\n'],
    ['../home/.codex/config.toml', '# agentic e2e sandbox home\n'],
  ]);

  for (const [relativePath, content] of files.entries()) {
    const filePath = path.resolve(sandbox.projectDir, relativePath);
    assertPathInsideSandbox(sandbox, filePath, `fixture ${relativePath}`);
    await mkdir(path.dirname(filePath), { recursive: true });
    await writeFile(filePath, content, 'utf8');
  }

  return snapshotAgenticE2ESandbox(config);
}

export async function snapshotAgenticE2ESandbox(config = {}) {
  const sandbox = requireSandbox(config);
  const files = await listFiles(sandbox.projectDir, sandbox.projectDir);
  return {
    rootDir: sandbox.rootDir,
    projectDir: sandbox.projectDir,
    files,
  };
}

function requireSandbox(config = {}) {
  const sandbox = config.sandbox;
  if (!sandbox || typeof sandbox !== 'object' || Array.isArray(sandbox)) {
    throw new Error('agentic e2e sandbox config is required');
  }
  for (const field of ['rootDir', 'homeDir', 'projectDir', 'uploadFile']) {
    if (typeof sandbox[field] !== 'string' || !sandbox[field].trim()) {
      throw new Error(`agentic e2e sandbox ${field} is required`);
    }
  }
  return sandbox;
}

function assertPathInsideSandbox(sandbox, targetPath, context) {
  const root = path.resolve(sandbox.rootDir);
  const target = path.resolve(targetPath);
  const relative = path.relative(root, target);
  if (relative.startsWith('..') || path.isAbsolute(relative)) {
    throw new Error(`agentic e2e sandbox path escaped for ${context}: ${targetPath}`);
  }
}

async function listFiles(rootDir, dir) {
  const entries = await readdir(dir, { withFileTypes: true });
  const files = [];
  for (const entry of entries) {
    const fullPath = path.join(dir, entry.name);
    const relative = path.relative(rootDir, fullPath).split(path.sep).join('/');
    if (entry.isDirectory()) {
      files.push(...await listFiles(rootDir, fullPath));
      continue;
    }
    if (entry.isFile()) {
      const info = await stat(fullPath);
      files.push(relative);
      if (!Number.isFinite(info.size)) throw new Error(`invalid sandbox file size for ${relative}`);
    }
  }
  return files.sort();
}
