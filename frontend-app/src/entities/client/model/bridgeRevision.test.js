import { describe, expect, it } from 'vitest';
import {
  ACTIVE_PROMPT_PREF_KEY,
  bridgeRevisionKey,
  isDagNodeStatusBridgeEvent,
  requireDagNodeStatusPayload,
} from './bridgeRevision.js';

describe('bridge revision helpers', () => {
  it('routes bridge events to their store revision counters', () => {
    expect(bridgeRevisionKey('skills/changed')).toBe('skillRevision');
    expect(bridgeRevisionKey('ui/shared-files/changed')).toBe('sharedFilesRevision');
    expect(bridgeRevisionKey('memory/changed')).toBe('memoryRevision');
    expect(bridgeRevisionKey('dags/changed')).toBe('workflowRevision');
    expect(bridgeRevisionKey('prompts/changed')).toBe('promptRevision');
    expect(bridgeRevisionKey('ui/preferences/changed', { key: ACTIVE_PROMPT_PREF_KEY })).toBe('promptRevision');
    expect(bridgeRevisionKey('ui/preferences/changed', { key: 'other' })).toBe('');
  });

  it('requires complete DAG node status payloads before incrementing workflow revision', () => {
    expect(bridgeRevisionKey('task/node/statuschanged', {
      dag_key: 'daily',
      node_key: 'write',
      new_status: 'done',
      run_key: 'run-1',
    })).toBe('workflowRevision');

    expect(() => bridgeRevisionKey('task/node/statuschanged', {
      node_key: 'write',
      new_status: 'done',
      run_key: 'run-1',
    })).toThrow('dag status event dag key is required');
  });

  it('accepts either run key or positive run id for DAG node status payloads', () => {
    expect(() => requireDagNodeStatusPayload({
      dag_key: 'daily',
      node_key: 'write',
      new_status: 'done',
      run_id: 12,
    })).not.toThrow();

    expect(() => requireDagNodeStatusPayload({
      dag_key: 'daily',
      node_key: 'write',
      new_status: 'done',
    })).toThrow('dag status event run identity is required');
  });

  it('detects DAG node status bridge events by method or type', () => {
    expect(isDagNodeStatusBridgeEvent({ method: 'task/node/statuschanged' })).toBe(true);
    expect(isDagNodeStatusBridgeEvent({ type: 'TASK/NODE/STATUSCHANGED' })).toBe(true);
    expect(isDagNodeStatusBridgeEvent({ method: 'dags/changed' })).toBe(false);
  });
});
