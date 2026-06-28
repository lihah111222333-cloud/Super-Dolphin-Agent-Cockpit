import { execSync } from 'node:child_process';
import { resolve, dirname } from 'node:path';
import { fileURLToPath } from 'node:url';

const __dirname = dirname(fileURLToPath(import.meta.url));

export default function setup() {
    console.log('🛡️  Running codebase size guard...');
    const targetScript = resolve(__dirname, 'size-guard.cjs');
    try {
        execSync(`node "${targetScript}"`, {
            cwd: resolve(__dirname, '..'),
            stdio: 'inherit',
        });
    } catch (err) {
        throw new Error('Size guard check failed. Please fix violations before running tests.');
    }
}
