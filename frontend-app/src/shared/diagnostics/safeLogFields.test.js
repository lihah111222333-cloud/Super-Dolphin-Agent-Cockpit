import { describe, expect, it } from 'vitest';
import {
  SAFE_LOG_LIMITS,
  SAFE_LOG_REDACTED_VALUE,
  SAFE_LOG_TRUNCATED_VALUE,
  normalizeSafeLogFieldKey,
  redactUITestValue,
  safeLogFields,
} from './safeLogFields.js';

function nestedDepthPayload(depth, leaf) {
  let value = leaf;
  for (let index = depth; index >= 1; index -= 1) {
    value = { [`level${index}`]: value };
  }
  return value;
}

describe('safeLogFields', () => {
  const forbiddenKeys = [
    'token',
    'api_key',
    'secret',
    'authorization',
    'prompt',
    'user_prompt',
    'user_message',
    'message_text',
    'text',
    'content',
    'file_content',
    'tool_result',
    'memory',
    'skill',
    'thread_messages',
  ];

  it('redacts forbidden keys at any nesting depth', () => {
    const fields = Object.fromEntries(
      forbiddenKeys.map((key) => [key, `${key}-must-not-leak`]),
    );
    fields.nested = {
      safe: true,
      token: 'nested-token-must-not-leak',
      child: {
        userPrompt: 'camel-prompt-must-not-leak',
        toolResult: 'camel-tool-result-must-not-leak',
      },
    };

    const safe = safeLogFields(fields);

    for (const key of forbiddenKeys) {
      expect(safe[key]).toBe(SAFE_LOG_REDACTED_VALUE);
    }
    expect(safe.nested.token).toBe(SAFE_LOG_REDACTED_VALUE);
    expect(safe.nested.child.userPrompt).toBe(SAFE_LOG_REDACTED_VALUE);
    expect(safe.nested.child.toolResult).toBe(SAFE_LOG_REDACTED_VALUE);
    expect(JSON.stringify(safe)).not.toContain('must-not-leak');
  });

  it('can omit forbidden keys for bridge log compatibility', () => {
    expect(safeLogFields({
      method: 'thread/start',
      prompt: 'secret prompt',
      nested: { content: 'secret content', count: 2 },
    }, { forbiddenKeyMode: 'omit' })).toEqual({
      method: 'thread/start',
      nested: { count: 2 },
    });
  });

  it('replaces absolute local paths inside strings', () => {
    expect(safeLogFields({
      posix: '/home/me/repo/file.txt',
      mac: 'open /Users/me/repo/file.txt now',
      windows: 'C:\\Users\\me\\repo\\file.txt',
      mixed: ['before /home/me/repo/file.txt after'],
    })).toEqual({
      posix: '[path]',
      mac: 'open [path] now',
      windows: '[path]',
      mixed: ['before [path] after'],
    });
  });

  it('truncates strings longer than safe log maxStringLength', () => {
    const value = 'x'.repeat(SAFE_LOG_LIMITS.maxStringLength + 20);

    const safe = safeLogFields({ value });

    expect(safe.value).toHaveLength(SAFE_LOG_LIMITS.maxStringLength);
    expect(safe.value).toBe(`${'x'.repeat(SAFE_LOG_LIMITS.maxStringLength - 3)}...`);
  });

  it('bounds objects deeper than maxFieldDepth', () => {
    const safe = safeLogFields(nestedDepthPayload(5, { token: 'deep-token-must-not-leak' }));

    expect(safe.level1.level2.level3.level4).toBe(SAFE_LOG_TRUNCATED_VALUE);
    expect(JSON.stringify(safe)).not.toContain('deep-token-must-not-leak');
  });

  it('bounds objects and arrays wider than maxFieldCount', () => {
    const wide = Object.fromEntries(
      Array.from({ length: SAFE_LOG_LIMITS.maxFieldCount + 10 }, (_, index) => [`field_${index}`, index]),
    );
    const array = Array.from({ length: SAFE_LOG_LIMITS.maxFieldCount + 10 }, (_, index) => index);

    const safe = safeLogFields({ wide, array });

    expect(Object.keys(safe.wide)).toHaveLength(SAFE_LOG_LIMITS.maxFieldCount);
    expect(safe.wide.field_0).toBe(0);
    expect(safe.wide[`field_${SAFE_LOG_LIMITS.maxFieldCount}`]).toBeUndefined();
    expect(safe.array).toHaveLength(SAFE_LOG_LIMITS.maxFieldCount);
  });

  it('redacts unsafe scalar values without dropping safe numbers and booleans', () => {
    expect(safeLogFields({
      count: 2,
      enabled: true,
      bearer: 'Bearer abcdefghijklmnop',
      assignment: 'api_key=secret-value',
      nested: { secretText: 'sk-abcdefghijkl' },
    })).toEqual({
      count: 2,
      enabled: true,
      bearer: SAFE_LOG_REDACTED_VALUE,
      assignment: SAFE_LOG_REDACTED_VALUE,
      nested: { secretText: SAFE_LOG_REDACTED_VALUE },
    });
  });

  it('serializes errors safely', () => {
    const error = new Error('backend unavailable');
    error.code = 'ECONNREFUSED';
    error.authorization = 'Bearer abcdefghijklmnop';

    expect(safeLogFields({ error })).toEqual({
      error: {
        name: 'Error',
        message: 'backend unavailable',
        code: 'ECONNREFUSED',
        authorization: SAFE_LOG_REDACTED_VALUE,
      },
    });
  });

  it('normalizes field keys consistently', () => {
    expect(normalizeSafeLogFieldKey('apiKey')).toBe('api_key');
    expect(normalizeSafeLogFieldKey('user-prompt')).toBe('user_prompt');
    expect(normalizeSafeLogFieldKey('message.text')).toBe('message_text');
  });

  it('redactUITestValue sanitizes standalone values', () => {
    expect(redactUITestValue('/home/me/repo/file.txt')).toBe('[path]');
    expect(redactUITestValue('token=secret-value')).toBe(SAFE_LOG_REDACTED_VALUE);
  });

  it('throws on invalid inputs or unknown options', () => {
    expect(() => safeLogFields(null)).toThrow('safeLogFields fields must be a plain object');
    expect(() => safeLogFields([], {})).toThrow('safeLogFields fields must be a plain object');
    expect(() => safeLogFields({}, { unknown: true })).toThrow('safeLogFields option unknown is not supported');
    expect(() => safeLogFields({}, { forbiddenKeyMode: 'drop' })).toThrow(
      'safeLogFields forbiddenKeyMode must be redact or omit',
    );
  });
});
