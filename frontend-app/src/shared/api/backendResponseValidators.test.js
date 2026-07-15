import { describe, expect, it, test } from 'vitest';

import { RPC_METHODS } from './backendApi.js';
import { createBackendResponseValidators } from './backendResponseValidators.js';

const validators = createBackendResponseValidators(RPC_METHODS);

test('cron list response requires exact snake_case page fields', () => {
  const validate = validators[RPC_METHODS.CRONJOB_LIST];
  expect(validate(RPC_METHODS.CRONJOB_LIST, { jobs: [], next_cursor: '', has_more: false })).toEqual({ jobs: [], next_cursor: '', has_more: false });
  expect(() => validate(RPC_METHODS.CRONJOB_LIST, { jobs: [], nextCursor: '', has_more: false })).toThrow(/exactly/);
  expect(() => validate(RPC_METHODS.CRONJOB_LIST, { jobs: [], next_cursor: '', has_more: false, extra: true })).toThrow(/exactly/);
});

function validate(method, response) {
  const validator = validators[method];
  expect(validator, `${method} must have a validator`).toBeTypeOf('function');
  return validator(method, response);
}

describe('backend response validators', () => {
  it('accepts only the canonical thread recovery envelope', () => {
    const response = {
      thread: { id: 'thread-1', status: 'recovering' },
      recovered: true,
      mode: 'relaunch_resume',
    };

    expect(validate(RPC_METHODS.THREAD_RECOVER, response)).toBe(response);
  });

  it.each([
    [{ recovered: true, mode: 'relaunch_resume' }, 'thread/recover response thread must be an object'],
    [{ thread: { status: 'recovering' }, recovered: true, mode: 'relaunch_resume' }, 'thread/recover response thread.id must be a non-empty string'],
    [{ thread: { id: 'thread-1' }, recovered: true, mode: 'relaunch_resume' }, 'thread/recover response thread.status must be recovering'],
    [{ thread: { id: 'thread-1', status: 'idle' }, recovered: true, mode: 'relaunch_resume' }, 'thread/recover response thread.status must be recovering'],
    [{ thread: { id: 'thread-1', status: 'recovering' }, recovered: 'true', mode: 'relaunch_resume' }, 'thread/recover response recovered must be a boolean'],
    [{ thread: { id: 'thread-1', status: 'recovering' }, recovered: true, mode: '   ' }, 'thread/recover response mode must be a non-empty string'],
    [{ thread: { id: 'thread-1', status: 'recovering' }, recovered: true, mode: 'relaunch_resume', debug: true }, 'thread/recover response body must not include debug'],
    [{ thread: { id: 'thread-1', status: 'recovering', name: 'extra' }, recovered: true, mode: 'relaunch_resume' }, 'thread/recover response thread must not include name'],
  ])('rejects malformed thread recovery envelopes', (response, message) => {
    expect(() => validate(RPC_METHODS.THREAD_RECOVER, response)).toThrow(message);
  });

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

  it('rejects unknown ActivityStats fields for full UI state responses', () => {
    expect(() => validate(RPC_METHODS.UI_STATE_GET, {
      threads: [],
      activityStatsByThread: {
        'thread-1': { lspCalls: 1, commands: 2, fileEdits: 3, surprise: true },
      },
    })).toThrow('activityStatsByThread.thread-1 must not include surprise');
  });

  it('rejects unknown ActivityStats fields for sidebar UI state responses', () => {
    expect(() => validate(RPC_METHODS.UI_SIDEBAR_GET, {
      threads: [],
      agents: [],
      workspace: { runs: [] },
      token_usage: { inputTokens: 0, outputTokens: 0, totalTokens: 0, usedTokens: 0 },
      activityStatsByThread: {
        'thread-1': { lspCalls: 1, commands: 2, fileEdits: 3, surprise: true },
      },
    })).toThrow('activityStatsByThread.thread-1 must not include surprise');
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
      ...response,
      surprise: true,
    })).toThrow('body must not include surprise');
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

  it('validates the complete canonical toolbridge catalog response', () => {
    expect(validate(RPC_METHODS.TOOLBRIDGE_TOOLS_LIST, {
      tools: [{
        serverName: 'lsp',
        toolName: 'lsp_edit',
        displayName: 'lsp_edit',
        description: 'Edit source',
        enabled: true,
        disabledReason: '',
      }],
    })).toEqual({
      tools: [{
        serverName: 'lsp',
        toolName: 'lsp_edit',
        displayName: 'lsp_edit',
        description: 'Edit source',
        enabled: true,
        disabledReason: '',
      }],
    });

    expect(() => validate(RPC_METHODS.TOOLBRIDGE_TOOLS_LIST, {
      tools: [{
        serverName: 'lsp',
        displayName: 'grep',
        description: '',
        enabled: true,
        disabledReason: '',
      }],
    })).toThrow('toolbridge/tools/list response tools[0].toolName must be a non-empty string');

    expect(() => validate(RPC_METHODS.TOOLBRIDGE_TOOLS_LIST, {
      tools: [{
        serverName: 'lsp',
        toolName: 'grep',
        displayName: 'grep',
        description: '',
        enabled: 'yes',
        disabledReason: '',
      }],
    })).toThrow('toolbridge/tools/list response tools[0].enabled must be a boolean');

    expect(() => validate(RPC_METHODS.TOOLBRIDGE_TOOLS_LIST, {
      tools: [],
      debug: true,
    })).toThrow('toolbridge/tools/list response body must not include debug');
  });

  it('accepts only the exact prompt history response contract', () => {
    const response = {
      entries: [{
        threadId: 'thread-1',
        messageId: 'message-1',
        text: 'duplicate prompt',
        createdAt: '2026-07-12T10:00:00Z',
      }, {
        threadId: 'thread-1',
        messageId: 'message-2',
        text: 'duplicate prompt',
        createdAt: '2026-07-12T09:00:00Z',
      }],
      nextCursor: 'cursor-1',
      hasMore: true,
      nonce: 'nonce-1',
    };

    expect(validate(RPC_METHODS.THREAD_PROMPT_HISTORY, response)).toBe(response);
    expect(() => validate(RPC_METHODS.THREAD_PROMPT_HISTORY, { ...response, debug: true }))
      .toThrow('thread/promptHistory response body must not include debug');
    expect(() => validate(RPC_METHODS.THREAD_PROMPT_HISTORY, { ...response, entries: null }))
      .toThrow('thread/promptHistory response entries must be an array');
    expect(() => validate(RPC_METHODS.THREAD_PROMPT_HISTORY, {
      ...response,
      entries: Array.from({ length: 51 }, (_, index) => ({
        ...response.entries[0],
        messageId: `message-${index}`,
      })),
    })).toThrow('thread/promptHistory response entries must not exceed 50');
    expect(() => validate(RPC_METHODS.THREAD_PROMPT_HISTORY, {
      ...response,
      entries: [{ ...response.entries[0], raw: 'secret' }],
    })).toThrow('thread/promptHistory response entries[0] must not include raw');
    expect(() => validate(RPC_METHODS.THREAD_PROMPT_HISTORY, { ...response, nonce: '' }))
      .toThrow('thread/promptHistory response nonce must be a non-empty string');
    expect(() => validate(RPC_METHODS.THREAD_PROMPT_HISTORY, { ...response, nonce: 'n'.repeat(2049) }))
      .toThrow('thread/promptHistory response nonce exceeds 2048 bytes');
    expect(() => validate(RPC_METHODS.THREAD_PROMPT_HISTORY, { ...response, nextCursor: 'c'.repeat(2049) }))
      .toThrow('thread/promptHistory response nextCursor exceeds 2048 bytes');
    expect(() => validate(RPC_METHODS.THREAD_PROMPT_HISTORY, { ...response, nextCursor: '' }))
      .toThrow('thread/promptHistory response nextCursor must be non-empty when hasMore is true');
    expect(() => validate(RPC_METHODS.THREAD_PROMPT_HISTORY, {
      ...response,
      hasMore: false,
      nextCursor: 'unexpected-cursor',
    })).toThrow('thread/promptHistory response nextCursor must be empty when hasMore is false');
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
