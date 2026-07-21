import { mkdtempSync, readFileSync, rmSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { cwd, execPath } from 'node:process';
import { describe, expect, it } from 'vitest';
import { runManagedCommand } from './managed-command.mjs';

function processIsGone(pid) {
  try {
    process.kill(pid, 0);
    return false;
  }
  catch (error) {
    return error.code === 'ESRCH';
  }
}

describe('managed command', () => {
  it('terminates a timed-out command group including its descendant process', async () => {
    const tempRoot = mkdtempSync(join(tmpdir(), 'managed-command-descendant-'));
    const pidPath = join(tempRoot, 'pids.json');
    try {
      const result = await runManagedCommand(execPath, ['-e', `
      const { spawn } = require('node:child_process');
      const fs = require('node:fs');
      const child = spawn(process.execPath, ['-e', 'setInterval(() => {}, 1000)'], { stdio: 'ignore' });
      fs.mkdirSync(require('node:path').dirname(process.argv[1]), { recursive: true });
      fs.writeFileSync(process.argv[1], JSON.stringify({ parent: process.pid, child: child.pid }));
      setInterval(() => {}, 1000);
    `, pidPath], {
        cwd: cwd(),
        timeoutMs: 500,
        killGraceMs: 100,
      });

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
});
