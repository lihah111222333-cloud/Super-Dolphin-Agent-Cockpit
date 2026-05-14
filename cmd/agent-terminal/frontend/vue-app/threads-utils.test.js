/**
 * Phase 1-2: threads.js Store 纯函数回归测试
 *
 * 覆盖 threads.js 中所有将被提取的纯工具函数。
 */
import { describe, it, expect } from 'vitest';
import {
    normalizeEpochMillis,
    parseEpochMillis,
    parseThreadCreatedAtFromID,
    normalizePreferenceScopeCwd,
    withPreferenceScope,
    shouldSyncAfterPreferencePersist,
    normalizeThreadID,
    toNormalizedEventString,
    getBridgeEventThreadId,
    getBridgeEventMethod,
    getBridgeEventType,
    getBridgeEventCommand,
    collectBridgeEventItemKinds,
    isContextCompactionItemKind,
    isCompactCommand,
    normalizeSplitRatio,
    normalizeThreadRailWidth,
    normalizeCmdCardCols,
    normalizeThread,
    normalizeThreadTimestampMap,
    ensureThreadOrderIndex,
    sortThreadsByStableFirstSeen,
} from './stores/threads.js';

// ─── normalizeEpochMillis ───────────────────────────────────────────
describe('normalizeEpochMillis', () => {
    it('returns 0 for non-finite or negative', () => {
        expect(normalizeEpochMillis(NaN)).toBe(0);
        expect(normalizeEpochMillis(-1)).toBe(0);
        expect(normalizeEpochMillis(0)).toBe(0);
    });

    it('converts 10-digit unix seconds to milliseconds', () => {
        expect(normalizeEpochMillis(1700000000)).toBe(1700000000000);
    });

    it('keeps 13-digit milliseconds as-is', () => {
        expect(normalizeEpochMillis(1700000000000)).toBe(1700000000000);
    });
});

// ─── parseEpochMillis ───────────────────────────────────────────────
describe('parseEpochMillis', () => {
    it('returns 0 for null/undefined/empty', () => {
        expect(parseEpochMillis(null)).toBe(0);
        expect(parseEpochMillis(undefined)).toBe(0);
        expect(parseEpochMillis('')).toBe(0);
    });

    it('parses numeric value', () => {
        expect(parseEpochMillis(1700000000000)).toBe(1700000000000);
    });

    it('parses numeric string', () => {
        expect(parseEpochMillis('1700000000000')).toBe(1700000000000);
    });

    it('parses ISO date string', () => {
        const ts = parseEpochMillis('2024-01-01T00:00:00Z');
        expect(ts).toBeGreaterThan(0);
    });

    it('returns 0 for invalid string', () => {
        expect(parseEpochMillis('not-a-date')).toBe(0);
    });
});

// ─── parseThreadCreatedAtFromID ─────────────────────────────────────
describe('parseThreadCreatedAtFromID', () => {
    it('returns 0 for empty ID', () => {
        expect(parseThreadCreatedAtFromID('')).toBe(0);
    });

    it('returns 0 for purely text ID', () => {
        expect(parseThreadCreatedAtFromID('thread-abc')).toBe(0);
    });

    it('extracts timestamp from ID with embedded epoch', () => {
        const ts = parseThreadCreatedAtFromID('thread-1700000000000-abc');
        expect(ts).toBe(1700000000000);
    });

    it('parses 19-digit nanosecond agent ids from idgen.NewAgentID()', () => {
        // Regression: Go's time.Now().UnixNano() produces 19-digit chunks
        // which the old 16-digit upper bound rejected, sending every fresh
        // thread to the bottom of the chat rail.
        expect(parseThreadCreatedAtFromID('agent_1778748074684743000')).toBe(1778748074684);
        // Child agent suffix (`-<seq>`) must not break parent ns parsing.
        expect(parseThreadCreatedAtFromID('agent_1778748074684743000-1')).toBe(1778748074684);
    });

    it('still parses legacy 13-digit ms agent ids with hex suffix', () => {
        // idgen.NewID('agent') format used by codex sessions:
        // agent_<13-digit ms>_<8-byte hex>. Must remain unchanged.
        expect(parseThreadCreatedAtFromID('agent_1774720455588_04820e21fd876e3b')).toBe(1774720455588);
    });

    it('rejects 20+ digit chunks as out-of-range', () => {
        expect(parseThreadCreatedAtFromID('thread-99999999999999999999')).toBe(0);
    });
});

