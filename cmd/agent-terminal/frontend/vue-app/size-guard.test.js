// @ts-nocheck
/**
 * size-guard 集成测试 — 确保体积守卫在 vitest 中也被触发。
 *
 * 之前 size-guard 只在 `npm run build` 的 prebuild 钩子中运行，
 * 导致 `npx vitest run` 不会检测到超限违规。
 * 本测试通过 helper 回归 + 子进程执行 size-guard.cjs，将其纳入 CI/vitest 流程。
 */
import { describe, it, expect } from 'vitest';
import { execSync } from 'node:child_process';
import { createRequire } from 'node:module';
import { resolve, dirname } from 'node:path';
import { fileURLToPath } from 'node:url';

const __dirname = dirname(fileURLToPath(import.meta.url));
const SCRIPT = resolve(__dirname, '..', 'scripts', 'size-guard.cjs');
const require = createRequire(import.meta.url);
const { extractFunctions, countEffectiveLines } = require('../scripts/size-guard.cjs');

describe('size-guard helpers', () => {
  it('detects parameterless setup without leaking into nested functions', () => {
    const lines = [
      'const AppRoot = {',
      '  setup() {',
      '    async function refreshBuildInfo() {',
      '      return 1;',
      '    }',
      '    return { refreshBuildInfo };',
      '  },',
      '  template: `<div />`,',
      '};',
    ];

    expect(extractFunctions(lines)).toEqual([
      { name: 'setup', start: 2, end: 7, lines: 6 },
    ]);
  });

  it('stops object-property setup at its own closing brace', () => {
    const lines = [
      'const component = {',
      '  setup(props) {',
      '    const label = props.label;',
      '    return { label };',
      '  },',
      '  template: `<div />`,',
      '};',
    ];

    const functions = extractFunctions(lines);
    expect(functions).toEqual([
      { name: 'setup', start: 2, end: 5, lines: 4 },
    ]);
    expect(countEffectiveLines(lines, functions[0].start - 1, functions[0].end - 1)).toBe(4);
  });

  it('keeps functions open when the body brace appears on a later line', () => {
    const lines = [
      'function foo(',
      '  arg',
      ') {',
      '  return arg;',
      '}',
    ];

    const functions = extractFunctions(lines);
    expect(functions).toEqual([
      { name: 'foo', start: 1, end: 5, lines: 5 },
    ]);
    expect(countEffectiveLines(lines, 0, 4)).toBe(5);
  });

  it('keeps single-line function bodies detectable', () => {
    const lines = [
      'function foo() {}',
      'const component = {',
      '  setup() {},',
      '};',
    ];

    const functions = extractFunctions(lines);
    expect(functions).toEqual([
      { name: 'foo', start: 1, end: 1, lines: 1 },
      { name: 'setup', start: 3, end: 3, lines: 1 },
    ]);
    expect(countEffectiveLines(lines, 0, 0)).toBe(1);
    expect(countEffectiveLines(lines, 2, 2)).toBe(1);
  });
});

describe('size-guard CLI', () => {
  it('passes without violations (exit code 0)', () => {
    let exitCode = 0;
    let output = '';
    try {
      output = execSync(`node "${SCRIPT}"`, {
        cwd: resolve(__dirname, '..'),
        encoding: 'utf8',
        timeout: 15_000,
      });
    } catch (/** @type {any} */ err) {
      exitCode = err.status ?? 1;
      output = (err.stdout || '') + '\n' + (err.stderr || '');
    }

    if (exitCode !== 0) {
      console.error('size-guard 违规输出:\n' + output);
    }

    expect(exitCode, `size-guard 检查失败 (exit ${exitCode}):\n${output}`).toBe(0);
  });
});
