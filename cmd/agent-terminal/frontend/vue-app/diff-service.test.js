// @ts-nocheck
/**
 * Phase 1-5: diff.js 服务回归测试
 *
 * 覆盖 parseUnifiedDiff 和 diffStats 函数。
 */
import { describe, it, expect } from 'vitest';
import { parseUnifiedDiff, diffStats } from './services/diff.js';

// ─── parseUnifiedDiff ───────────────────────────────────────────────
describe('parseUnifiedDiff', () => {
    it('returns empty array for null/empty input', () => {
        expect(parseUnifiedDiff(null)).toEqual([]);
        expect(parseUnifiedDiff('')).toEqual([]);
        expect(parseUnifiedDiff(undefined)).toEqual([]);
    });

    it('parses single file diff', () => {
        const diff = [
            'diff --git a/src/file.js b/src/file.js',
            '--- a/src/file.js',
            '+++ b/src/file.js',
            '@@ -1,3 +1,4 @@',
            ' line1',
            '-removed',
            '+added1',
            '+added2',
            ' line3',
        ].join('\n');
        const result = parseUnifiedDiff(diff);
        expect(result).toHaveLength(1);
        expect(result[0].filename).toBe('src/file.js');
        expect(result[0].lines).toHaveLength(6); // hunk + ctx + del + add + add + ctx
    });

    it('parses multi-file diff', () => {
        const diff = [
            'diff --git a/a.js b/a.js',
            '--- a/a.js',
            '+++ b/a.js',
            '@@ -1,1 +1,1 @@',
            '-old',
            '+new',
            'diff --git a/b.js b/b.js',
            '--- a/b.js',
            '+++ b/b.js',
            '@@ -1,1 +1,1 @@',
            '-x',
            '+y',
        ].join('\n');
        const result = parseUnifiedDiff(diff);
        expect(result).toHaveLength(2);
        expect(result[0].filename).toBe('a.js');
        expect(result[1].filename).toBe('b.js');
    });

    it('parses multi-file diff without diff --git headers', () => {
        const diff = [
            '--- a/a.js',
            '+++ b/a.js',
            '@@ -1,1 +1,1 @@',
            '-old',
            '+new',
            '--- a/b.js',
            '+++ b/b.js',
            '@@ -2,1 +2,1 @@',
            '-x',
            '+y',
        ].join('\n');
        const result = parseUnifiedDiff(diff);
        expect(result.map((file) => file.filename)).toEqual(['a.js', 'b.js']);
    });

    it('parses special patch headers emitted by patch tools', () => {
        const diff = [
            '*** Begin Patch',
            '*** Update File: docs/a.md',
            '@@ -1,1 +1,1 @@',
            '-old',
            '+new',
            '*** Add File: docs/b.md',
            '+hello',
            '+world',
            '*** End Patch',
        ].join('\n');
        const result = parseUnifiedDiff(diff);
        expect(result.map((file) => file.filename)).toEqual(['docs/a.md', 'docs/b.md']);
        expect(result[0].lines[0].type).toBe('hunk');
        expect(result[1].lines.map((line) => line.type)).toEqual(['add', 'add']);
    });

    it('assigns correct line types', () => {
        const diff = [
            'diff --git a/f.js b/f.js',
            '--- a/f.js',
            '+++ b/f.js',
            '@@ -1,3 +1,3 @@',
            ' ctx',
            '-del',
            '+add',
        ].join('\n');
        const result = parseUnifiedDiff(diff);
        const types = result[0].lines.map((line) => line.type);
        expect(types).toEqual(['hunk', 'ctx', 'del', 'add']);
    });

    it('tracks line numbers correctly', () => {
        const diff = [
            'diff --git a/f.js b/f.js',
            '--- a/f.js',
            '+++ b/f.js',
            '@@ -10,3 +20,3 @@',
            ' ctx',
            '-del',
            '+add',
        ].join('\n');
        const result = parseUnifiedDiff(diff);
        const lines = result[0].lines;
        // hunk has empty line numbers
        expect(lines[0].type).toBe('hunk');
        expect(lines[0].oldNo).toBe('');
        expect(lines[0].newNo).toBe('');
        // ctx starts at 10/20
        expect(lines[1].oldNo).toBe(10);
        expect(lines[1].newNo).toBe(20);
        // del only has oldNo
        expect(lines[2].oldNo).toBe(11);
        expect(lines[2].newNo).toBe('');
        // add only has newNo
        expect(lines[3].oldNo).toBe('');
        expect(lines[3].newNo).toBe(21);
    });

    it('handles meta lines (no newline at end)', () => {
        const diff = [
            'diff --git a/f.js b/f.js',
            '--- a/f.js',
            '+++ b/f.js',
            '@@ -1,1 +1,1 @@',
            '-old',
            '+new',
            '\\ No newline at end of file',
        ].join('\n');
        const result = parseUnifiedDiff(diff);
        const metaLine = result[0].lines.find((line) => line.type === 'meta');
        expect(metaLine).toBeDefined();
        expect(metaLine.text).toContain('No newline');
    });

    it('handles +++ b/ filename override', () => {
        const diff = [
            'diff --git a/oldname.js b/newname.js',
            '--- a/oldname.js',
            '+++ b/newname.js',
            '@@ -1,1 +1,1 @@',
            ' line',
        ].join('\n');
        const result = parseUnifiedDiff(diff);
        expect(result[0].filename).toBe('newname.js');
    });
});

// ─── diffStats ──────────────────────────────────────────────────────
describe('diffStats', () => {
    it('counts add and del lines', () => {
        const file = {
            filename: 'test.js',
            lines: [
                { type: 'hunk', text: '@@' },
                { type: 'ctx', text: 'x' },
                { type: 'add', text: 'a' },
                { type: 'add', text: 'b' },
                { type: 'del', text: 'c' },
            ],
        };
        const stats = diffStats(file);
        expect(stats.add).toBe(2);
        expect(stats.del).toBe(1);
    });

    it('returns zeros for context-only diff', () => {
        const file = {
            filename: 'test.js',
            lines: [
                { type: 'hunk', text: '@@' },
                { type: 'ctx', text: 'x' },
            ],
        };
        const stats = diffStats(file);
        expect(stats.add).toBe(0);
        expect(stats.del).toBe(0);
    });
});
