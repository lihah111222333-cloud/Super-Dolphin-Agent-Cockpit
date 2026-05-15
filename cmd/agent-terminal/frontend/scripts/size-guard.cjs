#!/usr/bin/env node
/**
 * size-guard.js — 代码体积守卫
 *
 * 规则：
 *   1. 超过 FILE_LINE_LIMIT 行的文件不允许增加行数（基线锁定）
 *   2. 低于 FILE_LINE_LIMIT 行的文件不允许突破该阈值
 *   3. 超过 FUNC_LINE_LIMIT 行的函数不允许增加行数（基线锁定）
 *   4. 低于 FUNC_LINE_LIMIT 行的函数不允许突破该阈值
 *   5. 函数内嵌套深度不允许超过 NESTING_DEPTH_LIMIT 层
 *
 * 用法：
 *   node scripts/size-guard.js          # 检查，失败返回 exit 1
 *   node scripts/size-guard.js --update  # 用当前值更新基线
 *
 * @module size-guard
 */

'use strict';

const fs = require('fs');
const path = require('path');

// ─── 阈值 ───────────────────────────────────────────────────────────
const FILE_LINE_LIMIT = 800;
const FUNC_LINE_LIMIT = 250;
const NESTING_DEPTH_LIMIT = 5;

// ─── 扫描范围 ────────────────────────────────────────────────────────
const SCAN_DIR = path.resolve(__dirname, '..', 'vue-app');
const BASELINE_PATH = path.resolve(__dirname, 'size-guard-baseline.json');
const TEST_BASELINE_PATH = path.resolve(__dirname, 'size-guard-baseline-test.json');
const IGNORE_PATTERNS = [
    /node_modules/,
    /\/lib\//,
    /\/dist\//,
];

function isTestFile(rel) {
    return /\.test\.js$/.test(rel) || /\.spec\.js$/.test(rel);
}

// ─── 工具函数 ────────────────────────────────────────────────────────

function collectJsFiles(dir, result = []) {
    for (const entry of fs.readdirSync(dir, { withFileTypes: true })) {
        const full = path.join(dir, entry.name);
        if (entry.isDirectory()) {
            collectJsFiles(full, result);
        } else if (entry.isFile() && /\.js$/.test(entry.name)) {
            const rel = path.relative(SCAN_DIR, full);
            if (!IGNORE_PATTERNS.some((re) => re.test(rel))) {
                result.push({ abs: full, rel });
            }
        }
    }
    return result;
}

/**
 * countEffectiveLines — 只计算有效代码行，排除：
 *   - 空行
 *   - // 行注释
 *   - /* ... *\/ 块注释（支持跨行）
 * @param {string[]} lines - 文件按行分割的数组
 * @param {number} [startIdx=0] - 起始下标（含）
 * @param {number} [endIdx]     - 结束下标（含），默认为最后一行
 * @returns {number}
 */
function countEffectiveLines(lines, startIdx = 0, endIdx = lines.length - 1) {
    let count = 0;
    let inBlock = false;
    for (let i = startIdx; i <= endIdx; i++) {
        const line = lines[i].trim();
        if (line === '') continue;
        if (inBlock) {
            const closeIdx = line.indexOf('*/');
            if (closeIdx >= 0) {
                inBlock = false;
                const rest = line.slice(closeIdx + 2).trim();
                if (rest !== '' && !rest.startsWith('//')) count++;
            }
            // whole line is inside block comment
            continue;
        }
        if (line.startsWith('//')) continue;
        if (line.startsWith('/*')) {
            const closeIdx = line.indexOf('*/', 2);
            if (closeIdx >= 0) {
                // single-line block comment: /* ... */
                const rest = line.slice(closeIdx + 2).trim();
                if (rest !== '' && !rest.startsWith('//')) count++;
            } else {
                inBlock = true;
            }
            continue;
        }
        count++;
    }
    return count;
}

/**
 * 简易函数边界检测（针对 Vue SFC 的 JS 组件）。
 * 不做完整 AST 解析，用花括号深度追踪 top-level 函数。
 */
