import { fileURLToPath } from 'node:url';

process.argv[1] = fileURLToPath(new URL('../frontend-code-size-guard.mjs', import.meta.url));
await import('../frontend-code-size-guard.mjs');
