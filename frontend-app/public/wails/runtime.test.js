import { readFileSync } from 'node:fs';
import { join } from 'node:path';
import { cwd } from 'node:process';
import { describe, expect, it } from 'vitest';

describe('development Wails runtime shim', () => {
  it('keeps the websocket bridge entrypoint in the served runtime', () => {
    const source = readFileSync(join(cwd(), 'public/wails/runtime.js'), 'utf8');
    expect(source).toContain('/wails/ws');
    expect(source).toContain('__WAILS_SHIM_DEBUG__');
  });
});
