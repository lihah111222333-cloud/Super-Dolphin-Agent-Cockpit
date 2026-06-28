/**
 * translate-dict.js — 轻量级英中字典翻译工具
 *
 * 从本地 en-zh-dict.json 加载 ~14000 条英中词典（含技术术语），
 * 用于将 AI 模型输出的英文思考摘要 / 状态标题翻译为中文。
 *
 * 三层翻译策略：
 *   1. 完整短语精确匹配（优先）
 *   2. 正则模式替换（处理变量句型如 "Exit code 1"）
 *   3. 逐词/短语分段替换（动词/名词字典）
 */

// ── 字典数据（从 JSON 文件加载） ──────────────────────────
let _phrases = {};
let _verbs = {};
let _nouns = {};
let _stopwords = new Set();
let _loaded = false;
let _loadPromise = null;

// ── 正则模式替换 ─────────────────────────────────────
const REGEX_RULES = [
  { pattern: /^exit\s+code\s+(\d+)$/i, replace: '退出码 $1' },
  { pattern: /^running\.{2,}$/i, replace: '运行中…' },
];

// 翻译结果缓存
const _cache = new Map();
const CACHE_MAX = 512;

/**
 * 加载字典文件。首次调用时从 en-zh-dict.json 加载，后续返回缓存。
 */
function loadDict() {
  if (_loaded) return Promise.resolve();
  if (_loadPromise) return _loadPromise;

  // public/data/en-zh-dict.json → Vite 构建后在 dist/data/，debug/Wails 均可通过绝对路径访问
  const jsonUrl = '/data/en-zh-dict.json';

  _loadPromise = fetch(jsonUrl)
    .then((res) => {
      if (!res.ok) throw new Error(`Dict load failed: ${res.status}`);
      return res.json();
    })
    .then((data) => {
      _phrases = data.phrases || {};
      _verbs = data.verbs || {};
      _nouns = data.nouns || {};
      _stopwords = new Set(data.stopwords || []);
      _loaded = true;
    })
    .catch((err) => {
      // 加载失败，使用内置迷你字典兜底
      console.warn('[translate-dict] Failed to load en-zh-dict.json, using fallback:', err.message);
      _phrases = FALLBACK_PHRASES;
      _verbs = FALLBACK_VERBS;
      _nouns = FALLBACK_NOUNS;
      _stopwords = new Set(FALLBACK_STOPWORDS);
      _loaded = true;
    });

  return _loadPromise;
}

// 启动时立即开始加载
loadDict();

// ── 内置迷你兜底字典（JSON 加载失败时使用） ─────────────────
const FALLBACK_PHRASES = {
  'checking for untracked changes': '检查未追踪的更改',
  'checking for errors': '检查错误',
  'running the tests': '运行测试',
  'running tests': '运行测试',
  'terminal command': '终端命令',
  'ran command': '已执行命令',
  'running command': '命令执行中',
  'errored command': '命令执行失败',
  'canceled command': '命令已取消',
  '(empty plan)': '(空计划)',
};

const FALLBACK_VERBS = {
  'checking': '检查', 'reading': '读取', 'analyzing': '分析',
  'writing': '编写', 'implementing': '实现', 'running': '运行',
  'fixing': '修复', 'updating': '更新', 'reviewing': '审查',
  'creating': '创建', 'searching': '搜索', 'planning': '规划',
  'debugging': '调试', 'refactoring': '重构', 'testing': '测试',
  'understanding': '理解', 'verifying': '验证', 'exploring': '探索',
  'looking': '查看', 'building': '构建', 'deploying': '部署',
  'generating': '生成', 'parsing': '解析', 'processing': '处理',
};

const FALLBACK_NOUNS = {
  'code': '代码', 'file': '文件', 'files': '文件', 'test': '测试',
  'tests': '测试', 'error': '错误', 'errors': '错误', 'output': '输出',
  'result': '结果', 'results': '结果', 'changes': '变更', 'fix': '修复',
  'implementation': '实现', 'dependencies': '依赖', 'config': '配置',
  'build': '构建', 'project': '项目', 'command': '命令', 'module': '模块',
  'component': '组件', 'function': '函数', 'interface': '接口',
  'structure': '结构', 'codebase': '代码库', 'issue': '问题',
};

