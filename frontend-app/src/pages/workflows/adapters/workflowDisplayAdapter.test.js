import { describe, expect, it } from 'vitest';
import { finalOutputKind, finalOutputPath, workflowConfigDiagnostics } from './workflowDisplayAdapter.js';

describe('workflowDisplayAdapter', () => {
  it('reads artifact final output paths', () => {
    const output = {
      kind: 'artifact',
      path_template: 'reports/workflows/approval/{{run_id}}/final.docx',
    };

    expect(finalOutputPath(output)).toBe('reports/workflows/approval/{{run_id}}/final.docx');
    expect(finalOutputKind(output)).toBe('文件');
  });

  it('reports malformed workflow node configs for diagnostics', () => {
    expect(workflowConfigDiagnostics([
      { nodeKey: 'bad-json', title: 'Bad JSON', config: '{"inputs":' },
    ])).toEqual([
      expect.objectContaining({
        nodeKey: 'bad-json',
        severity: 'error',
        message: expect.stringContaining('config'),
      }),
    ]);
  });
});
