import { spawn } from 'node:child_process';

const DEFAULT_MAX_BUFFER = 16 * 1024 * 1024;

function signalProcessTree(child, signal) {
  if (!child.pid) return;
  if (process.platform !== 'win32') {
    try {
      process.kill(-child.pid, signal);
      return;
    }
    catch (error) {
      if (error.code !== 'ESRCH') throw error;
      return;
    }
  }
  if (child.exitCode == null && child.signalCode == null) child.kill(signal);
}

export function runManagedCommand(command, args, {
  cwd,
  env = process.env,
  timeoutMs,
  killGraceMs = 1_000,
  maxBuffer = DEFAULT_MAX_BUFFER,
} = {}) {
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
    let stdout = '';
    let stderr = '';
    let timedOut = false;
    let terminationStarted = false;
    let processError;
    let forceTimer;

    const terminate = (reason) => {
      if (terminationStarted) return;
      terminationStarted = true;
      timedOut = reason === 'timeout';
      if (reason !== 'timeout') processError = new Error(reason);
      try {
        signalProcessTree(child, 'SIGTERM');
      }
      catch (error) {
        processError ||= error;
      }
      forceTimer = setTimeout(() => {
        try {
          signalProcessTree(child, 'SIGKILL');
        }
        catch (error) {
          processError ||= error;
        }
      }, killGraceMs);
    };

    const append = (stream, chunk) => {
      const value = chunk.toString();
      if (stream === 'stdout') stdout += value;
      else stderr += value;
      if (Buffer.byteLength(stdout) + Buffer.byteLength(stderr) > maxBuffer) {
        terminate(`managed command exceeded maxBuffer=${maxBuffer}`);
      }
    };

    child.stdout.on('data', (chunk) => append('stdout', chunk));
    child.stderr.on('data', (chunk) => append('stderr', chunk));
    child.once('error', (error) => {
      processError = error;
    });

    const timeout = setTimeout(() => terminate('timeout'), timeoutMs);
    child.once('close', (status, signal) => {
      clearTimeout(timeout);
      if (forceTimer) {
        clearTimeout(forceTimer);
        try {
          signalProcessTree(child, 'SIGKILL');
        }
        catch (error) {
          processError ||= error;
        }
      }
      resolve({
        status,
        signal,
        stdout,
        stderr,
        timedOut,
        error: processError,
      });
    });
  });
}
