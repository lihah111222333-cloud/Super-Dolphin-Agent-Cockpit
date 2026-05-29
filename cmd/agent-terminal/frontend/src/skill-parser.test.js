/**
 * Phase 1-4: SkillsPage 解析函数回归测试
 *
 * 覆盖 SkillsPage.js 中 setup() 外的所有纯解析函数。
 */
import { describe, it, expect } from 'vitest';
import {
    normalizeWordList,
    listToText,
    inferSkillNameFromPath,
    summarizeItems,
    normalizePathKey,
    fileNameFromPath,
    skillDirFromFilePath,
    isSkillMainFilePath,
    parseFrontmatter,
    parseWordsValue,
    cleanScalar,
    parseSkillMarkdown,
    quoteYAML,
    buildSkillMarkdown,
} from './pages/SkillsPage.js';

// ─── normalizeWordList ──────────────────────────────────────────────
describe('normalizeWordList', () => {
    it('returns empty for null/empty', () => {
        expect(normalizeWordList(null)).toEqual([]);
        expect(normalizeWordList('')).toEqual([]);
    });

    it('splits by comma', () => {
        expect(normalizeWordList('a, b, c')).toEqual(['a', 'b', 'c']);
    });

    it('deduplicates', () => {
        const result = normalizeWordList('a, b, a');
        expect(result).toEqual(['a', 'b']);
    });

    it('handles newlines', () => {
        const result = normalizeWordList('hello\nworld');
        expect(result.length).toBeGreaterThan(0);
    });
});

// ─── listToText ─────────────────────────────────────────────────────
describe('listToText', () => {
    it('returns empty for empty array', () => {
        expect(listToText([])).toBe('');
    });

    it('joins with comma-space', () => {
        expect(listToText(['a', 'b', 'c'])).toBe('a, b, c');
    });

    it('handles non-array', () => {
        expect(listToText(null)).toBe('');
    });
});

// ─── inferSkillNameFromPath ─────────────────────────────────────────
describe('inferSkillNameFromPath', () => {
    it('returns empty for empty path', () => {
        expect(inferSkillNameFromPath('')).toBe('');
    });

    it('extracts last path segment', () => {
        expect(inferSkillNameFromPath('/path/to/MySkill')).toBe('MySkill');
    });

    it('strips trailing slashes', () => {
        expect(inferSkillNameFromPath('/path/to/MySkill/')).toBe('MySkill');
    });
});

// ─── summarizeItems ─────────────────────────────────────────────────
describe('summarizeItems', () => {
    it('returns empty for empty array', () => {
        expect(summarizeItems([])).toBe('');
    });

    it('summarizes with count', () => {
        const result = summarizeItems(['a', 'b', 'c', 'd', 'e'], 3);
        expect(result).toContain('a');
        expect(result).toContain('5 项');
    });
});

// ─── normalizePathKey ───────────────────────────────────────────────
describe('normalizePathKey', () => {
    it('lowercases and converts slashes', () => {
        expect(normalizePathKey('Src\\Utils\\File.JS')).toBe('src/utils/file.js');
    });

    it('trims whitespace', () => {
        expect(normalizePathKey('  path  ')).toBe('path');
    });
});

// ─── fileNameFromPath ───────────────────────────────────────────────
describe('fileNameFromPath', () => {
    it('extracts filename', () => {
        expect(fileNameFromPath('/path/to/file.txt')).toBe('file.txt');
    });

    it('handles Windows paths', () => {
        expect(fileNameFromPath('C:\\Users\\file.txt')).toBe('file.txt');
    });

    it('returns empty for empty', () => {
        expect(fileNameFromPath('')).toBe('');
    });
});

// ─── skillDirFromFilePath ───────────────────────────────────────────
describe('skillDirFromFilePath', () => {
    it('returns parent directory', () => {
        expect(skillDirFromFilePath('/skills/MySkill/SKILL.md')).toBe('/skills/MySkill');
    });

    it('handles Windows paths', () => {
        expect(skillDirFromFilePath('C:\\skills\\MySkill\\SKILL.md')).toBe('C:\\skills\\MySkill');
    });

    it('returns empty for empty', () => {
        expect(skillDirFromFilePath('')).toBe('');
    });
});

// ─── isSkillMainFilePath ────────────────────────────────────────────
describe('isSkillMainFilePath', () => {
    it('matches SKILL.md', () => {
        expect(isSkillMainFilePath('/path/SKILL.md')).toBe(true);
        expect(isSkillMainFilePath('SKILL.md')).toBe(true);
    });

    it('case insensitive', () => {
        expect(isSkillMainFilePath('skill.md')).toBe(true);
    });

    it('rejects non-SKILL.md', () => {
        expect(isSkillMainFilePath('README.md')).toBe(false);
    });
});

// ─── parseFrontmatter ───────────────────────────────────────────────
describe('parseFrontmatter', () => {
    it('returns full text as body when no frontmatter', () => {
        const result = parseFrontmatter('Hello World');
        expect(result.attrs).toEqual({});
        expect(result.body).toBe('Hello World');
    });

    it('parses YAML frontmatter', () => {
        const content = '---\nname: TestSkill\ndescription: A skill\n---\n# Body';
        const result = parseFrontmatter(content);
        expect(result.attrs.name).toBe('TestSkill');
        expect(result.attrs.description).toBe('A skill');
        expect(result.body.trim()).toBe('# Body');
    });

    it('handles empty frontmatter', () => {
        const content = '---\n---\nBody';
        const result = parseFrontmatter(content);
        expect(result.attrs).toEqual({});
        expect(result.body).toContain('Body');
    });
});

