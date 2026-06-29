import { mkdir, mkdtemp, readFile, writeFile } from 'node:fs/promises';
import { join } from 'node:path';
import { tmpdir } from 'node:os';
import { expect, it } from 'vitest';
import { syncFrontendDist } from './sync-frontend-dist.mjs';

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
  await writeFile(join(destDir, '.gitkeep'), '');
  await writeFile(join(destDir, 'stale.txt'), 'stale');

  await syncFrontendDist({ sourceDir, destDir });

  await expect(readFile(join(destDir, 'index.html'), 'utf8')).resolves.toBe('<main>new</main>');
  await expect(readFile(join(destDir, '.gitkeep'), 'utf8')).resolves.toBe('');
  await expect(readFile(join(destDir, 'stale.txt'), 'utf8')).rejects.toThrow();
});

it('fails fast when the frontend-app build output is missing index.html', async () => {
  const root = await tempDir();
  const sourceDir = join(root, 'frontend-app', 'dist');
  const destDir = join(root, 'cmd', 'agent-terminal', 'web-dist');

  await mkdir(sourceDir, { recursive: true });
  await writeFile(join(sourceDir, 'asset.txt'), 'asset');

  await expect(syncFrontendDist({ sourceDir, destDir })).rejects.toThrow(
    'frontend-app dist is missing index.html',
  );
});
