import { describe, expect, it } from 'vitest';
import { FilesPage } from './FilesPage.jsx';

describe('FilesPage module', () => {
  it('exports the files page component', () => {
    expect(FilesPage).toBeTypeOf('function');
  });
});
