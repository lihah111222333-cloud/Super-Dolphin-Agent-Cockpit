import { execFileSync } from 'node:child_process';
import { existsSync, readFileSync } from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { createRequire } from 'node:module';

const require = createRequire(import.meta.url);
const PLAYWRIGHT_VIDEO_ENV = 'SUPER_DOLPHIN_PLAYWRIGHT_VIDEO';

export function chromiumLaunchOptions(executable, headless = true, args = []) {
  const value = String(executable || '').trim();
  if (!value) throw new Error('Chromium executable is required');
  const systemChrome = value === '/Applications/Google Chrome.app/Contents/MacOS/Google Chrome' || value === '/usr/bin/google-chrome';
  return { headless, args: [...args, '--disable-gpu', '--disable-dev-shm-usage'], ...(systemChrome ? { channel: 'chrome' } : { executablePath: value }) };
}

export function resolvePlaywrightVideoMode(env = process.env, probe = ffmpegAvailable) {
  const requested = String(env[PLAYWRIGHT_VIDEO_ENV] || '').trim().toLowerCase();
  const disabled = new Set(['0', 'false', 'off', 'no']);
  const enabled = new Set(['1', 'true', 'on', 'yes']);
  if (disabled.has(requested)) return false;
  const available = probe(env);
  if (enabled.has(requested) && !available) {
    throw new Error(`${PLAYWRIGHT_VIDEO_ENV}=1 requires an executable ffmpeg; install Playwright ffmpeg or set SUPER_DOLPHIN_FFMPEG_BIN`);
  }
  return available ? 'retain-on-failure' : false;
}

export function ffmpegAvailable(env = process.env, exists = existsSync, run = canRunExecutable) {
  const configured = String(env.SUPER_DOLPHIN_FFMPEG_BIN || env.FFMPEG_PATH || '').trim();
  if (configured) return exists(configured) && run(configured);
  const cached = playwrightFfmpegCandidates(env).find((candidate) => exists(candidate));
  return cached ? run(cached) : run('ffmpeg');
}

function canRunExecutable(candidate) {
  try {
    execFileSync(candidate, ['-version'], { stdio: 'ignore' });
    return true;
  } catch {
    return false;
  }
}

function playwrightFfmpegCandidates(env) {
  const revision = playwrightFfmpegRevision();
  const platformName = process.platform === 'darwin' ? 'ffmpeg-mac' : process.platform === 'win32' ? 'ffmpeg-win64.exe' : 'ffmpeg-linux';
  const cacheRoot = env.PLAYWRIGHT_BROWSERS_PATH === '0'
    ? path.join(path.dirname(require.resolve('playwright-core/package.json')), '.local-browsers')
    : env.PLAYWRIGHT_BROWSERS_PATH || path.join(defaultCacheRoot(), 'ms-playwright');
  return [path.join(cacheRoot, `ffmpeg-${revision}`, platformName)];
}

function playwrightFfmpegRevision() {
  const packageRoot = path.dirname(require.resolve('playwright-core/package.json'));
  const metadata = JSON.parse(readFileSync(path.join(packageRoot, 'browsers.json'), 'utf8'));
  const descriptor = metadata.browsers.find((entry) => entry.name === 'ffmpeg');
  if (!descriptor?.revision) throw new Error('Playwright ffmpeg revision is missing from browsers.json');
  return descriptor.revision;
}

function defaultCacheRoot() {
  if (process.platform === 'darwin') return path.join(os.homedir(), 'Library', 'Caches');
  if (process.platform === 'win32') return process.env.LOCALAPPDATA || path.join(os.homedir(), 'AppData', 'Local');
  return process.env.XDG_CACHE_HOME || path.join(os.homedir(), '.cache');
}
