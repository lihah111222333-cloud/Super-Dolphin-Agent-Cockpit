import { describe, expect, it } from 'vitest';
import { finalOutputKind, finalOutputPath } from './workflowDisplayAdapter.js';

describe('workflowDisplayAdapter', () => {
  it('reads artifact final output paths', () => {
    const output = {
      kind: 'artifact',
      path_template: 'reports/workflows/approval/{{run_id}}/final.docx',
    };

    expect(finalOutputPath(output)).toBe('reports/workflows/approval/{{run_id}}/final.docx');
    expect(finalOutputKind(output)).toBe('文件');
  });
});
