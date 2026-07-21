import { spawn } from 'node:child_process';
import { realpathSync, statSync } from 'node:fs';
import { createServer } from 'node:net';
import { join } from 'node:path';
import process from 'node:process';
import { pathToFileURL } from 'node:url';

import { BUILD_IDENTITY, SOURCE_ROOT } from './identity.mjs';

function requiredEnvironment(env, name) {
  const value = env[name];
  if (typeof value !== 'string' || value.trim() === '' || value.includes('\0')) {
    throw new Error(`${name} is required and must be a non-empty NUL-free string`);
  }
  return value;
}

export function parseAthTargetPort(env = process.env) {
  const raw = requiredEnvironment(env, 'ATH_TARGET_PORT');
  if (!/^[0-9]+$/u.test(raw)) throw new Error('ATH_TARGET_PORT must be an integer TCP port');
  const port = Number(raw);
  if (!Number.isSafeInteger(port) || port < 1 || port > 65_535) {
    throw new Error('ATH_TARGET_PORT must be between 1 and 65535');
  }
  return port;
}

export function resolveAthNonce(env = process.env) {
  const nonce = requiredEnvironment(env, 'ATH_TARGET_NONCE');
  if (!/^[A-Za-z0-9_-]{43}$/u.test(nonce)) {
    throw new Error('ATH_TARGET_NONCE must be a 256-bit base64url value');
  }
  return nonce;
}

export function resolveGoModuleCache(value) {
  if (typeof value !== 'string' || value.trim() === '' || value.includes('\0')) {
    throw new Error('Go module cache path is required');
  }
  const resolved = realpathSync(value);
  if (!statSync(resolved).isDirectory()) throw new Error('Go module cache path must be a directory');
  return resolved;
}

function reserveLoopbackPort() {
  return new Promise((resolve, reject) => {
    const server = createServer();
    server.once('error', reject);
    server.listen({ host: '127.0.0.1', port: 0, exclusive: true }, () => {
      const address = server.address();
      if (address === null || typeof address === 'string') {
        server.close();
        reject(new Error('failed to reserve a numeric loopback port'));
        return;
      }
      resolve({ port: address.port, server });
    });
  });
}

function closeReservation(reservation) {
  return new Promise((resolve, reject) => reservation.server.close((error) => {
    if (error) reject(error); else resolve();
  }));
}

export async function allocateAuxiliaryPorts(targetPort) {
  const reservations = [];
  try {
    while (reservations.length < 2) {
      const reservation = await reserveLoopbackPort();
      if (reservation.port === targetPort) {
        await closeReservation(reservation);
      } else {
        reservations.push(reservation);
      }
    }
    return Object.freeze({ backendPort: reservations[0].port, controlPort: reservations[1].port });
  } finally {
    await Promise.all(reservations.map(closeReservation));
  }
}

export function buildProductEnvironment(env, ports, goModuleCache) {
  const targetPort = parseAthTargetPort(env);
  resolveAthNonce(env);
  const isolatedHome = requiredEnvironment(env, 'HOME');
  const isolatedTemp = requiredEnvironment(env, 'TMPDIR');
  if (
    !Number.isSafeInteger(ports.backendPort) ||
    !Number.isSafeInteger(ports.controlPort) ||
    ports.backendPort < 1 ||
    ports.backendPort > 65_535 ||
    ports.controlPort < 1 ||
    ports.controlPort > 65_535 ||
    new Set([targetPort, ports.backendPort, ports.controlPort]).size !== 3
  ) {
    throw new Error('target, backend, and control ports must be distinct TCP ports');
  }
  const productHome = join(isolatedHome, 'super-dolphin');
  const logRoot = join(isolatedTemp, 'super-dolphin-agentic-harness');
  const buildRoot = join(SOURCE_ROOT, '.tmp', 'agentic-testing-harness');
  const goRoot = join(buildRoot, 'go');
  return Object.freeze({
    ...env,
    VITE_DEV_URL: `http://127.0.0.1:${String(targetPort)}`,
    FRONTEND_DEVSERVER_URL: `http://127.0.0.1:${String(targetPort)}`,
    SUPER_DOLPHIN_HTTP_ADDR: `127.0.0.1:${String(ports.backendPort)}`,
    GO_AGENT_CTL_RPC_ADDR: `127.0.0.1:${String(ports.controlPort)}`,
    SUPER_DOLPHIN_HOME: productHome,
    SUPER_DOLPHIN_SQLITE_PATH: join(productHome, 'super-dolphin.db'),
    SUPER_DOLPHIN_BACKEND_LOG: join(logRoot, 'backend.log'),
    SUPER_DOLPHIN_FRONTEND_LOG: join(logRoot, 'frontend.log'),
    GOPATH: goRoot,
    GOMODCACHE: resolveGoModuleCache(goModuleCache),
    GOCACHE: join(goRoot, 'build'),
    GOPROXY: 'off',
    GOTOOLCHAIN: 'local',
    GO_AGENT_PEER_BIN_DIR: join(buildRoot, 'peer-bin'),
    SUPER_DOLPHIN_FRONTEND_PACKAGE_MANAGER: 'npm',
    SUPER_DOLPHIN_VITE_USE_POLLING: '0',
    CHOKIDAR_USEPOLLING: '0',
    SUPER_DOLPHIN_ATH_SOURCE_ROOT: SOURCE_ROOT,
    SUPER_DOLPHIN_ATH_BUILD_IDENTITY: BUILD_IDENTITY,
  });
}

export async function runSuperDolphinTarget(env = process.env, spawnImpl = spawn, goModuleCache = process.argv[2]) {
  const targetPort = parseAthTargetPort(env);
  resolveAthNonce(env);
  const ports = await allocateAuxiliaryPorts(targetPort);
  const child = spawnImpl(join(SOURCE_ROOT, 'run-new-ui-desktop.sh'), [], {
    cwd: SOURCE_ROOT,
    env: buildProductEnvironment(env, ports, goModuleCache),
    stdio: 'inherit',
  });
  let stopping = false;
  const forwardSignal = (signal) => {
    if (stopping || child.exitCode !== null || child.signalCode !== null) return;
    stopping = true;
    child.kill(signal);
  };
  const onSigint = () => forwardSignal('SIGINT');
  const onSigterm = () => forwardSignal('SIGTERM');
  process.on('SIGINT', onSigint);
  process.on('SIGTERM', onSigterm);
  try {
    return await new Promise((resolve, reject) => {
      child.once('error', reject);
      child.once('exit', (code, signal) => resolve({ code, signal }));
    });
  } finally {
    process.off('SIGINT', onSigint);
    process.off('SIGTERM', onSigterm);
  }
}

if (process.argv[1] !== undefined && import.meta.url === pathToFileURL(process.argv[1]).href) {
  runSuperDolphinTarget().then(
    ({ code, signal }) => {
      if (code !== null) process.exit(code);
      console.error(`Super-Dolphin target exited from signal ${signal ?? 'unknown'}`);
      process.exit(1);
    },
    (error) => {
      console.error(error);
      process.exit(1);
    },
  );
}