// ─── normalizePreferenceScopeCwd ────────────────────────────────────
describe('normalizePreferenceScopeCwd', () => {
    it('returns empty for null/empty/dot', () => {
        expect(normalizePreferenceScopeCwd(null)).toBe('');
        expect(normalizePreferenceScopeCwd('')).toBe('');
        expect(normalizePreferenceScopeCwd('.')).toBe('');
    });

    it('strips trailing slashes', () => {
        expect(normalizePreferenceScopeCwd('/home/user/')).toBe('/home/user');
        expect(normalizePreferenceScopeCwd('/home/user\\')).toBe('/home/user');
    });
});

// ─── withPreferenceScope ────────────────────────────────────────────
describe('withPreferenceScope', () => {
    it('returns copy of payload without cwd when scope is empty', () => {
        const result = withPreferenceScope({ key: 'test' });
        expect(result).toEqual({ key: 'test' });
    });

    it('returns empty object for non-object input', () => {
        // @ts-expect-error intentional null input regression coverage
        const result = withPreferenceScope(null);
        expect(result).toEqual({});
    });
});

// ─── shouldSyncAfterPreferencePersist ───────────────────────────────
describe('shouldSyncAfterPreferencePersist', () => {
    it('skips immediate sync for active thread selection keys', () => {
        expect(shouldSyncAfterPreferencePersist('activeThreadId')).toBe(false);
        expect(shouldSyncAfterPreferencePersist('activeCmdThreadId')).toBe(false);
    });

    it('keeps sync for other preference keys', () => {
        expect(shouldSyncAfterPreferencePersist('viewPrefs.cmd')).toBe(true);
        expect(shouldSyncAfterPreferencePersist('viewPrefs.chat')).toBe(true);
    });
});


// ─── normalizeThreadID ──────────────────────────────────────────────
describe('normalizeThreadID', () => {
    it('trims whitespace', () => {
        expect(normalizeThreadID('  thread-1  ')).toBe('thread-1');
    });

    it('handles null/undefined', () => {
        expect(normalizeThreadID(null)).toBe('');
        expect(normalizeThreadID(undefined)).toBe('');
    });
});

// ─── toNormalizedEventString ────────────────────────────────────────
describe('toNormalizedEventString', () => {
    it('lowercases and trims', () => {
        expect(toNormalizedEventString('  HELLO  ')).toBe('hello');
    });

    it('handles null', () => {
        expect(toNormalizedEventString(null)).toBe('');
    });
});

// ─── getBridgeEventThreadId ─────────────────────────────────────────
describe('getBridgeEventThreadId', () => {
    it('finds threadId at top level', () => {
        expect(getBridgeEventThreadId({ threadId: 'abc' })).toBe('abc');
    });

    it('finds thread_id at top level', () => {
        expect(getBridgeEventThreadId({ thread_id: 'abc' })).toBe('abc');
    });

    it('finds threadId in params', () => {
        expect(getBridgeEventThreadId({ params: { threadId: 'abc' } })).toBe('abc');
    });

    it('finds threadId in payload', () => {
        expect(getBridgeEventThreadId({ payload: { threadId: 'abc' } })).toBe('abc');
    });

    it('finds threadId in nested item', () => {
        expect(getBridgeEventThreadId({ item: { threadId: 'abc' } })).toBe('abc');
    });

    it('returns empty for no match', () => {
        expect(getBridgeEventThreadId({})).toBe('');
        expect(getBridgeEventThreadId({ other: 'value' })).toBe('');
    });
});

