import { spawn } from 'node:child_process';
import { setTimeout as sleep } from 'node:timers/promises';

const DEFAULT_MAX_BUFFER = 16 * 1024 * 1024;
const activeTerminations = new Set();
let signalHandlersInstalled = false;

function signalError(signal) {
  return new Error(`managed command interrupted by ${signal}`);
}

function installSignalHandlers() {
  if (signalHandlersInstalled) return;
  signalHandlersInstalled = true;
  process.on('SIGINT', onInterrupt);
  process.on('SIGTERM', onTerminate);
}

function removeSignalHandlers() {
  if (!signalHandlersInstalled || activeTerminations.size !== 0) return;
  signalHandlersInstalled = false;
  process.off('SIGINT', onInterrupt);
  process.off('SIGTERM', onTerminate);
}

function onInterrupt() {
  terminateManagedCommands('SIGINT');
}

function onTerminate() {
  terminateManagedCommands('SIGTERM');
}

function killPosixProcessGroup(child, signal, processKill = process.kill.bind(process)) {
  if (!child.pid) return false;
  try {
    processKill(-child.pid, signal);
    return true;
  }
  catch (error) {
    if (error.code === 'ESRCH') return false;
    throw error;
  }
}

function killWindowsProcessTree(child, spawnImpl = spawn) {
  if (!child.pid) return Promise.resolve();
  return new Promise((resolve, reject) => {
    const killer = spawnImpl('taskkill', ['/PID', String(child.pid), '/T', '/F'], {
      stdio: 'ignore',
      windowsHide: true,
    });
    killer.once('error', reject);
    killer.once('close', (status) => {
      if (status === 0) {
        resolve();
        return;
      }
      reject(new Error(`taskkill /T /F failed for pid=${child.pid}: exit=${status}`));
    });
  });
}

export function signalProcessTree(child, signal, platform = process.platform, processKill = process.kill.bind(process)) {
  if (platform === 'win32') return killWindowsProcessTree(child);
  killPosixProcessGroup(child, signal, processKill);
  return Promise.resolve();
}

function posixProcessGroupExists(child, processKill) {
  if (!child.pid) return false;
  try {
    processKill(-child.pid, 0);
    return true;
  }
  catch (error) {
    if (error.code === 'ESRCH') return false;
    throw error;
  }
}

async function waitForPosixProcessGroupExit(child, timeoutMs, processKill, sleepImpl) {
  const deadline = Date.now() + timeoutMs;
  while (posixProcessGroupExists(child, processKill)) {
    if (Date.now() >= deadline) return false;
    await sleepImpl(Math.min(20, Math.max(1, deadline - Date.now())));
  }
  return true;
}

export async function terminateProcessTree(child, {
  platform = process.platform,
  spawnImpl = spawn,
  processKill = process.kill.bind(process),
  killGraceMs = 1_000,
  sleepImpl = sleep,
} = {}) {
  if (!child?.pid) return;
  if (platform === 'win32') {
    await killWindowsProcessTree(child, spawnImpl);
    return;
  }
  if (!killPosixProcessGroup(child, 'SIGTERM', processKill)) return;
  if (await waitForPosixProcessGroupExit(child, killGraceMs, processKill, sleepImpl)) return;
  killPosixProcessGroup(child, 'SIGKILL', processKill);
  if (!await waitForPosixProcessGroupExit(child, killGraceMs, processKill, sleepImpl)) {
    throw new Error(`process group ${child.pid} remained alive after SIGKILL`);
  }
}

export function terminateManagedCommands(signal = 'SIGTERM') {
  for (const terminate of [...activeTerminations]) terminate(signalError(signal));
}

export function runManagedCommand(command, args, {
  cwd,
  env = process.env,
  timeoutMs,
  killGraceMs = 1_000,
  maxBuffer = DEFAULT_MAX_BUFFER,
} = {}) {
  if (typeof command !== 'string' || !command) throw new Error('command must be a non-empty string');
  if (!Array.isArray(args)) throw new Error('args must be an array');
  if (!Number.isSafeInteger(timeoutMs) || timeoutMs < 1) throw new Error('timeoutMs must be a positive integer');
  if (!Number.isSafeInteger(killGraceMs) || killGraceMs < 1) throw new Error('killGraceMs must be a positive integer');
  if (!Number.isSafeInteger(maxBuffer) || maxBuffer < 1) throw new Error('maxBuffer must be a positive integer');

  return new Promise((resolve) => {
    const child = spawn(command, args, {
      cwd,
      env,
      detached: process.platform !== 'win32',
      stdio: ['ignore', 'pipe', 'pipe'],
    });
    const stdoutChunks = [];
    const stderrChunks = [];
    let capturedBytes = 0;
    let timedOut = false;
    let outputTruncated = false;
    let terminationStarted = false;
    let processError;
    let timeout;
    let settled = false;
    let terminationPromise;

    const recordTerminationError = (error) => {
      processError ||= error;
    };

    const terminate = (reason) => {
      if (terminationStarted) return;
      terminationStarted = true;
      timedOut = reason === 'timeout';
      processError ||= reason === 'timeout'
        ? new Error(`managed command timed out after ${timeoutMs}ms`)
        : reason;
      terminationPromise = terminateProcessTree(child, { killGraceMs }).catch(recordTerminationError);
    };

    const finish = async (status, signal) => {
      if (settled) return;
      settled = true;
      clearTimeout(timeout);
      if (status !== 0 && !terminationStarted) {
        terminationStarted = true;
        terminationPromise = terminateProcessTree(child, { killGraceMs }).catch(recordTerminationError);
      }
      if (terminationPromise) await terminationPromise;
      activeTerminations.delete(terminate);
      removeSignalHandlers();
      resolve({
        status,
        signal,
        stdout: Buffer.concat(stdoutChunks).toString('utf8'),
        stderr: Buffer.concat(stderrChunks).toString('utf8'),
        timedOut,
        outputTruncated,
        error: processError,
      });
    };

    const append = (stream, chunk) => {
      if (outputTruncated) return;
      const bytes = Buffer.isBuffer(chunk) ? chunk : Buffer.from(chunk);
      const remaining = maxBuffer - capturedBytes;
      if (remaining <= 0) {
        outputTruncated = true;
        child.stdout?.pause();
        child.stderr?.pause();
        terminate(new Error(`managed command exceeded maxBuffer=${maxBuffer}`));
        return;
      }
      const retained = Buffer.from(bytes.subarray(0, remaining));
      if (stream === 'stdout') stdoutChunks.push(retained);
      else stderrChunks.push(retained);
      capturedBytes += retained.byteLength;
      if (retained.byteLength < bytes.byteLength) {
        outputTruncated = true;
        child.stdout?.pause();
        child.stderr?.pause();
        terminate(new Error(`managed command exceeded maxBuffer=${maxBuffer}`));
      }
    };

    activeTerminations.add(terminate);
    installSignalHandlers();
    child.stdout?.on('data', (chunk) => append('stdout', chunk));
    child.stderr?.on('data', (chunk) => append('stderr', chunk));
    child.once('error', (error) => {
      processError ||= error;
      if (!child.pid) void finish(null, null);
    });
    timeout = setTimeout(() => terminate('timeout'), timeoutMs);
    child.once('close', (status, signal) => { void finish(status, signal); });
  });
}
