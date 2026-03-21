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
const IGNORE_PATTERNS = [
    /node_modules/,
    /\.test\.js$/,
    /\.spec\.js$/,
    /\/lib\//,
    /\/dist\//,
];

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

function loadBaseline() {
    if (!fs.existsSync(BASELINE_PATH)) return {};
    try {
        return JSON.parse(fs.readFileSync(BASELINE_PATH, 'utf8'));
    } catch {
        return {};
    }
}

function saveBaseline(data) {
    fs.writeFileSync(BASELINE_PATH, JSON.stringify(data, null, 2) + '\n', 'utf8');
}

// ─── 主逻辑 ──────────────────────────────────────────────────────────

function run() {
    const isUpdate = process.argv.includes('--update');
    const files = collectJsFiles(SCAN_DIR);
    const baseline = loadBaseline();
    const violations = [];
    const autoShrunk = [];   // 自动收缩记录
    const nestingViolations = [];  // 嵌套深度违规（仅报告，不阻断）
    const newBaseline = { _meta: { updatedAt: new Date().toISOString(), fileLimit: FILE_LINE_LIMIT, funcLimit: FUNC_LINE_LIMIT, nestingLimit: NESTING_DEPTH_LIMIT }, files: {}, functions: {}, nesting: {} };

    for (const { abs, rel } of files) {
        const content = fs.readFileSync(abs, 'utf8');
        const lines = content.split('\n');
        // 只统计有效代码行（剔除空行与注释），防止 agent 删注释来缩减体积
        const lineCount = countEffectiveLines(lines);

        // ── 文件行数检查 ──
        newBaseline.files[rel] = lineCount;
        const fileBaseline = baseline.files?.[rel];

        if (lineCount > FILE_LINE_LIMIT) {
            if (typeof fileBaseline === 'number' && fileBaseline > FILE_LINE_LIMIT) {
                if (lineCount > fileBaseline) {
                    // 已有基线锁定：不允许增长
                    violations.push({
                        type: 'file',
                        file: rel,
                        current: lineCount,
                        baseline: fileBaseline,
                        limit: FILE_LINE_LIMIT,
                        message: `文件 ${rel} 超过基线：${lineCount} 有效行（基线 ${fileBaseline}，上限 ${FILE_LINE_LIMIT}）`,
                    });
                } else if (lineCount < fileBaseline) {
                    // 仍超限但已缩小 → 自动收紧基线
                    autoShrunk.push({ type: 'file', key: rel, from: fileBaseline, to: lineCount });
                }
            } else {
                // 新突破阈值
                violations.push({
                    type: 'file',
                    file: rel,
                    current: lineCount,
                    baseline: null,
                    limit: FILE_LINE_LIMIT,
                    message: `文件 ${rel} 突破上限：${lineCount} 有效行（上限 ${FILE_LINE_LIMIT}）`,
                });
            }
        } else if (typeof fileBaseline === 'number' && fileBaseline > FILE_LINE_LIMIT) {
            // ✨ 达标：之前超限，现在已回落到阈值内 → 自动解冻
            autoShrunk.push({ type: 'file', key: rel, from: fileBaseline, to: lineCount });
        }

        // ── 函数行数检查（只统计有效代码行）──
        const functions = extractFunctions(lines);
        for (const func of functions) {
            const funcKey = `${rel}::${func.name}`;
            // func.start / func.end 是 1-indexed 行号，转成 0-indexed 下标
            const effectiveLines = countEffectiveLines(lines, func.start - 1, func.end - 1);
            newBaseline.functions[funcKey] = effectiveLines;
            const funcBaseline = baseline.functions?.[funcKey];

            if (effectiveLines > FUNC_LINE_LIMIT) {
                if (typeof funcBaseline === 'number' && funcBaseline > FUNC_LINE_LIMIT) {
                    if (effectiveLines > funcBaseline) {
                        violations.push({
                            type: 'function',
                            file: rel,
                            func: func.name,
                            start: func.start,
                            end: func.end,
                            current: effectiveLines,
                            baseline: funcBaseline,
                            limit: FUNC_LINE_LIMIT,
                            message: `函数 ${rel}::${func.name} 超过基线：${effectiveLines} 有效行（基线 ${funcBaseline}，上限 ${FUNC_LINE_LIMIT}）`,
                        });
                    } else if (effectiveLines < funcBaseline) {
                        // 仍超限但已缩小 → 自动收紧基线
                        autoShrunk.push({ type: 'function', key: funcKey, from: funcBaseline, to: effectiveLines });
                    }
                } else {
                    violations.push({
                        type: 'function',
                        file: rel,
                        func: func.name,
                        start: func.start,
                        end: func.end,
                        current: effectiveLines,
                        baseline: null,
                        limit: FUNC_LINE_LIMIT,
                        message: `函数 ${rel}::${func.name} 突破上限：${effectiveLines} 有效行（上限 ${FUNC_LINE_LIMIT}）`,
                    });
                }
            } else if (typeof funcBaseline === 'number' && funcBaseline > FUNC_LINE_LIMIT) {
                // ✨ 达标：之前超限，现在已回落到阈值内 → 自动解冻
                autoShrunk.push({ type: 'function', key: funcKey, from: funcBaseline, to: effectiveLines });
            }

            // ── 嵌套深度检查 ──
            const { maxDepth, maxDepthLine } = measureNestingDepth(lines, func.start - 1, func.end - 1);
            newBaseline.nesting[funcKey] = maxDepth;
            const nestBaseline = baseline.nesting?.[funcKey];

            if (maxDepth > NESTING_DEPTH_LIMIT) {
                if (typeof nestBaseline === 'number' && nestBaseline > NESTING_DEPTH_LIMIT) {
                    if (maxDepth > nestBaseline) {
                        // 已冻结但继续加深 → 违规
                        nestingViolations.push({
                            file: rel, func: func.name, maxDepth, line: maxDepthLine,
                            limit: NESTING_DEPTH_LIMIT,
                            message: `函数 ${funcKey} 嵌套深度 ${maxDepth} 层，超过冻结基线 ${nestBaseline}（上限 ${NESTING_DEPTH_LIMIT}，最深处 L${maxDepthLine}）`,
                        });
                    } else if (maxDepth < nestBaseline) {
                        // 仍超限但已缩小 → 自动收紧
                        autoShrunk.push({ type: 'nesting', key: funcKey, from: nestBaseline, to: maxDepth });
                    }
                    // maxDepth === nestBaseline → 冻结不变，静默通过
                } else {
                    // 新突破嵌套阈值
                    nestingViolations.push({
                        file: rel, func: func.name, maxDepth, line: maxDepthLine,
                        limit: NESTING_DEPTH_LIMIT,
                        message: `函数 ${funcKey} 嵌套深度 ${maxDepth} 层（上限 ${NESTING_DEPTH_LIMIT}，最深处 L${maxDepthLine}）`,
                    });
                }
            } else if (typeof nestBaseline === 'number' && nestBaseline > NESTING_DEPTH_LIMIT) {
                // ✨ 达标：之前超限，现在已回落 → 自动解冻
                autoShrunk.push({ type: 'nesting', key: funcKey, from: nestBaseline, to: maxDepth });
            }
        }
    }

    // ── 更新模式 ──
    if (isUpdate) {
        saveBaseline(newBaseline);
        const overLimitFiles = Object.entries(newBaseline.files).filter(([, v]) => v > FILE_LINE_LIMIT);
        const overLimitFuncs = Object.entries(newBaseline.functions).filter(([, v]) => v > FUNC_LINE_LIMIT);
        const overLimitNest = Object.entries(newBaseline.nesting).filter(([, v]) => v > NESTING_DEPTH_LIMIT);
        console.log(`✅ 基线已更新 → ${path.basename(BASELINE_PATH)}`);
        console.log(`   文件总数: ${files.length}`);
        if (overLimitFiles.length > 0) {
            console.log(`   ⚠ 超限文件 (>${FILE_LINE_LIMIT} 行):`);
            overLimitFiles.forEach(([k, v]) => console.log(`     ${k}: ${v}`));
        }
        if (overLimitFuncs.length > 0) {
            console.log(`   ⚠ 超限函数 (>${FUNC_LINE_LIMIT} 行):`);
            overLimitFuncs.forEach(([k, v]) => console.log(`     ${k}: ${v}`));
        }
        if (overLimitNest.length > 0) {
            console.log(`   ⚠ 超限嵌套 (>${NESTING_DEPTH_LIMIT} 层):`);
            overLimitNest.forEach(([k, v]) => console.log(`     ${k}: ${v}`));
        }
        return 0;
    }

    // ── 检查模式 ──
    if (!baseline.files && !baseline.functions) {
        console.error('❌ 基线文件不存在，请先运行: node scripts/size-guard.js --update');
        return 1;
    }

    // ── 自动收缩：达标项自动更新基线 ──
    if (autoShrunk.length > 0) {
        // 将收缩后的值写回基线（newBaseline 已经是最新值）
        saveBaseline(newBaseline);
        console.log(`🔽 自动收缩 ${autoShrunk.length} 项基线：`);
        for (const s of autoShrunk) {
            const label = s.type === 'nesting' ? `嵌套 ${s.key}` : s.type === 'file' ? `文件 ${s.key}` : `函数 ${s.key}`;
            const limit = s.type === 'nesting' ? NESTING_DEPTH_LIMIT : s.type === 'file' ? FILE_LINE_LIMIT : FUNC_LINE_LIMIT;
            const tag = s.to <= limit ? '✅ 已解冻' : '📉 已收紧';
            console.log(`  ${tag} ${label}: ${s.from} → ${s.to}`);
        }
    }

    // 打印统计
    const overLimitFiles = Object.entries(newBaseline.files).filter(([, v]) => v > FILE_LINE_LIMIT);
    const overLimitFuncs = Object.entries(newBaseline.functions).filter(([, v]) => v > FUNC_LINE_LIMIT);
    console.log(`📏 size-guard: ${files.length} 文件, ${overLimitFiles.length} 超限文件, ${overLimitFuncs.length} 超限函数`);

    // ── 嵌套深度报告 ──
    if (nestingViolations.length > 0) {
        console.warn(`\n🔀 嵌套深度超限 ${nestingViolations.length} 处（上限 ${NESTING_DEPTH_LIMIT} 层）：`);
        for (const nv of nestingViolations) {
            console.warn(`  ⚠ ${nv.message}`);
        }
    }

    if (violations.length === 0) {
        console.log('✅ 体积守卫通过 — 无新增超限');
        return nestingViolations.length > 0 ? 1 : 0;
    }

    console.error(`\n❌ 发现 ${violations.length} 处违规:\n`);
    for (const v of violations) {
        console.error(`  🚫 ${v.message}`);
    }
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