// ─── getBridgeEventMethod ───────────────────────────────────────────
describe('getBridgeEventMethod', () => {
    it('finds method at top level', () => {
        expect(getBridgeEventMethod({ method: 'send' })).toBe('send');
    });

    it('finds method in params', () => {
        expect(getBridgeEventMethod({ params: { method: 'send' } })).toBe('send');
    });

    it('returns empty for no method', () => {
        expect(getBridgeEventMethod({})).toBe('');
    });
});

// ─── getBridgeEventType ─────────────────────────────────────────────
describe('getBridgeEventType', () => {
    it('finds type in payload', () => {
        expect(getBridgeEventType({ payload: { type: 'message' } })).toBe('message');
    });

    it('finds type at top level', () => {
        expect(getBridgeEventType({ type: 'event' })).toBe('event');
    });

    it('returns empty for no type', () => {
        expect(getBridgeEventType({})).toBe('');
    });
});

// ─── getBridgeEventCommand ──────────────────────────────────────────
describe('getBridgeEventCommand', () => {
    it('finds command at top level', () => {
        expect(getBridgeEventCommand({ command: '/compact' })).toBe('/compact');
    });

    it('finds cmd at top level', () => {
        expect(getBridgeEventCommand({ cmd: '/compact' })).toBe('/compact');
    });

    it('finds uiCommand in params', () => {
        expect(getBridgeEventCommand({ params: { uiCommand: 'run' } })).toBe('run');
    });

    it('returns empty for no command', () => {
        expect(getBridgeEventCommand({})).toBe('');
    });
});

// ─── collectBridgeEventItemKinds ────────────────────────────────────
describe('collectBridgeEventItemKinds', () => {
    it('collects from multiple sources', () => {
        const result = collectBridgeEventItemKinds({
            item: { type: 'tool', kind: 'write' },
            type: 'event',
        });
        expect(result).toContain('tool');
        expect(result).toContain('write');
        expect(result).toContain('event');
    });

    it('filters empty values', () => {
        const result = collectBridgeEventItemKinds({ item: { type: '' } });
        expect(result.every(v => v.length > 0)).toBe(true);
    });
});

// ─── isContextCompactionItemKind ────────────────────────────────────
describe('isContextCompactionItemKind', () => {
    it('matches context_compaction', () => {
        expect(isContextCompactionItemKind('context_compaction')).toBe(true);
    });

    it('matches contextCompaction (camelCase)', () => {
        expect(isContextCompactionItemKind('contextCompaction')).toBe(true);
    });

    it('matches context-compaction (kebab)', () => {
        expect(isContextCompactionItemKind('context-compaction')).toBe(true);
    });

    it('matches contextcompacted', () => {
        expect(isContextCompactionItemKind('contextcompacted')).toBe(true);
    });

    it('rejects unrelated values', () => {
        expect(isContextCompactionItemKind('message')).toBe(false);
        expect(isContextCompactionItemKind('')).toBe(false);
    });
});

// ─── isCompactCommand ───────────────────────────────────────────────
describe('isCompactCommand', () => {
    it('matches /compact', () => {
        expect(isCompactCommand('/compact')).toBe(true);
    });

    it('matches with extra spaces', () => {
        expect(isCompactCommand(' / compact ')).toBe(true);
    });

    it('rejects other commands', () => {
        expect(isCompactCommand('/send')).toBe(false);
        expect(isCompactCommand('')).toBe(false);
    });
});

// ─── normalizeSplitRatio ────────────────────────────────────────────
describe('normalizeSplitRatio', () => {
    it('clamps to [30, 75]', () => {
        expect(normalizeSplitRatio(10)).toBe(30);
        expect(normalizeSplitRatio(90)).toBe(75);
        expect(normalizeSplitRatio(50)).toBe(50);
    });

    it('returns default 60 for NaN', () => {
        expect(normalizeSplitRatio(NaN)).toBe(60);
        expect(normalizeSplitRatio('abc')).toBe(60);
    });
});

