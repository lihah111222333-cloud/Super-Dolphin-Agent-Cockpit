#!/usr/bin/env node
import { spawn } from 'node:child_process';
import { existsSync, mkdirSync, rmSync } from 'node:fs';
import net from 'node:net';
import path from 'node:path';
import { setTimeout as sleep } from 'node:timers/promises';
import { fileURLToPath } from 'node:url';

const DEFAULT_HTTP_ADDR = '127.0.0.1:4513';
const DEFAULT_VITE_URL = 'http://127.0.0.1:5176';
const DEFAULT_CTL_ADDR = '127.0.0.1:8093';
const DEFAULT_POSTGRES_PORT = 55434;
const DEFAULT_TIMEOUT_MS = 180000;
const DEFAULT_CHROME_PATH = '/usr/bin/google-chrome';

export function repoRootFromScript(metaURL = import.meta.url) {
  return path.resolve(path.dirname(fileURLToPath(metaURL)), '..', '..');
}

export function resolveChromeExecutable(env = process.env, exists = existsSync) {
  if (env.PLAYWRIGHT_CHROMIUM_EXECUTABLE) return env.PLAYWRIGHT_CHROMIUM_EXECUTABLE;
  if (exists(DEFAULT_CHROME_PATH)) return DEFAULT_CHROME_PATH;
  throw new Error(`PLAYWRIGHT_CHROMIUM_EXECUTABLE is required; ${DEFAULT_CHROME_PATH} was not found`);
}

export function desktopUXSmokeConfig(env = process.env, repoRoot = repoRootFromScript()) {
  const postgresPort = positiveInt(env.SUPER_DOLPHIN_PLAYWRIGHT_POSTGRES_PORT, DEFAULT_POSTGRES_PORT);
  const runID = `${postgresPort}-${process.pid}`;
  return {
    repoRoot,
    frontendRoot: path.join(repoRoot, 'frontend-app'),
    runner: env.SUPER_DOLPHIN_PLAYWRIGHT_RUNNER || path.join(repoRoot, 'run-new-ui-desktop.sh'),
    httpAddr: env.SUPER_DOLPHIN_PLAYWRIGHT_HTTP_ADDR || DEFAULT_HTTP_ADDR,
    viteURL: env.SUPER_DOLPHIN_PLAYWRIGHT_VITE_URL || DEFAULT_VITE_URL,
    ctlAddr: env.SUPER_DOLPHIN_PLAYWRIGHT_CTL_ADDR || DEFAULT_CTL_ADDR,
    postgresPort,
    postgresDataDir: env.SUPER_DOLPHIN_PLAYWRIGHT_POSTGRES_DATA_DIR || path.join(repoRoot, '.tmp', `playwright-pgdata-${runID}`),
    postgresRuntimeDir: env.SUPER_DOLPHIN_PLAYWRIGHT_POSTGRES_RUNTIME_DIR || path.join('/tmp', `sd-pw-pg-${runID}`),
    chromeExecutable: resolveChromeExecutable(env),
    timeoutMs: positiveInt(env.SUPER_DOLPHIN_PLAYWRIGHT_TIMEOUT_MS, DEFAULT_TIMEOUT_MS),
  };
}

function positiveInt(value, fallback) {
  if (value == null || value === '') return fallback;
  const parsed = Number(value);
  if (!Number.isInteger(parsed) || parsed <= 0) throw new Error(`expected positive integer, got ${value}`);
  return parsed;
}

export function buildDesktopUXEnv(config, baseEnv = process.env) {
  const logDir = path.join(config.repoRoot, '.tmp', 'playwright-ux-smoke');
  return {
    ...baseEnv,
    SUPER_DOLPHIN_HTTP_ADDR: config.httpAddr,
    GO_AGENT_CTL_RPC_ADDR: config.ctlAddr,
    VITE_DEV_URL: config.viteURL,
    FRONTEND_DEVSERVER_URL: config.viteURL,
    SUPER_DOLPHIN_LOCAL_POSTGRES_PORT: String(config.postgresPort),
    SUPER_DOLPHIN_LOCAL_POSTGRES_DATA_DIR: config.postgresDataDir,
    SUPER_DOLPHIN_LOCAL_POSTGRES_RUNTIME_DIR: config.postgresRuntimeDir,
    SUPER_DOLPHIN_LOCAL_POSTGRES_LOG: path.join(logDir, 'postgres.log'),
    SUPER_DOLPHIN_BACKEND_LOG: path.join(logDir, 'backend.log'),
    SUPER_DOLPHIN_FRONTEND_LOG: path.join(logDir, 'frontend.log'),
    SUPER_DOLPHIN_DESKTOP_SMOKE_ACTIVE: '1',
    SUPER_DOLPHIN_DESKTOP_UX_BASE_URL: config.viteURL,
    PLAYWRIGHT_CHROMIUM_EXECUTABLE: config.chromeExecutable,
  };
}

