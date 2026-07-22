#!/usr/bin/env node

import process from 'node:process';

const args = process.argv.slice(2);
if (args.length === 2 && args[0] === 'app-server' && args[1] === '--help') {
  process.stdout.write('desktop smoke Codex app-server stub\n');
} else {
  process.stderr.write('desktop smoke Codex stub refuses provider execution\n');
  process.exitCode = 64;
}
