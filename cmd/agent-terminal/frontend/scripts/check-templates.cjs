#!/usr/bin/env node
// check-templates.cjs — 根治 webview 黑屏的 preflight 守卫（v2）
//
// 用 jsdom 构造浏览器环境，import lib/vue.esm-browser.prod.js 本体的 compile
// 函数（跟 Wails webview 里加载的是同一份代码），对所有 template 做编译
// 检查。任何一个 template 编译挂，exit 1 → desktop dev launcher abort，窗口不起。

const { JSDOM } = require('jsdom');
const jsdom = new JSDOM('<!doctype html><html><body></body></html>', {
  url: 'http://localhost/',
  pretendToBeVisual: true,
});
globalThis.window = jsdom.window;
globalThis.document = jsdom.window.document;
globalThis.navigator = jsdom.window.navigator;
globalThis.Element = jsdom.window.Element;
globalThis.HTMLElement = jsdom.window.HTMLElement;
globalThis.SVGElement = jsdom.window.SVGElement;
globalThis.Node = jsdom.window.Node;
globalThis.getComputedStyle = jsdom.window.getComputedStyle;

const fs = require('node:fs');
const path = require('node:path');

const VUE_APP_ROOT = path.resolve(__dirname, '..', 'vue-app');
const VUE_BUNDLE = path.resolve(__dirname, '..', 'lib', 'vue.esm-browser.prod.js');

function walk(dir, out = []) {
  for (const ent of fs.readdirSync(dir, { withFileTypes: true })) {
    const full = path.join(dir, ent.name);
    if (ent.isDirectory()) {
      if (ent.name === 'node_modules' || ent.name === 'dist') continue;
      walk(full, out);
    } else if (ent.isFile() && /\.js$/.test(ent.name) && !/\.test\.js$/.test(ent.name)) {
      out.push(full);
    }
  }
  return out;
}

function findBacktickEnd(src, start) {
  let i = start, depth = 0, escaped = false;
  while (i < src.length) {
    const ch = src[i];
    if (escaped) { escaped = false; i++; continue; }
    if (ch === '\\') { escaped = true; i++; continue; }
    if (ch === '$' && src[i + 1] === '{') { depth++; i += 2; continue; }
    if (ch === '}' && depth > 0) { depth--; i++; continue; }
    if (ch === '`' && depth === 0) return i;
    i++;
  }
  return -1;
}

function extract(src) {
  const out = [];
  const seen = new Set();
  const patterns = [
    /(?:\b)template\b\s*[:=]\s*`/g,
    /\b(?:const|let|var|export\s+const|export\s+let|export\s+var)\s+\w*(?:TEMPLATE|Template|_TPL|_tpl)\w*\s*=\s*`/g,
  ];
  for (const re of patterns) {
    let m;
    while ((m = re.exec(src)) !== null) {
      const end = findBacktickEnd(src, re.lastIndex);
      if (end === -1) continue;
      if (seen.has(m.index)) continue;
      seen.add(m.index);
      const tpl = src.slice(re.lastIndex, end);
      if (!/<[a-zA-Z]/.test(tpl)) continue;
      out.push({ tpl, lineNumber: src.slice(0, m.index).split('\n').length });
    }
  }
  return out;
}

function formatError(file, entry, err) {
  const { tpl, lineNumber } = entry;
  const lines = tpl.split('\n');
  const head = lines.slice(0, Math.min(3, lines.length)).map((l, i) => `  L${i + 1}: ${l.slice(0, 180)}`).join('\n');
  const tail = lines.length > 6
    ? '\n  ...\n' + lines.slice(-3).map((l, i) => `  L${lines.length - 3 + i + 1}: ${l.slice(0, 180)}`).join('\n')
    : '';
  return [
    `  ❌ ${path.relative(VUE_APP_ROOT, file)}`,
    `     template @ line ${lineNumber} (len=${tpl.length})`,
    `     ${err.name}: ${err.message}`,
    err.loc ? `     at template line ${err.loc.start?.line}, col ${err.loc.start?.column}` : '',
    head,
    tail,
  ].filter(Boolean).join('\n');
}

(async () => {
  // 动态 import 浏览器 Vue bundle
  const Vue = await import(`file://${VUE_BUNDLE}`);
  if (typeof Vue.compile !== 'function') {
    console.error('[check-templates] ❌ Vue.compile not available in', VUE_BUNDLE);
    process.exit(2);
  }

  const files = walk(VUE_APP_ROOT);
  let scanned = 0;
  const failures = [];
  for (const file of files) {
    const src = fs.readFileSync(file, 'utf8');
    for (const entry of extract(src)) {
      scanned++;
      // 关键：模板源码在 .js 文件里是 JS 模板字面量，浏览器 JS parser 会先把 \n 解析成真换行。
      // 必须把 raw 源码片段**当作模板字面量 eval 一次**，得到 runtime 真实字符串，
      // 再交给 Vue.compile；否则无法复现 webview 里的黑屏 bug。
      let runtimeTpl;
      try {
        // eslint-disable-next-line no-new-func
        runtimeTpl = Function('return `' + entry.tpl + '`')();
      } catch (err) {
        failures.push({ file, entry, err: new Error('模板字面量 eval 失败：' + err.message) });
        continue;
      }
      try {
        const render = Vue.compile(runtimeTpl);
        // 为了严格模拟浏览器 Vue runtime-compiler 路径，再 reparse 一次 render 源码。
        // eslint-disable-next-line no-new-func
        void new Function('return ' + render.toString())();
      } catch (err) {
        failures.push({ file, entry, err });
      }
    }
  }

  if (failures.length === 0) {
    console.log(`[check-templates] ✅ ${scanned} Vue templates 全部通过 (浏览器 Vue runtime-compiler)`);
    process.exit(0);
  }
  console.error(`[check-templates] ❌ ${failures.length}/${scanned} Vue templates 编译或 new Function() reparse 失败（会导致 webview 黑屏）`);
  for (const f of failures) {
    console.error('');
    console.error(formatError(f.file, f.entry, f.err));
  }
  console.error('');
  console.error('[check-templates] 修复上述问题后再启动 run-new-ui-desktop.sh 或 run-new-ui-desktop.ps1');
  process.exit(1);
})();