export async function runDesktopUXSmoke(config = desktopUXSmokeConfig(), deps = {}) {
  await assertSmokePortsFree(config);
  const env = buildDesktopUXEnv(config);
  mkdirSync(path.dirname(env.SUPER_DOLPHIN_BACKEND_LOG), { recursive: true });
  const child = startDesktop(config, env, deps.spawn || spawn);
  try {
    await Promise.all([
      waitForHTTP(`http://${config.httpAddr}/metrics`, config.timeoutMs, child),
      waitForHTTP(config.viteURL, config.timeoutMs, child),
    ]);
    await runPlaywright(config, env, deps.spawn || spawn);
    console.log('desktop UX smoke passed');
  }
  finally {
    await stopProcessTree(child);
    cleanupCreatedPaths(config);
  }
}

async function assertSmokePortsFree(config) {
  for (const value of [config.httpAddr, config.ctlAddr, `127.0.0.1:${config.postgresPort}`, new URL(config.viteURL).host]) {
    await assertPortFree(value);
  }
}

export async function assertPortFree(hostPort) {
  const { host, port } = parseHostPort(hostPort);
  await new Promise((resolve, reject) => {
    const server = net.createServer();
    server.once('error', (error) => reject(new Error(`port ${port} is already in use on ${host}: ${error.message}`)));
    server.listen(port, host, () => {
      server.close(resolve);
    });
  });
}

export function parseHostPort(value) {
  const raw = String(value || '').trim();
  const index = raw.lastIndexOf(':');
  if (index <= 0) throw new Error(`host:port is required, got ${value}`);
  const host = raw.slice(0, index);
  const port = positiveInt(raw.slice(index + 1), 0);
  return { host, port };
}

function startDesktop(config, env, spawnImpl) {
  console.log(`starting desktop UX smoke: ${config.runner}`);
  return spawnImpl(config.runner, [], {
    cwd: config.repoRoot,
    env,
    stdio: 'inherit',
    detached: true,
  });
}

function runPlaywright(config, env, spawnImpl) {
  const playwrightBin = path.join(config.frontendRoot, 'node_modules', '.bin', 'playwright');
  if (!existsSync(playwrightBin)) throw new Error(`missing Playwright binary: ${playwrightBin}`);
  return runCommand(playwrightBin, ['test', '--config', 'playwright.desktop.config.js'], config.frontendRoot, env, spawnImpl);
}

function runCommand(command, args, cwd, env, spawnImpl) {
  console.log(`running UX smoke command: ${command} ${args.join(' ')}`);
  return new Promise((resolve, reject) => {
    const child = spawnImpl(command, args, { cwd, env, stdio: 'inherit' });
    child.once('error', reject);
    child.once('exit', (code, signal) => {
      if (code === 0) {
        resolve();
        return;
      }
      reject(new Error(`${command} ${args.join(' ')} failed: exit=${code} signal=${signal || ''}`));
    });
  });
}

async function waitForHTTP(url, timeoutMs, child) {
  const deadline = Date.now() + timeoutMs;
  while (Date.now() < deadline) {
    if (child.exitCode != null || child.signalCode != null) {
      throw new Error(`desktop exited before readiness: exit=${child.exitCode} signal=${child.signalCode}`);
    }
    try {
      const response = await fetch(url);
      if (response.ok) {
        console.log(`ready: ${url}`);
        return;
      }
    }
    catch {
      // Poll until timeout; child exit above is the fail-fast path.
    }
    await sleep(500);
  }
  throw new Error(`timed out waiting for ${url}`);
}

async function stopProcessTree(child) {
  if (!child || child.pid == null || child.exitCode != null || child.signalCode != null) return;
  try {
    process.kill(-child.pid, 'SIGTERM');
  }
  catch {
    child.kill('SIGTERM');
  }
  await Promise.race([
    new Promise((resolve) => child.once('exit', resolve)),
    sleep(10000).then(() => {
      try {
        process.kill(-child.pid, 'SIGKILL');
      }
      catch {
        child.kill('SIGKILL');
      }
    }),
  ]);
}

function cleanupCreatedPaths(config) {
  for (const dir of [config.postgresRuntimeDir, config.postgresDataDir]) {
    if (dir.startsWith('/tmp/sd-pw-pg-') || dir.includes(`${path.sep}.tmp${path.sep}playwright-pgdata-`)) {
      rmSync(dir, { recursive: true, force: true });
    }
  }
}

export function isMain(metaURL, argv1) {
  return argv1 ? path.resolve(fileURLToPath(metaURL)) === path.resolve(argv1) : false;
}

if (isMain(import.meta.url, process.argv[1])) {
  runDesktopUXSmoke().catch((error) => {
    console.error(`desktop UX smoke failed: ${error.message}`);
    process.exitCode = 1;
  });
}
