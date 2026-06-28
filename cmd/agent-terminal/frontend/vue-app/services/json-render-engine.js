/**
 * json-render spec 解析引擎。
 * 从 markdown 文本中提取 ```json-render 代码块并解析为结构化 spec。
 */

const SPEC_RE = /```json-render\s*\n([\s\S]*?)```/g;

/**
 * 将 root+elements 结构递归解析为带 type 的嵌套树。
 * 若输入已有 type 字段（简化格式），直接透传。
 * @param {object} raw - JSON.parse 后的原始 spec
 * @returns {object} 解析后的 { type, ...props, children?: [...] }
 */
function resolveSpec(raw) {
    if (!raw || typeof raw !== 'object') return raw;

    // 简化格式: 顶层已有 type → 直接返回（递归处理 children）
    if (raw.type && typeof raw.type === 'string') {
        if (Array.isArray(raw.children)) {
            raw.children = raw.children.map(child => {
                if (typeof child === 'string') return child;
                if (child && typeof child === 'object') return resolveSpec(child);
                return child;
            });
        }
        return raw;
    }

    // 标准格式: { root, elements }
    const elements = raw.elements;
    const rootId = raw.root;
    if (!rootId || !elements || typeof elements !== 'object') return raw;

    function resolveElement(id, visited) {
        if (visited.has(id)) return null; // 防止循环引用
        visited.add(id);

        const el = elements[id];
        if (!el || typeof el !== 'object') return null;

        const { type, props, children: childIds, ...rest } = el;
        const resolved = { type: type || '', ...rest, ...(props || {}) };

        if (Array.isArray(childIds) && childIds.length > 0) {
            resolved.children = childIds.map(childId => {
                if (typeof childId === 'string' && elements[childId]) {
                    return resolveElement(childId, new Set(visited));
                }
                if (childId && typeof childId === 'object') return resolveSpec(childId);
                return childId;
            }).filter(Boolean);
        }

        return resolved;
    }

    return resolveElement(rootId, new Set()) || raw;
}

/**
 * 检查文本是否包含 json-render spec 代码块。
 * @param {string} text
 * @returns {boolean}
 */
export function hasJsonRenderSpec(text) {
    if (!text || typeof text !== 'string') return false;
    SPEC_RE.lastIndex = 0;
    return SPEC_RE.test(text);
}

/**
 * 将文本拆分为 text/spec 交替段落。
 * @param {string} text
 * @returns {Array<{ type: 'text'|'spec', content?: string, spec?: object }>}
 */
const _specCache = new Map();

export function extractSpecBlocks(text) {
    if (!text || typeof text !== 'string') return [{ type: 'text', content: text || '' }];

    const blocks = [];
    let lastIndex = 0;
    let match;
    SPEC_RE.lastIndex = 0;

    while ((match = SPEC_RE.exec(text)) !== null) {
        if (match.index > lastIndex) {
            const before = text.slice(lastIndex, match.index).trim();
            if (before) blocks.push({ type: 'text', content: before });
        }
        try {
            const jsonStr = match[1];
            let spec;
            if (_specCache.has(jsonStr)) {
                spec = _specCache.get(jsonStr);
                // Refresh LRU
                _specCache.delete(jsonStr);
                _specCache.set(jsonStr, spec);
            } else {
                const raw = JSON.parse(jsonStr);
                spec = resolveSpec(raw);
                _specCache.set(jsonStr, spec);
                if (_specCache.size > 200) {
                    _specCache.delete(_specCache.keys().next().value);
                }
            }
            blocks.push({ type: 'spec', spec });
        } catch {
            blocks.push({ type: 'text', content: match[0] });
        }
        lastIndex = match.index + match[0].length;
    }

    if (lastIndex < text.length) {
        const tail = text.slice(lastIndex).trim();
        if (tail) blocks.push({ type: 'text', content: tail });
    }

    return blocks.length > 0 ? blocks : [{ type: 'text', content: text }];
}
