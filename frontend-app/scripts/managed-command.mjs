import { spawn } from 'node:child_process';

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

function killPosixProcessGroup(child, signal, killImpl = process.kill) {
  if (!child.pid) return;
  try {
    killImpl(-child.pid, signal);
  }
  catch (error) {
    if (error.code !== 'ESRCH') throw error;
  }
}

function killWindowsProcessTree(child) {
  if (!child.pid) return Promise.resolve();
  return new Promise((resolve, reject) => {
    const killer = spawn('taskkill', ['/PID', String(child.pid), '/T', '/F'], {
      stdio: 'ignore',
      windowsHide: true,
    });
    killer.once('error', reject);
    killer.once('close', (status) => {
      if (status === 0 || child.exitCode != null || child.signalCode != null) {
        resolve();
        return;
      }
      reject(new Error(`taskkill /T /F failed for pid=${child.pid}: exit=${status}`));
    });
  });
}

export function signalProcessTree(child, signal, platform = process.platform, killImpl = process.kill) {
  if (platform === 'win32') return killWindowsProcessTree(child);
  killPosixProcessGroup(child, signal, killImpl);
  return Promise.resolve();
}

function killChildFallback(child) {
  if (child.exitCode != null || child.signalCode != null) return;
  try {
    child.kill('SIGKILL');
  }
  catch (error) {
    if (error.code !== 'ESRCH') throw error;
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
    let forceTimer;
    let timeout;
    let settled = false;

    const recordTerminationError = (error) => {
      processError ||= error;
      if (process.platform === 'win32') {
        try {
          killChildFallback(child);
        }
        catch (fallbackError) {
          processError ||= fallbackError;
        }
      }
    };

    const signalTree = (signal) => {
      try {
        signalProcessTree(child, signal).catch(recordTerminationError);
      }
      catch (error) {
        recordTerminationError(error);
      }
    };

    const terminate = (reason) => {
      if (terminationStarted) return;
      terminationStarted = true;
      timedOut = reason === 'timeout';
      processError ||= reason === 'timeout'
        ? new Error(`managed command timed out after ${timeoutMs}ms`)
        : reason;
      signalTree('SIGTERM');
      if (process.platform !== 'win32') {
        forceTimer = setTimeout(() => signalTree('SIGKILL'), killGraceMs);
      }
    };

    const finish = (status, signal) => {
      if (settled) return;
      settled = true;
      clearTimeout(timeout);
      if (forceTimer) clearTimeout(forceTimer);
      if (terminationStarted && process.platform !== 'win32') {
        try {
          killPosixProcessGroup(child, 'SIGKILL');
        }
        catch (error) {
          processError ||= error;
        }
      }
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
      if (!child.pid) finish(null, null);
    });
    timeout = setTimeout(() => terminate('timeout'), timeoutMs);
    child.once('close', finish);
  });
}
