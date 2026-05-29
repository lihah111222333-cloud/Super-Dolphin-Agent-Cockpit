// @ts-nocheck
/**
 * Mermaid 懒加载工具
 * - renderMermaidInContainer(el): 查找页面内所有待渲染的 mermaid 容器并渲染
 * - 按需动态 import mermaid，避免影响首屏体积
 */

let mermaidInstance = null;
let mermaidInitPromise = null;

async function ensureMermaid() {
  if (mermaidInstance) return mermaidInstance;
  // 若上一次初始化已失败，清空缓存允许重试
  if (mermaidInitPromise) {
    try {
      return await mermaidInitPromise;
    } catch {
      mermaidInitPromise = null;
    }
  }

  mermaidInitPromise = (async () => {
    const mod = await import('mermaid');
    const mermaid = mod.default || mod;
    mermaid.initialize({
      startOnLoad: false,
      theme: 'dark',
      themeVariables: {
        darkMode: true,
        background: '#1e1e1e',
        primaryColor: '#7c3aed',
        primaryTextColor: '#e2e8f0',
        primaryBorderColor: '#6d28d9',
        lineColor: '#64748b',
        secondaryColor: '#374151',
        tertiaryColor: '#1f2937',
        fontSize: '13px',
      },
      fontFamily: 'ui-monospace, "SF Mono", Menlo, monospace',
      logLevel: 'error',
    });
    mermaidInstance = mermaid;
    return mermaid;
  })();

  return mermaidInitPromise;
}

/**
 * 清理 mermaid 生成的 SVG，移除潜在脚本注入内容。
 * Electron 渲染进程无沙箱，须主动过滤。
 */
function sanitizeSvgEl(svgEl) {
  // 删除所有 <script> 标签
  svgEl.querySelectorAll('script').forEach((el) => el.remove());
  // 删除所有元素上的 on* 事件属性
  svgEl.querySelectorAll('*').forEach((el) => {
    Array.from(el.attributes).forEach((attr) => {
      if (/^on/i.test(attr.name)) el.removeAttribute(attr.name);
    });
  });
  // 删除 href/xlink:href 指向 javascript: 的元素
  svgEl.querySelectorAll('[href],[xlink\\:href]').forEach((el) => {
    const href = el.getAttribute('href') || el.getAttribute('xlink:href') || '';
    if (/^\s*javascript:/i.test(href)) el.removeAttribute('href');
  });
}

let renderCounter = 0;

/**
 * 扫描 DOM 元素内所有未渲染的 mermaid 容器并渲染它们
 * @param {Element|Document} [root=document]
 */
export async function renderMermaidInContainer(root = document) {
  const containers = Array.from(
    (root || document).querySelectorAll('.chat-md-mermaid-container:not([data-mermaid-rendered])'),
  );
  if (containers.length === 0) return;

  let mermaid;
  try {
    mermaid = await ensureMermaid();
  } catch (err) {
    // mermaid 加载失败，保留 fallback 的源码显示
    console.warn('[mermaid] load failed:', err);
    return;
  }

  await Promise.all(
    containers.map(async (container) => {
      const source = (container.getAttribute('data-mermaid-source') || '').trim();
      if (!source) return;
      container.setAttribute('data-mermaid-rendered', 'true');

      const id = `mermaid-${Date.now()}-${(renderCounter += 1)}`;
      try {
        const { svg } = await mermaid.render(id, source);
        const svgEl = document.createElement('div');
        svgEl.className = 'chat-md-mermaid-svg';
        svgEl.innerHTML = svg;
        sanitizeSvgEl(svgEl);
        // 移除 fallback，插入净化后的 svg
        const fallback = container.querySelector('.chat-md-mermaid-fallback');
        if (fallback) fallback.remove();
        container.appendChild(svgEl);
      } catch (err) {
        // 渲染失败：显示错误提示，保留原始代码块
        const errorEl = document.createElement('div');
        errorEl.className = 'chat-md-mermaid-error';
        errorEl.textContent = `Mermaid 渲染失败: ${String(err?.message || err || 'unknown')}`;
        container.prepend(errorEl);
        console.warn('[mermaid] render error:', err);
      }
    }),
  );
}