// ─── normalizeThreadRailWidth ───────────────────────────────────────
describe('normalizeThreadRailWidth', () => {
    it('clamps to [188, 420]', () => {
        expect(normalizeThreadRailWidth(100)).toBe(188);
        expect(normalizeThreadRailWidth(500)).toBe(420);
        expect(normalizeThreadRailWidth(300)).toBe(300);
    });

    it('returns default 232 for NaN', () => {
        expect(normalizeThreadRailWidth(NaN)).toBe(232);
    });
});

// ─── normalizeCmdCardCols ───────────────────────────────────────────
describe('normalizeCmdCardCols', () => {
    it('returns 2 for input 2', () => {
        expect(normalizeCmdCardCols(2)).toBe(2);
    });

    it('returns 3 for any other value', () => {
        expect(normalizeCmdCardCols(3)).toBe(3);
        expect(normalizeCmdCardCols(1)).toBe(3);
        expect(normalizeCmdCardCols(null)).toBe(3);
    });
});

// ─── normalizeThread ────────────────────────────────────────────────
describe('normalizeThread', () => {
    it('normalizes complete thread', () => {
        const result = normalizeThread({ id: 'abc', name: 'MyThread', state: 'running' });
        expect(result.id).toBe('abc');
        expect(result.name).toBe('MyThread');
        expect(result.state).toBe('running');
    });

    it('keeps missing name empty', () => {
        const result = normalizeThread({ id: 'abc' });
        expect(result.name).toBe('');
    });

    it('handles null', () => {
        const result = normalizeThread(null);
        expect(result.id).toBe('');
        expect(result.name).toBe('');
    });
});

// ─── normalizeThreadTimestampMap ────────────────────────────────────
describe('normalizeThreadTimestampMap', () => {
    it('returns empty for non-object', () => {
        expect(normalizeThreadTimestampMap(null)).toEqual({});
        expect(normalizeThreadTimestampMap([])).toEqual({});
    });

    it('filters invalid entries', () => {
        const result = normalizeThreadTimestampMap({
            'a': 1000,
            'b': -1,
            '': 500,
            'c': 'invalid',
        });
        expect(result).toEqual({ a: 1000 });
    });
});

// ─── ensureThreadOrderIndex ─────────────────────────────────────────
describe('ensureThreadOrderIndex', () => {
    it('returns MAX_SAFE_INTEGER for empty ID', () => {
        expect(ensureThreadOrderIndex('')).toBe(Number.MAX_SAFE_INTEGER);
    });

    it('returns stable index for same ID', () => {
        const idx = ensureThreadOrderIndex('test-stable');
        expect(ensureThreadOrderIndex('test-stable')).toBe(idx);
    });

    it('returns incrementing index for new IDs', () => {
        const a = ensureThreadOrderIndex('order-a');
        const b = ensureThreadOrderIndex('order-b');
        expect(b).toBeGreaterThan(a);
    });
});

// ─── sortThreadsByStableFirstSeen ───────────────────────────────────
describe('sortThreadsByStableFirstSeen', () => {
    it('returns empty array for non-array', () => {
        expect(sortThreadsByStableFirstSeen(null)).toEqual([]);
    });

    it('returns same for single item', () => {
        const input = [{ id: 'x' }];
        expect(sortThreadsByStableFirstSeen(input)).toEqual(input);
    });

    it('keeps first-seen order', () => {
        // Force registration order
        ensureThreadOrderIndex('sort-first');
        ensureThreadOrderIndex('sort-second');
        const result = sortThreadsByStableFirstSeen([
            { id: 'sort-second' },
            { id: 'sort-first' },
        ]);
        expect(result[0].id).toBe('sort-first');
        expect(result[1].id).toBe('sort-second');
    });
});
