import { access, cp, mkdir, readFile, readdir, rm } from 'node:fs/promises';
import { constants } from 'node:fs';
import { resolve } from 'node:path';
import { fileURLToPath } from 'node:url';

function defaultRepoRoot() {
  if (import.meta.url.startsWith('file:')) {
    return resolve(fileURLToPath(new URL('.', import.meta.url)), '..', '..');
  }
  return process.cwd();
}

export async function requiredFrontendEntries({
  manifestPath = resolve(defaultRepoRoot(), 'frontend-app', 'required-dist-entries.txt'),
} = {}) {
  let content;
  try {
    content = await readFile(manifestPath, 'utf8');
  } catch {
    throw new Error(`required frontend entries manifest is unreadable: ${manifestPath}`);
  }
  const entries = content.split(/\r?\n/).filter((entry) => entry !== '');
  if (entries.length === 0) {
    throw new Error(`required frontend entries manifest is empty: ${manifestPath}`);
  }
  const uniqueEntries = new Set();
  for (const entry of entries) {
    if (!/^[A-Za-z0-9][A-Za-z0-9._-]*$/.test(entry)) {
      throw new Error(`required frontend entries manifest has invalid entry ${entry}: ${manifestPath}`);
    }
    if (uniqueEntries.has(entry)) {
      throw new Error(`required frontend entries manifest has duplicate entry ${entry}: ${manifestPath}`);
    }
    uniqueEntries.add(entry);
  }
  return entries;
}

async function requireFile(path, message) {
  try {
    await access(path, constants.R_OK);
  } catch {
    throw new Error(message);
  }
}

export async function syncFrontendDist({
  sourceDir = resolve(defaultRepoRoot(), 'frontend-app', 'dist'),
  destDir = resolve(defaultRepoRoot(), 'cmd', 'agent-terminal', 'web-dist'),
  manifestPath,
} = {}) {
  const requiredEntries = await requiredFrontendEntries({ manifestPath });
  for (const entry of requiredEntries) {
    const entryPath = resolve(sourceDir, entry);
    await requireFile(entryPath, `frontend-app dist is missing required entry ${entry}: ${entryPath}`);
  }
  await mkdir(destDir, { recursive: true });
  const entries = await readdir(destDir, { withFileTypes: true });
  await Promise.all(entries
    .filter((entry) => entry.name !== '.gitkeep')
    .map((entry) => rm(resolve(destDir, entry.name), { recursive: true, force: true })));
  await cp(sourceDir, destDir, { recursive: true });
  for (const entry of requiredEntries) {
    const entryPath = resolve(destDir, entry);
    await requireFile(entryPath, `embedded frontend dist is missing required entry ${entry}: ${entryPath}`);
  }
}

if (process.argv[1] === fileURLToPath(import.meta.url)) {
  syncFrontendDist().catch((error) => {
    console.error(error.message || error);
    process.exit(1);
  });
}
