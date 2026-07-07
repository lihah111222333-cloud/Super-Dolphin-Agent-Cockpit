import fs from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { describe, expect, it } from 'vitest';

const sourceRoot = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '../..');

function read(relPath) {
  return fs.readFileSync(path.join(sourceRoot, relPath), 'utf8');
}

describe('shared page surface boundary', () => {
  it('keeps App memory badge and pageShared behind page-owned memory services', () => {
    const checked = ['App.jsx', 'pages/shared/pageShared.js'];
    const violations = [];
    for (const relPath of checked) {
      const source = read(relPath);
      if (source.includes('services/modules/memoryService.js')) violations.push(`${relPath} imports memoryService`);
    }
    expect(violations).toEqual([]);
    expect(read('App.jsx')).not.toMatch(/\bfetchMemoryDashboard\b/);
    expect(read('App.jsx')).toMatch(/memory(?:Page|Badge)Service/);
    expect(read('pages/shared/pageShared.js')).toMatch(/memory(?:Page|Badge)Service/);
  });

  it('keeps prompt feature view behind the prompt page service', () => {
    const source = read('features/prompts/PromptPageView.jsx');
    expect(source).not.toContain('shared/api/backendApi.js');
    expect(source).toMatch(/promptPageService/);
  });
});
