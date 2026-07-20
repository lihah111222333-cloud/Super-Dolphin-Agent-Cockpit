import { execFileSync } from 'node:child_process';
import { realpathSync } from 'node:fs';
import { dirname, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';

export const NONCE_HEADER = 'x-agentic-testing-harness-nonce';
export const SOURCE_ROOT_HEADER = 'x-super-dolphin-source-root';
export const BUILD_IDENTITY_HEADER = 'x-super-dolphin-build-identity';
export const HEALTH_PATH = '/__ath_health';
export const TARGET_NAME = 'super-dolphin-frontend';

const identityDirectory = dirname(fileURLToPath(import.meta.url));
export const FRONTEND_ROOT = realpathSync(resolve(identityDirectory, '../..'));
export const SOURCE_ROOT = realpathSync(resolve(FRONTEND_ROOT, '..'));

function safeHeader(name, value) {
  if (typeof value !== 'string' || value.trim() === '' || /[\r\n]/u.test(value)) {
    throw new Error(`${name} must be a non-empty value without CR or LF`);
  }
  return value;
}

export function resolveBuildIdentity(sourceRoot = SOURCE_ROOT) {
  const commit = execFileSync('git', ['rev-parse', 'HEAD'], {
    cwd: sourceRoot,
    encoding: 'utf8',
    stdio: ['ignore', 'pipe', 'pipe'],
  }).trim();
  return safeHeader('build identity', `git:${commit}`);
}

export const BUILD_IDENTITY = resolveBuildIdentity();
export const EXPECTED_IDENTITY = Object.freeze({
  sourceRootHeader: SOURCE_ROOT_HEADER,
  buildIdentityHeader: BUILD_IDENTITY_HEADER,
  sourceRoot: safeHeader('source root', SOURCE_ROOT),
  buildIdentity: BUILD_IDENTITY,
});
