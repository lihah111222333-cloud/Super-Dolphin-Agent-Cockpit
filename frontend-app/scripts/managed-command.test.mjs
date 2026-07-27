import { mkdtempSync, readFileSync, rmSync } from 'node:fs';
import { EventEmitter } from 'node:events';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { cwd, execPath } from 'node:process';
import { describe, expect, it } from 'vitest';
import { runManagedCommand, terminateManagedCommands, terminateProcessTree } from './managed-command.mjs';

function processIsGone(pid) {
  try {
    process.kill(pid, 0);
    return false;
  }
  catch (error) {
    return error.code === 'ESRCH';
  }
}

async function waitForFile(filePath, timeoutMs = 4_000) {
  const deadline = Date.now() + timeoutMs;
  while (Date.now() < deadline) {
    try {
      readFileSync(filePath, 'utf8');
      return;
    }
    catch (error) {
      if (error.code !== 'ENOENT') throw error;
    }
    await new Promise((resolve) => setTimeout(resolve, 20));
  }
  throw new Error(`timed out waiting for ${filePath}`);
}

describe('managed command', () => {
  it('terminates a timed-out command group including its descendant process', async () => {
    const tempRoot = mkdtempSync(join(tmpdir(), 'managed-command-descendant-'));
    const pidPath = join(tempRoot, 'pids.json');
    try {
      const pending = runManagedCommand(execPath, ['-e', `
      const { spawn } = require('node:child_process');
      const fs = require('node:fs');
      const child = spawn(process.execPath, ['-e', 'setInterval(() => {}, 1000)'], { stdio: 'ignore' });
      fs.mkdirSync(require('node:path').dirname(process.argv[1]), { recursive: true });
      fs.writeFileSync(process.argv[1], JSON.stringify({ parent: process.pid, child: child.pid }));
      setInterval(() => {}, 1000);
    `, pidPath], {
      cwd: cwd(),
      timeoutMs: 5_000,
      killGraceMs: 100,
    });

      await waitForFile(pidPath);
      const result = await pending;

      expect(result.timedOut).toBe(true);
      expect(result.status ?? result.signal).not.toBe(null);
      const { parent, child } = JSON.parse(readFileSync(pidPath, 'utf8'));
      const deadline = Date.now() + 5_000;
      while ((!processIsGone(parent) || !processIsGone(child)) && Date.now() < deadline) {
        await new Promise((resolve) => setTimeout(resolve, 50));
      }
      expect(processIsGone(parent)).toBe(true);
      expect(processIsGone(child)).toBe(true);
    }
    finally {
      rmSync(tempRoot, { recursive: true, force: true });
    }
  }, 10_000);

  it('terminates active command groups when the outer runner is interrupted', async () => {
    const tempRoot = mkdtempSync(join(tmpdir(), 'managed-command-interrupt-'));
    const pidPath = join(tempRoot, 'pids.json');
    try {
      const pending = runManagedCommand(execPath, ['-e', `
      const { spawn } = require('node:child_process');
      const fs = require('node:fs');
      const child = spawn(process.execPath, ['-e', 'setInterval(() => {}, 1000)'], { stdio: 'ignore' });
      fs.writeFileSync(process.argv[1], JSON.stringify({ parent: process.pid, child: child.pid }));
      setInterval(() => {}, 1000);
    `, pidPath], {
        cwd: cwd(),
        timeoutMs: 10_000,
        killGraceMs: 100,
      });

      await waitForFile(pidPath);
      terminateManagedCommands('SIGINT');
      const result = await pending;

      expect(result.timedOut).toBe(false);
      expect(result.error?.message).toContain('interrupted by SIGINT');
      const { parent, child } = JSON.parse(readFileSync(pidPath, 'utf8'));
      const deadline = Date.now() + 5_000;
      while ((!processIsGone(parent) || !processIsGone(child)) && Date.now() < deadline) {
        await new Promise((resolve) => setTimeout(resolve, 50));
      }
      expect(processIsGone(parent)).toBe(true);
      expect(processIsGone(child)).toBe(true);
    }
    finally {
      rmSync(tempRoot, { recursive: true, force: true });
    }
  }, 10_000);

  it('caps captured output before terminating an unbounded writer', async () => {
    const maxBuffer = 1_024;
    const result = await runManagedCommand(execPath, ['-e', `
      process.stdout.write('x'.repeat(128 * 1024));
      setInterval(() => {}, 1_000);
    `], {
      cwd: cwd(),
      timeoutMs: 5_000,
      killGraceMs: 100,
      maxBuffer,
    });

    expect(result.timedOut).toBe(false);
    expect(result.error?.message).toContain(`maxBuffer=${maxBuffer}`);
    expect(Buffer.byteLength(result.stdout) + Buffer.byteLength(result.stderr)).toBeLessThanOrEqual(maxBuffer);
  }, 10_000);

  it('sweeps descendants when the managed parent exits non-zero', async () => {
    if (process.platform === 'win32') return;
    const tempRoot = mkdtempSync(join(tmpdir(), 'managed-command-nonzero-'));
    const pidPath = join(tempRoot, 'child.pid');
    try {
      const result = await runManagedCommand(execPath, ['-e', `
        const { spawn } = require('node:child_process');
        const fs = require('node:fs');
        const child = spawn(process.execPath, ['-e', 'setInterval(() => {}, 1000)'], { stdio: 'ignore' });
        fs.writeFileSync(process.argv[1], String(child.pid));
        process.exit(2);
      `, pidPath], {
        cwd: cwd(),
        timeoutMs: 5_000,
        killGraceMs: 100,
      });
      expect(result.status).toBe(2);
      const childPID = Number(readFileSync(pidPath, 'utf8'));
      expect(processIsGone(childPID)).toBe(true);
    }
    finally {
      rmSync(tempRoot, { recursive: true, force: true });
    }
  }, 10_000);

  it('awaits Windows taskkill tree cleanup with an immutable PID', async () => {
    const calls = [];
    const spawnImpl = (command, args, options) => {
      calls.push({ command, args, options });
      const killer = new EventEmitter();
      queueMicrotask(() => killer.emit('close', 0));
      return killer;
    };
    await terminateProcessTree({ pid: 2468 }, {
      platform: 'win32',
      spawnImpl,
      killGraceMs: 5,
    });
    expect(calls).toEqual([expect.objectContaining({
      command: 'taskkill',
      args: ['/PID', '2468', '/T', '/F'],
    })]);
  });
});
