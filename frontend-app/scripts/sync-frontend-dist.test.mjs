import { mkdir, mkdtemp, readFile, writeFile } from 'node:fs/promises';
import { join } from 'node:path';
import { tmpdir } from 'node:os';
import { expect, it } from 'vitest';
import { requiredFrontendEntries, syncFrontendDist } from './sync-frontend-dist.mjs';

const manifestPath = join(process.cwd(), 'required-dist-entries.txt');

async function tempDir() {
  return mkdtemp(join(tmpdir(), 'super-dolphin-sync-dist-'));
}

it('replaces the embedded frontend dist with the built frontend-app dist', async () => {
  const root = await tempDir();
  const sourceDir = join(root, 'frontend-app', 'dist');
  const destDir = join(root, 'cmd', 'agent-terminal', 'web-dist');

  await mkdir(sourceDir, { recursive: true });
  await mkdir(destDir, { recursive: true });
  await writeFile(join(sourceDir, 'index.html'), '<main>new</main>');
  await writeFile(join(sourceDir, 'recovery.html'), '<main>recover</main>');
  await writeFile(join(destDir, '.gitkeep'), '');
  await writeFile(join(destDir, 'stale.txt'), 'stale');

  await syncFrontendDist({ sourceDir, destDir, manifestPath });

  await expect(readFile(join(destDir, 'index.html'), 'utf8')).resolves.toBe('<main>new</main>');
  await expect(readFile(join(destDir, 'recovery.html'), 'utf8')).resolves.toBe('<main>recover</main>');
  await expect(readFile(join(destDir, '.gitkeep'), 'utf8')).resolves.toBe('');
  await expect(readFile(join(destDir, 'stale.txt'), 'utf8')).rejects.toThrow();
});

it('loads the required frontend entries from the shared manifest', async () => {
  await expect(requiredFrontendEntries({ manifestPath })).resolves.toEqual(['index.html', 'recovery.html']);
});

it('fails fast when the frontend-app build output is missing index.html', async () => {
  const root = await tempDir();
  const sourceDir = join(root, 'frontend-app', 'dist');
  const destDir = join(root, 'cmd', 'agent-terminal', 'web-dist');

  await mkdir(sourceDir, { recursive: true });
  await writeFile(join(sourceDir, 'asset.txt'), 'asset');

  await expect(syncFrontendDist({ sourceDir, destDir, manifestPath })).rejects.toThrow(
    `frontend-app dist is missing required entry index.html: ${join(sourceDir, 'index.html')}`,
  );
});

it('fails fast before mutating the embedded dist when recovery.html is missing', async () => {
  const root = await tempDir();
  const sourceDir = join(root, 'frontend-app', 'dist');
  const destDir = join(root, 'cmd', 'agent-terminal', 'web-dist');

  await mkdir(sourceDir, { recursive: true });
  await mkdir(destDir, { recursive: true });
  await writeFile(join(sourceDir, 'index.html'), '<main>new</main>');
  await writeFile(join(destDir, 'sentinel.txt'), 'preserve');

  await expect(syncFrontendDist({ sourceDir, destDir, manifestPath })).rejects.toThrow(
    `frontend-app dist is missing required entry recovery.html: ${join(sourceDir, 'recovery.html')}`,
  );
  await expect(readFile(join(destDir, 'sentinel.txt'), 'utf8')).resolves.toBe('preserve');
});
