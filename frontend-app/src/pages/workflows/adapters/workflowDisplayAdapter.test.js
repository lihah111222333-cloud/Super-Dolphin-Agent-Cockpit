import { describe, expect, it } from 'vitest';
import { finalOutputKind, finalOutputPath, workflowConfigDiagnostics, workflowSharedFileRows } from './workflowDisplayAdapter.js';

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

  it('reports non-object JSON configs instead of normalizing them to empty objects', () => {
    expect(workflowConfigDiagnostics([
      { nodeKey: 'array-config', title: 'Array Config', config: '[]' },
      { nodeKey: 'text-config', title: 'Text Config', config: '"hello"' },
    ])).toEqual([
      expect.objectContaining({ nodeKey: 'array-config', message: expect.stringContaining('config JSON object parse failed') }),
      expect.objectContaining({ nodeKey: 'text-config', message: expect.stringContaining('config JSON object parse failed') }),
    ]);
  });

  it('fails fast for malformed nested shared-file config shapes', () => {
    expect(() => workflowSharedFileRows([
      {
        nodeKey: 'bad-nested',
        title: 'Bad Nested',
        config: {
          inputs: '[]',
          outputs: '{"to_sharedfile":[]}',
        },
      },
    ])).toThrow(/config JSON object parse failed/);
  });
});
