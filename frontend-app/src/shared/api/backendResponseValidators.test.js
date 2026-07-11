import { describe, expect, it } from 'vitest';

import { RPC_METHODS } from './backendApi.js';
import { createBackendResponseValidators } from './backendResponseValidators.js';

const validators = createBackendResponseValidators(RPC_METHODS);

function validate(method, response) {
  const validator = validators[method];
  expect(validator, `${method} must have a validator`).toBeTypeOf('function');
  return validator(method, response);
}

describe('backend response validators', () => {
  it('fails fast when required thread and turn response fields are missing', () => {
    expect(() => validate(RPC_METHODS.THREAD_START, {})).toThrow('thread/start response missing threadId or thread_id');
    expect(() => validate(RPC_METHODS.THREAD_START, { thread: { id: '' } })).toThrow('thread/start response missing threadId or thread_id');
    expect(() => validate(RPC_METHODS.TURN_START, {})).toThrow('turn/start response missing turn_id or turnId');
  });

  it('validates the canonical thread/fork response envelope', () => {
    expect(validate(RPC_METHODS.THREAD_FORK, {
      thread: { id: 'thread-fork', forkedFrom: 'thread-parent' },
      kickoff_state: 'created_only',
      kickoffState: 'created_only',
    })).toEqual({
      thread: { id: 'thread-fork', forkedFrom: 'thread-parent' },
      kickoff_state: 'created_only',
      kickoffState: 'created_only',
    });

    expect(() => validate(RPC_METHODS.THREAD_FORK, {
      thread: { id: '', forkedFrom: 'thread-parent' },
      kickoffState: 'created_only',
    })).toThrow('thread/fork response thread.id is required');
    expect(() => validate(RPC_METHODS.THREAD_FORK, {
      thread: { id: 'thread-fork', forkedFrom: '' },
      kickoffState: 'created_only',
    })).toThrow('thread/fork response thread.forkedFrom is required');
    expect(() => validate(RPC_METHODS.THREAD_FORK, {
      thread: { id: 'thread-fork', forkedFrom: 'thread-parent' },
    })).toThrow('thread/fork response kickoff state is required');
    expect(() => validate(RPC_METHODS.THREAD_FORK, {
      thread: { id: 'thread-fork', forkedFrom: 'thread-parent' },
      kickoff_state: 'created_only',
      kickoffState: 'started',
    })).toThrow('thread/fork response kickoff state fields conflict');
    expect(() => validate(RPC_METHODS.THREAD_FORK, {
      thread: { id: 'thread-fork', forkedFrom: 'thread-parent' },
      kickoffState: 'automatic',
    })).toThrow('thread/fork response unsupported kickoff state automatic');
  });

  it('accepts the exact Go UI state status map wire shapes', () => {
    const response = {
      threads: [],
      agents: [],
      token_usage: {},
      statuses: { 'thread-1': 'running' },
      statusHeadersByThread: { 'thread-1': 'Thinking' },
      statusDetailsByThread: { 'thread-1': 'Inspecting state' },
      interruptibleByThread: { 'thread-1': true },
      activityStatsByThread: {
        'thread-1': { lspCalls: 1, commands: 2, fileEdits: 3, toolCalls: { read: 4 } },
      },
      agentRuntimeById: { 'thread-1': { agentId: 'agent-1', state: 'running' } },
    };

    expect(validate(RPC_METHODS.UI_STATE_GET, response)).toBe(response);
  });

  it.each([
    ['threads-only', { threads: [] }, 'agents, token_usage or tokenUsage'],
    ['agents-only', { agents: [] }, 'threads, token_usage or tokenUsage'],
    ['token_usage-only', { token_usage: {} }, 'threads, agents'],
  ])('rejects incomplete %s UI state snapshots', (_name, response, missingFields) => {
    expect(() => validate(RPC_METHODS.UI_STATE_GET, response)).toThrow(
      `ui/state/get response missing UI state snapshot fields; required: ${missingFields}`,
    );
  });

  it('accepts complete UI state snapshots with canonical snake or camel token usage', () => {
    const snakeResponse = { threads: [], agents: [], token_usage: {} };
    const camelResponse = { threads: [], agents: [], tokenUsage: {} };

    expect(validate(RPC_METHODS.UI_STATE_GET, snakeResponse)).toBe(snakeResponse);
    expect(validate(RPC_METHODS.UI_STATE_GET, camelResponse)).toBe(camelResponse);
  });

  it('binds sidebar responses to their partial wire contract and validates status map types', () => {
    const response = {
      statuses: { 'thread-1': 'running' },
      interruptibleByThread: { 'thread-1': true },
    };

    expect(validate(RPC_METHODS.UI_SIDEBAR_GET, response)).toBe(response);
    expect(() => validate(RPC_METHODS.UI_SIDEBAR_GET, {
      interruptibleByThread: { 'thread-1': 'true' },
    })).toThrow(
      'ui/sidebar/get response interruptibleByThread.thread-1 must be a boolean',
    );
  });

  it.each([
    ['statuses', ['running'], 'statuses must be an object'],
    ['statuses', { 'thread-1': { status: 'running' } }, 'statuses.thread-1 must be a string'],
    ['statuses', { '': 'running' }, 'statuses thread id must be non-empty'],
    ['statusHeadersByThread', { 'thread-1': false }, 'statusHeadersByThread.thread-1 must be a string'],
    ['statusDetailsByThread', null, 'statusDetailsByThread must be an object'],
    ['interruptibleByThread', { 'thread-1': 'true' }, 'interruptibleByThread.thread-1 must be a boolean'],
    [
      'activityStatsByThread',
      { 'thread-1': { lspCalls: '1', commands: 2, fileEdits: 3 } },
      'activityStatsByThread.thread-1.lspCalls must be an integer',
    ],
    [
      'activityStatsByThread',
      { 'thread-1': { lspCalls: 1, commands: 2, fileEdits: 3, toolCalls: { read: 1.5 } } },
      'activityStatsByThread.thread-1.toolCalls.read must be an integer',
    ],
    [
      'activityStatsByThread',
      { 'thread-1': { lspCalls: -1, commands: 2, fileEdits: 3 } },
      'activityStatsByThread.thread-1.lspCalls must be a non-negative integer',
    ],
    [
      'activityStatsByThread',
      { 'thread-1': { lspCalls: 1, commands: -2, fileEdits: 3 } },
      'activityStatsByThread.thread-1.commands must be a non-negative integer',
    ],
    [
      'activityStatsByThread',
      { 'thread-1': { lspCalls: 1, commands: 2, fileEdits: -3 } },
      'activityStatsByThread.thread-1.fileEdits must be a non-negative integer',
    ],
    [
      'activityStatsByThread',
      { 'thread-1': { lspCalls: 1, commands: 2, fileEdits: 3, toolCalls: { read: -1 } } },
      'activityStatsByThread.thread-1.toolCalls.read must be a non-negative integer',
    ],
    [
      'activityStatsByThread',
      { 'thread-1': { lspCalls: 1, commands: 2, fileEdits: 3, toolCalls: { '': 1 } } },
      'activityStatsByThread.thread-1.toolCalls tool name must be non-blank',
    ],
    [
      'activityStatsByThread',
      { 'thread-1': { lspCalls: 1, commands: 2, fileEdits: 3, toolCalls: { '   ': 1 } } },
      'activityStatsByThread.thread-1.toolCalls tool name must be non-blank',
    ],
    ['agentRuntimeById', { 'thread-1': [] }, 'agentRuntimeById.thread-1 must be an object'],
  ])('rejects malformed Go UI state %s maps', (field, malformed, message) => {
    expect(() => validate(RPC_METHODS.UI_STATE_GET, {
      threads: [],
      agents: [],
      token_usage: {},
      [field]: malformed,
    })).toThrow(message);
  });

  it('does not treat turn force-complete failure envelopes as success', () => {
    expect(validate(RPC_METHODS.TURN_FORCE_COMPLETE, {
      ok: false,
      forceCompleted: false,
      errorCode: 'turn_not_running',
    })).toEqual({
      ok: false,
      forceCompleted: false,
      errorCode: 'turn_not_running',
    });

    expect(() => validate(RPC_METHODS.TURN_FORCE_COMPLETE, {
      ok: true,
      forceCompleted: false,
      errorCode: 'turn_not_running',
    })).toThrow('turn/forceComplete response ok true cannot have forceCompleted false');
    expect(() => validate(RPC_METHODS.TURN_FORCE_COMPLETE, {
      ok: false,
      forceCompleted: false,
    })).toThrow('turn/forceComplete response failure must include errorCode, error, or message');
  });

  it('rejects MCP server control responses with unexpected or contradictory fields', () => {
    expect(() => validate(RPC_METHODS.MCP_SERVER_SQLITE_START, {
      configPath: '/repo/.mcp.json',
      serverName: 'sqlite',
      enabled: true,
      debug: true,
    })).toThrow('mcpServer/sqlite/start response body must not include debug');

    expect(() => validate(RPC_METHODS.MCP_SERVER_SQLITE_START, {
      configPath: '/repo/.mcp.json',
      serverName: 'playwright',
      enabled: true,
    })).toThrow('mcpServer/sqlite/start response serverName must be sqlite');

    expect(() => validate(RPC_METHODS.MCP_SERVER_SQLITE_STOP, {
      configPath: '/repo/.mcp.json',
      serverName: 'sqlite',
      enabled: true,
    })).toThrow('mcpServer/sqlite/stop response enabled must be false');
  });

  it('rejects MCP server list responses with unexpected server status fields', () => {
    expect(() => validate(RPC_METHODS.MCP_SERVER_LIST, {
      configPath: '/repo/.mcp.json',
      mcpServers: {
        sqlite: { enabled: true, command: 'sqlite-mcp' },
      },
    })).toThrow('mcpServer/list response mcpServers.sqlite must not include command');
  });

  it('wraps schema parser errors with method context', () => {
    expect(() => validate(RPC_METHODS.OBSERVABILITY_TRACE_GET, null)).toThrow(
      'observability/trace/get response observability response must be an object',
    );
    expect(() => validate(RPC_METHODS.UI_SHARED_FILE_GET, { path: '', content: '' })).toThrow(
      'ui/memory/shared-file/get response shared file detail path is required',
    );
  });
});
