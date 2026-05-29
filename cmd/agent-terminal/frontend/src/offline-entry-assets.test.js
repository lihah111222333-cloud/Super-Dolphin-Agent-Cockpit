import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { describe, expect, it } from 'vitest';

describe('offline startup assets', () => {
  it('keeps the HTML entry independent of external font and CDN URLs', () => {
    const html = readFileSync(resolve(process.cwd(), 'index.html'), 'utf8');

    expect(html).not.toMatch(/https?:\/\//);
    expect(html).not.toContain('cdn.jsdelivr.net');
    expect(html).not.toContain('fonts.googleapis.com');
  });
});