const FALLBACK_STOPWORDS = [
  'the', 'a', 'an', 'for', 'to', 'of', 'in', 'on', 'at', 'by',
  'with', 'from', 'into', 'is', 'are', 'it', 'its', 'i', 'and',
  'or', 'but', 'if', 'up', 'out', 'not', 'this', 'that',
];

// ── 文本预处理 ──────────────────────────────────────

/**
 * 预处理文本: 处理中英文混排、camelCase 等。
 *
 * 步骤:
 *   1. 中英文边界加空格 (函数like → 函数 like)
 *   2. camelCase 拆分 (takCloserLook → tak Closer Look)
 */
function _normalizeText(text) {
  if (!text) return text;

  // Step 1: 中英文边界加空格
  let result = text
    .replace(/([\u4e00-\u9fff])([a-zA-Z])/g, '$1 $2')
    .replace(/([a-zA-Z])([\u4e00-\u9fff])/g, '$1 $2');

  // Step 2: camelCase 拆分 (只在有大写字母时)
  if (/[A-Z]/.test(result)) {
    result = result.replace(/([a-z])([A-Z])/g, '$1 $2');
  }

  return result;
}

// ── 翻译函数 ──────────────────────────────────────

/**
 * 翻译英文短文本为中文（短语字典 + 正则 + 逐词翻译）。
 *
 * @param {string} text
 * @returns {string}
 */
export function translateText(text) {
  if (!text || typeof text !== 'string') return text || '';
  const trimmed = text.trim();
  if (!trimmed) return '';

  // 纯中文（无英文字母）→ 不翻译；混合中英文仍需处理
  if (/[\u4e00-\u9fff]/.test(trimmed) && !/[a-zA-Z]/.test(trimmed)) return trimmed;

  // 缓存命中
  if (_cache.has(trimmed)) return _cache.get(trimmed);

  const result = _translate(trimmed);
  _cache.set(trimmed, result);
  if (_cache.size > CACHE_MAX) {
    const first = _cache.keys().next().value;
    _cache.delete(first);
  }
  return result;
}

function _translate(text) {
  // 0. 预处理: 中英文边界分割 + camelCase 拆分
  const normalized = _normalizeText(text);
  const lower = normalized.toLowerCase();

  // 1. 完整短语精确匹配
  if (_phrases[lower]) return _phrases[lower];

  // 2. 正则规则
  for (const rule of REGEX_RULES) {
    if (rule.pattern.test(normalized)) {
      return normalized.replace(rule.pattern, rule.replace);
    }
  }

  // 3. 逐词翻译（JSON 词典 ~14000 条）
  const words = lower.split(/\s+/);
  if (words.length === 0) return text;

  const parts = [];
  let hasTranslation = false;

  for (const word of words) {
    const clean = word.replace(/[^a-z0-9_.\u4e00-\u9fff-]/g, '');
    if (!clean) continue;

    // 已经是中文（如预处理分离出的中文段）
    if (/[\u4e00-\u9fff]/.test(clean)) {
      parts.push(clean);
      hasTranslation = true;
      continue;
    }

    if (_verbs[clean]) {
      parts.push(_verbs[clean]);
      hasTranslation = true;
    } else if (_nouns[clean] !== undefined) {
      if (_nouns[clean]) parts.push(_nouns[clean]);
      hasTranslation = true;
    } else if (_stopwords.has(clean)) {
      hasTranslation = true;
    } else {
      // 未翻译的词保留原样
      parts.push(clean);
    }
  }

  if (!hasTranslation) return text;
  // 拼接结果，中英文交界处加空格提升可读性
  return parts.join('')
    .replace(/([\u4e00-\u9fff])([a-zA-Z0-9])/g, '$1 $2')
    .replace(/([a-zA-Z0-9])([\u4e00-\u9fff])/g, '$1 $2');
}

/**
 * 翻译思考摘要正文（多行文本）。
 * 对每一行尝试翻译，已有中文的行保持原样。
 *
 * @param {string} text
 * @returns {string}
 */
export function translateThinkingBody(text) {
  if (!text || typeof text !== 'string') return text || '';
  const trimmed = text.trim();
  if (!trimmed) return '';
  // 纯中文 → 不翻译；混合中英文仍需处理
  if (/[\u4e00-\u9fff]/.test(trimmed) && !/[a-zA-Z]/.test(trimmed)) return trimmed;

  const lines = trimmed.split('\n');
  return lines.map((line) => {
    const stripped = line.trim();
    if (!stripped) return '';
    return translateText(stripped);
  }).join('\n');
}