function extractFunctions(lines) {
    const functions = [];
    let depth = 0;
    let funcName = '';
    let funcStart = 0;

    // 匹配 top-level 函数声明 / export function / setup()
    const FUNC_RE = /^(?:export\s+)?(?:async\s+)?function\s+(\w+)\s*\(/;
    const SETUP_RE = /^\s*setup\s*\(/;
    const ARROW_ASSIGN_RE = /^(?:export\s+)?(?:const|let|var)\s+(\w+)\s*=\s*(?:async\s+)?\(/;
    let funcEntryDepth = 0;
    let sawBodyBrace = false;

    for (let i = 0; i < lines.length; i++) {
        const line = lines[i];
        const trimmed = line.trimStart();

        // 跳过注释行
        if (trimmed.startsWith('//') || trimmed.startsWith('*') || trimmed.startsWith('/*')) {
            continue;
        }

        // 只在 top-level (depth 0 或 depth 1 用于对象内的 setup) 检测函数开始
        if (!funcName) {
            const funcMatch = trimmed.match(FUNC_RE);
            const setupMatch = !funcMatch && depth <= 1 && trimmed.match(SETUP_RE);
            const arrowMatch = !funcMatch && !setupMatch && depth === 0 && trimmed.match(ARROW_ASSIGN_RE);

            if (funcMatch) {
                funcName = funcMatch[1];
                funcStart = i;
                funcEntryDepth = depth;
                sawBodyBrace = false;
            } else if (setupMatch) {
                funcName = 'setup';
                funcStart = i;
                funcEntryDepth = depth;
                sawBodyBrace = false;
            } else if (arrowMatch) {
                // 只追踪包含 { 的箭头函数（多行体 / 单行体）
                if (line.includes('{')) {
                    funcName = arrowMatch[1];
                    funcStart = i;
                    funcEntryDepth = depth;
                    sawBodyBrace = false;
                }
            }
        }

        let lineEnteredBody = false;
        for (const ch of line) {
            if (ch === '{') {
                depth++;
                if (funcName && depth > funcEntryDepth) {
                    lineEnteredBody = true;
                }
            }
            if (ch === '}') depth--;
        }

        if (depth < 0) depth = 0;
        if (funcName && lineEnteredBody) sawBodyBrace = true;

        if (funcName && sawBodyBrace && depth === funcEntryDepth) {
            const len = i - funcStart + 1;
            functions.push({ name: funcName, start: funcStart + 1, end: i + 1, lines: len });
            funcName = '';
            funcEntryDepth = 0;
            sawBodyBrace = false;
        }
    }

    return functions;
}

/**
 * measureNestingDepth — 测量函数体内的最大嵌套深度。
 * 以函数体 { 为第 1 层，每多一层 { 深度 +1。
 * 跳过字符串字面量和注释中的花括号，减少误报。
 * @param {string[]} lines - 文件按行分割的数组
 * @param {number} startIdx - 函数起始下标（0-indexed，含）
 * @param {number} endIdx   - 函数结束下标（0-indexed，含）
 * @returns {{ maxDepth: number, maxDepthLine: number }} maxDepthLine 是 1-indexed
 */
function measureNestingDepth(lines, startIdx, endIdx) {
    let depth = 0;
    let maxDepth = 0;
    let maxDepthLine = startIdx + 1;
    let inBlock = false;  // 块注释
    let bodyFound = false;
    let bodyEntryDepth = -1;

    for (let i = startIdx; i <= endIdx; i++) {
        const line = lines[i];

        // 逐字符扫描，跳过字符串和注释
        let inString = false;
        let stringChar = '';
        let j = 0;
        while (j < line.length) {
            const ch = line[j];
            const next = j + 1 < line.length ? line[j + 1] : '';

            // 块注释状态
            if (inBlock) {
                if (ch === '*' && next === '/') {
                    inBlock = false;
                    j += 2;
                    continue;
                }
                j++;
                continue;
            }

            // 行注释
            if (ch === '/' && next === '/') break;

            // 块注释开始
            if (ch === '/' && next === '*') {
                inBlock = true;
                j += 2;
                continue;
            }

            // 字符串
            if (inString) {
                if (ch === '\\') { j += 2; continue; }
                if (ch === stringChar) inString = false;
                j++;
                continue;
            }
            if (ch === '"' || ch === "'" || ch === '`') {
                inString = true;
                stringChar = ch;
                j++;
                continue;
            }

            // 花括号
            if (ch === '{') {
                depth++;
                if (!bodyFound) {
                    bodyFound = true;
                    bodyEntryDepth = depth;
                }
                if (bodyFound) {
                    const relDepth = depth - bodyEntryDepth;
                    if (relDepth > maxDepth) {
                        maxDepth = relDepth;
                        maxDepthLine = i + 1;
                    }
                }
            }
            if (ch === '}') depth--;
            j++;
        }
    }
    return { maxDepth, maxDepthLine };
}

function loadBaselineFrom(p) {
    if (!fs.existsSync(p)) return {};
    try {
        return JSON.parse(fs.readFileSync(p, 'utf8'));
    } catch {
        return {};
    }
}

function saveBaselineTo(p, data) {
    fs.writeFileSync(p, JSON.stringify(data, null, 2) + '\n', 'utf8');
}

/**
 * detectBadParadigms — 检测已知的坏代码范式
 * @param {string[]} lines - 文件按行分割的数组
 * @param {string} rel - 文件相对路径
 * @returns {Array<{file: string, line: number, message: string}>}
 */
function detectBadParadigms(lines, rel) {
    const localViolations = [];
    for (let i = 0; i < lines.length; i++) {
        const line = lines[i];
        const trimmed = line.trim();
        if (trimmed.startsWith('//') || trimmed.startsWith('/*') || trimmed.startsWith('*')) continue;

        // 移除字符串内容，减少误判
        const noStr = line.replace(/(["'`]).*?(?<!\\)\1/g, '""');
        // 移除可选链 ?. 和空值合并 ??
        const noOpt = noStr.replace(/\?\./g, '');
        const noNullish = noOpt.replace(/\?\?/g, '');

        // 1. 嵌套三元表达式 (Nested Ternary)
        const qCount = (noNullish.match(/\?/g) || []).length;
        const cCount = (noNullish.match(/:/g) || []).length;
        if (qCount >= 2 && cCount >= 2) {
            if (/\?.*?\?.*?:/.test(noNullish) || /\?.*?:.*?\?.*?:/.test(noNullish)) {
                localViolations.push({
                    file: rel,
                    line: i + 1,
                    message: `[坏范式] 嵌套三元表达式 (Nested Ternary)。请改用 if-else 或抽取变量，以提升可读性。`,
                });
            }
        }

        // 2. 冗余的条件对象展开
        if (/\.\.\.\s*\(.*?\?.*?\{.*?\}.*?:\s*\{\}\s*\)/.test(noStr)) {
            localViolations.push({
                file: rel,
                line: i + 1,
                message: `[坏范式] 冗余的内联条件对象展开 \`...(cond ? {k:v} : {})\`。请在外部提前判断并赋值。`,
            });
        }

        // 3. 伪 LRU 缓存清理
        if (/\.cache\.delete\(.*?\.cache\.keys\(\)\.next\(\)\.value\)/.test(noStr)) {
            localViolations.push({
                file: rel,
                line: i + 1,
                message: `[坏范式] Map 伪 LRU 删除逻辑 \`cache.delete(cache.keys().next().value)\`。此方式在命中时未更新插入顺序，会导致活跃数据被踢出。`,
            });
        }

        // 4. 纳秒大数隐式截断守卫 (BigInt Precision Loss)
        if (/(?:parseInt|Number)\s*\([^)]*(?:agent_id|trace_id|timestamp|_ts)\b[^)]*\)/i.test(noStr)) {
            localViolations.push({
                file: rel,
                line: i + 1,
                message: `[坏范式] 严禁对含有 id/ts 等纳秒级字段使用 parseInt/Number() 强转，会导致 19 位精度丢失错排。请使用 BigInt 或字符串字典序。`,
            });
        }

        // 5. CWD 全局状态逃逸守卫 (Cross-Project State Pollution)
        if (/callAPI\s*\(\s*['"](?:ui\/(?:dashboard|memory|window)|threads\/)[^'"]*['"]\s*,\s*\{(?![^}]*\bcwd\b)[^}]*\}/.test(noStr)) {
            localViolations.push({
                file: rel,
                line: i + 1,
                message: `[坏范式] 全局状态逃逸。调用特定多项目敏感接口时，单行 Payload 必须显式携带 cwd 参数。`,
            });
        }
    }
    return localViolations;
}

// ─── 主逻辑 ──────────────────────────────────────────────────────────

/**
 * scanFilesWithBaseline — 扫描文件列表，对比 baseline 生成违规/收缩/嵌套报告。
 * 返回 { newBaseline, violations, autoShrunk, nestingViolations }
 */
function scanFilesWithBaseline(files, baseline) {
    const violations = [];
    const autoShrunk = [];
    const nestingViolations = [];
    const newBaseline = { _meta: { updatedAt: new Date().toISOString(), fileLimit: FILE_LINE_LIMIT, funcLimit: FUNC_LINE_LIMIT, nestingLimit: NESTING_DEPTH_LIMIT }, files: {}, functions: {}, nesting: {}, paradigms: [] };

    for (const { abs, rel } of files) {
        const content = fs.readFileSync(abs, 'utf8');
        const lines = content.split('\n');
        const lineCount = countEffectiveLines(lines);

        newBaseline.files[rel] = lineCount;
        checkFileLines(rel, lineCount, baseline, violations, autoShrunk);

        const functions = extractFunctions(lines);
        for (const func of functions) {
            const funcKey = `${rel}::${func.name}`;
            const effectiveLines = countEffectiveLines(lines, func.start - 1, func.end - 1);
            newBaseline.functions[funcKey] = effectiveLines;
            checkFuncLines(rel, func, funcKey, effectiveLines, baseline, violations, autoShrunk);

            const { maxDepth, maxDepthLine } = measureNestingDepth(lines, func.start - 1, func.end - 1);
            newBaseline.nesting[funcKey] = maxDepth;
            checkNesting(funcKey, rel, func.name, maxDepth, maxDepthLine, baseline, nestingViolations, autoShrunk);
        }

        const badParadigms = detectBadParadigms(lines, rel);
        for (const bp of badParadigms) {
            const key = bp.file + ':' + bp.line;
            newBaseline.paradigms.push(key);
            if (!(baseline.paradigms && baseline.paradigms.includes(key))) {
                violations.push({ type: 'paradigm', file: bp.file, message: `${bp.file}:${bp.line} ${bp.message}` });
            }
        }
    }
    return { newBaseline, violations, autoShrunk, nestingViolations };
}

function checkFileLines(rel, lineCount, baseline, violations, autoShrunk) {
    const fileBaseline = baseline.files?.[rel];
    if (lineCount > FILE_LINE_LIMIT) {
        if (typeof fileBaseline === 'number' && fileBaseline > FILE_LINE_LIMIT) {
            if (lineCount > fileBaseline) {
                violations.push({ type: 'file', file: rel, current: lineCount, baseline: fileBaseline, limit: FILE_LINE_LIMIT, message: `文件 ${rel} 超过基线：${lineCount} 有效行（基线 ${fileBaseline}，上限 ${FILE_LINE_LIMIT}）` });
            } else if (lineCount < fileBaseline) {
                autoShrunk.push({ type: 'file', key: rel, from: fileBaseline, to: lineCount });
            }
        } else {
            violations.push({ type: 'file', file: rel, current: lineCount, baseline: null, limit: FILE_LINE_LIMIT, message: `文件 ${rel} 突破上限：${lineCount} 有效行（上限 ${FILE_LINE_LIMIT}）` });
        }
    } else if (typeof fileBaseline === 'number' && fileBaseline > FILE_LINE_LIMIT) {
        autoShrunk.push({ type: 'file', key: rel, from: fileBaseline, to: lineCount });
    }
}

function checkFuncLines(rel, func, funcKey, effectiveLines, baseline, violations, autoShrunk) {
    const funcBaseline = baseline.functions?.[funcKey];
    if (effectiveLines > FUNC_LINE_LIMIT) {
        if (typeof funcBaseline === 'number' && funcBaseline > FUNC_LINE_LIMIT) {
            if (effectiveLines > funcBaseline) {
                violations.push({ type: 'function', file: rel, func: func.name, start: func.start, end: func.end, current: effectiveLines, baseline: funcBaseline, limit: FUNC_LINE_LIMIT, message: `函数 ${funcKey} 超过基线：${effectiveLines} 有效行（基线 ${funcBaseline}，上限 ${FUNC_LINE_LIMIT}）` });
            } else if (effectiveLines < funcBaseline) {
                autoShrunk.push({ type: 'function', key: funcKey, from: funcBaseline, to: effectiveLines });
            }
        } else {
            violations.push({ type: 'function', file: rel, func: func.name, start: func.start, end: func.end, current: effectiveLines, baseline: null, limit: FUNC_LINE_LIMIT, message: `函数 ${funcKey} 突破上限：${effectiveLines} 有效行（上限 ${FUNC_LINE_LIMIT}）` });
        }
    } else if (typeof funcBaseline === 'number' && funcBaseline > FUNC_LINE_LIMIT) {
        autoShrunk.push({ type: 'function', key: funcKey, from: funcBaseline, to: effectiveLines });
    }
}

function checkNesting(funcKey, rel, funcName, maxDepth, maxDepthLine, baseline, nestingViolations, autoShrunk) {
    const nestBaseline = baseline.nesting?.[funcKey];
    if (maxDepth > NESTING_DEPTH_LIMIT) {
        if (typeof nestBaseline === 'number' && nestBaseline > NESTING_DEPTH_LIMIT) {
            if (maxDepth > nestBaseline) {
                nestingViolations.push({ file: rel, func: funcName, maxDepth, line: maxDepthLine, limit: NESTING_DEPTH_LIMIT, message: `函数 ${funcKey} 嵌套深度 ${maxDepth} 层，超过冻结基线 ${nestBaseline}（上限 ${NESTING_DEPTH_LIMIT}，最深处 L${maxDepthLine}）` });
            } else if (maxDepth < nestBaseline) {
                autoShrunk.push({ type: 'nesting', key: funcKey, from: nestBaseline, to: maxDepth });
            }
        } else {
            nestingViolations.push({ file: rel, func: funcName, maxDepth, line: maxDepthLine, limit: NESTING_DEPTH_LIMIT, message: `函数 ${funcKey} 嵌套深度 ${maxDepth} 层（上限 ${NESTING_DEPTH_LIMIT}，最深处 L${maxDepthLine}）` });
        }
    } else if (typeof nestBaseline === 'number' && nestBaseline > NESTING_DEPTH_LIMIT) {
        autoShrunk.push({ type: 'nesting', key: funcKey, from: nestBaseline, to: maxDepth });
    }
}

function reportUpdate(label, blPath, newBaseline) {
    const overFiles = Object.entries(newBaseline.files).filter(([, v]) => v > FILE_LINE_LIMIT);
    const overFuncs = Object.entries(newBaseline.functions).filter(([, v]) => v > FUNC_LINE_LIMIT);
    const overNest = Object.entries(newBaseline.nesting).filter(([, v]) => v > NESTING_DEPTH_LIMIT);
    console.log(`✅ ${label}基线已更新 → ${path.basename(blPath)}`);
    if (overFiles.length > 0) { console.log(`   ⚠ 超限文件 (>${FILE_LINE_LIMIT} 行):`); overFiles.forEach(([k, v]) => console.log(`     ${k}: ${v}`)); }
    if (overFuncs.length > 0) { console.log(`   ⚠ 超限函数 (>${FUNC_LINE_LIMIT} 行):`); overFuncs.forEach(([k, v]) => console.log(`     ${k}: ${v}`)); }
    if (overNest.length > 0) { console.log(`   ⚠ 超限嵌套 (>${NESTING_DEPTH_LIMIT} 层):`); overNest.forEach(([k, v]) => console.log(`     ${k}: ${v}`)); }
}

function applyShrink(label, blPath, newBaseline, autoShrunk) {
    if (autoShrunk.length === 0) return;
    saveBaselineTo(blPath, newBaseline);
    console.log(`🔽 ${label}自动收缩 ${autoShrunk.length} 项：`);
    for (const s of autoShrunk) {
        const lbl = s.type === 'nesting' ? `嵌套 ${s.key}` : s.type === 'file' ? `文件 ${s.key}` : `函数 ${s.key}`;
        const limit = s.type === 'nesting' ? NESTING_DEPTH_LIMIT : s.type === 'file' ? FILE_LINE_LIMIT : FUNC_LINE_LIMIT;
        console.log(`  ${s.to <= limit ? '✅ 已解冻' : '📉 已收紧'} ${lbl}: ${s.from} → ${s.to}`);
    }
}

function run() {
    const isUpdate = process.argv.includes('--update');
    const allFiles = collectJsFiles(SCAN_DIR);
    const prodFiles = allFiles.filter(f => !isTestFile(f.rel));
    const testFiles = allFiles.filter(f => isTestFile(f.rel));

    const prodBaseline = loadBaselineFrom(BASELINE_PATH);
    const testBaseline = loadBaselineFrom(TEST_BASELINE_PATH);

    const prod = scanFilesWithBaseline(prodFiles, prodBaseline);
    const test = scanFilesWithBaseline(testFiles, testBaseline);

    if (isUpdate) {
        saveBaselineTo(BASELINE_PATH, prod.newBaseline);
        saveBaselineTo(TEST_BASELINE_PATH, test.newBaseline);
        reportUpdate('生产', BASELINE_PATH, prod.newBaseline);
        console.log(`   生产文件: ${prodFiles.length}`);
        reportUpdate('测试', TEST_BASELINE_PATH, test.newBaseline);
        console.log(`   测试文件: ${testFiles.length}`);
        return 0;
    }

    if (!prodBaseline.files && !prodBaseline.functions && !testBaseline.files && !testBaseline.functions) {
        console.error('❌ 基线文件不存在，请先运行: node scripts/size-guard.cjs --update');
        return 1;
    }

    applyShrink('生产', BASELINE_PATH, prod.newBaseline, prod.autoShrunk);
    applyShrink('测试', TEST_BASELINE_PATH, test.newBaseline, test.autoShrunk);

    const allViolations = [...prod.violations, ...test.violations];
    const allNesting = [...prod.nestingViolations, ...test.nestingViolations];

    console.log(`📏 size-guard: ${allFiles.length} 文件 (生产 ${prodFiles.length}, 测试 ${testFiles.length})`);

    if (allNesting.length > 0) {
        console.warn(`\n🔀 嵌套深度超限 ${allNesting.length} 处（上限 ${NESTING_DEPTH_LIMIT} 层）：`);
        for (const nv of allNesting) { console.warn(`  ⚠ ${nv.message}`); }
    }

    if (allViolations.length === 0) {
        console.log('✅ 体积守卫通过 — 无新增超限');
        return allNesting.length > 0 ? 1 : 0;
    }

    console.error(`\n❌ 发现 ${allViolations.length} 处违规:\n`);
    for (const v of allViolations) { console.error(`  🚫 ${v.message}`); }
    console.error('\n💡 修复方法: 拆分文件/函数后重新运行。若为合理增长，运行 --update 更新基线。\n');
    return 1;
}

module.exports = {
    countEffectiveLines,
    extractFunctions,
    measureNestingDepth,
    run,
};

if (require.main === module) {
    process.exit(run());
}
