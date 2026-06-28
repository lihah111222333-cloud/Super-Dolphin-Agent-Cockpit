import { access, cp, rm } from 'node:fs/promises';
import { constants } from 'node:fs';
import { resolve } from 'node:path';
import { fileURLToPath } from 'node:url';

function defaultRepoRoot() {
  if (import.meta.url.startsWith('file:')) {
    return resolve(fileURLToPath(new URL('.', import.meta.url)), '..', '..');
  }
  return process.cwd();
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
  destDir = resolve(defaultRepoRoot(), 'cmd', 'agent-terminal', 'frontend', 'dist'),
} = {}) {
  await requireFile(resolve(sourceDir, 'index.html'), `frontend-app dist is missing index.html: ${sourceDir}`);
  await rm(destDir, { recursive: true, force: true });
  await cp(sourceDir, destDir, { recursive: true });
  await requireFile(resolve(destDir, 'index.html'), `embedded frontend dist is missing index.html: ${destDir}`);
}

if (process.argv[1] === fileURLToPath(import.meta.url)) {
  syncFrontendDist().catch((error) => {
    console.error(error.message || error);
    process.exit(1);
  });
}