// ─── parseWordsValue ────────────────────────────────────────────────
describe('parseWordsValue', () => {
    it('splits comma-separated string', () => {
        expect(parseWordsValue('a, b, c')).toEqual(['a', 'b', 'c']);
    });

    it('handles array input', () => {
        expect(parseWordsValue(['x', 'y'])).toEqual(['x', 'y']);
    });

    it('returns empty for empty input', () => {
        expect(parseWordsValue('')).toEqual([]);
    });
});

// ─── cleanScalar ────────────────────────────────────────────────────
describe('cleanScalar', () => {
    it('strips surrounding quotes', () => {
        expect(cleanScalar('"hello"')).toBe('hello');
        expect(cleanScalar("'hello'")).toBe('hello');
    });

    it('trims whitespace', () => {
        expect(cleanScalar('  hello  ')).toBe('hello');
    });

    it('handles empty/null', () => {
        expect(cleanScalar(null)).toBe('');
        expect(cleanScalar('')).toBe('');
    });
});

// ─── parseSkillMarkdown ─────────────────────────────────────────────
describe('parseSkillMarkdown', () => {
    it('parses complete SKILL.md', () => {
        const content = '---\nname: MySkill\ndisplay_name: My Skill\ndescription: Does things\ntags: [go, js]\n---\n# Body\nSome content';
        const result = parseSkillMarkdown(content);
        expect(result.name).toBe('MySkill');
        expect(result.displayName).toBe('My Skill');
        expect(result.description).toBe('Does things');
        expect(result.body).toContain('# Body');
    });

    it('parses title as a display name alias', () => {
        const result = parseSkillMarkdown('---\nname: docker-container-deploy\ntitle: Docker 容器化部署\n---\nbody');
        expect(result.displayName).toBe('Docker 容器化部署');
    });

    it('converts safe legacy display names into runtime names', () => {
        const result = parseSkillMarkdown('---\nname: Agent 工程学\n---\nbody');
        expect(result.name).toBe('agent-工程学');
        expect(result.displayName).toBe('Agent 工程学');
    });

    it('uses fallback name', () => {
        const result = parseSkillMarkdown('---\ndescription: test\n---\nbody', 'Fallback');
        expect(result.name).toBe('Fallback');
    });

    it('ignores internal marker summaries', () => {
        expect(parseSkillMarkdown('---\nsummary: <SUBAGENT-STOP>\n---\nbody').summary).toBe('');
    });

    it('handles empty content', () => {
        const result = parseSkillMarkdown('');
        expect(result.name).toBe('');
        expect(result.body).toBe('');
    });
});

// ─── quoteYAML ──────────────────────────────────────────────────────
describe('quoteYAML', () => {
    it('wraps in double quotes', () => {
        expect(quoteYAML('hello')).toBe('"hello"');
    });

    it('escapes inner double quotes', () => {
        expect(quoteYAML('say "hi"')).toBe('"say \\"hi\\""');
    });

    it('handles empty', () => {
        expect(quoteYAML('')).toBe('""');
    });
});

describe('buildSkillMarkdown', () => {
    it('writes display_name separately from the runtime name', () => {
        const markdown = buildSkillMarkdown({
            name: 'docker-container-deploy',
            displayName: 'Docker 容器化部署',
            description: '当你需要部署容器时使用。',
            triggerWordsText: '',
            forceWordsText: '',
            internalScenarioWordsText: '',
            body: '## 使用\n\nDeploy.',
        });
        expect(markdown).toContain('name: "docker-container-deploy"');
        expect(markdown).toContain('display_name: "Docker 容器化部署"');
    });
});

// ─── buildSkillMarkdown ─────────────────────────────────────────────
describe('buildSkillMarkdown', () => {
    it('builds complete skill markdown', () => {
        const result = buildSkillMarkdown({
            name: 'TestSkill',
            description: 'A test skill',
            body: '# Instructions\nDo stuff',
        });
        expect(result).toContain('---');
        expect(result).toContain('name:');
        expect(result).toContain('TestSkill');
        expect(result).toContain('description:');
        expect(result).toContain('# Instructions');
    });

    it('handles minimal form', () => {
        const result = buildSkillMarkdown({ name: 'Min' });
        expect(result).toContain('Min');
    });

    it('saves legacy summary text as description and does not write summary frontmatter', () => {
        const result = buildSkillMarkdown({
            name: 'LegacySummarySkill',
            summary: 'Use when working with old skill metadata',
            body: '# Body',
        });

        expect(result).toContain('description: "Use when working with old skill metadata"');
        expect(result).not.toContain('summary:');
    });

    it('migrates legacy force words into trigger words on save', () => {
        const result = buildSkillMarkdown({
            name: 'MigratedSkill',
            triggerWordsText: 'bug, 调试, @MigratedSkill',
            forceWordsText: '必须, bug',
            internalScenarioWordsText: '[skill:MigratedSkill]',
            body: '# Body',
        });

        expect(result).toContain('trigger_words: ["bug", "调试", "@MigratedSkill", "必须", "[skill:MigratedSkill]"]');
        expect(result).not.toContain('force_words');
    });
});
