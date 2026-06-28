// @ts-nocheck
/**
 * useMermaidRenderer
 * 在 onMounted / onUpdated 后自动扫描并懒加载渲染 Mermaid 图表。
 * 使用 rAF 去重，防止流式输出期间频繁触发。
 */
import { onMounted, onUpdated } from '../../lib/vue.esm-browser.prod.js';
import { renderMermaidInContainer } from '../utils/mermaid-renderer.js';

export function useMermaidRenderer() {
  let rafId = 0;

  function schedule() {
    if (typeof requestAnimationFrame === 'function') {
      if (rafId) cancelAnimationFrame(rafId);
      rafId = requestAnimationFrame(() => {
        rafId = 0;
        renderMermaidInContainer();
      });
    } else {
      setTimeout(() => renderMermaidInContainer(), 0);
    }
  }

  onMounted(schedule);
  onUpdated(schedule);
}
