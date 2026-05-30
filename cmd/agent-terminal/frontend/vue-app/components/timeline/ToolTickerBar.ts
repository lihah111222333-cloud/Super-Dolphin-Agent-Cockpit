import { onBeforeUnmount, onMounted, ref, watch } from '../../../lib/vue.esm-browser.prod.js';

const TOOL_TICKER_SCROLL_STEP_PX = 0.45;

type ToolTickerBarProps = { text?: string; visible?: boolean };
type ViewportRef = { value: HTMLElement | null };

function setupToolTickerBar(props: ToolTickerBarProps) {
  const toolTickerViewportRef: ViewportRef = ref(null);
  let toolTickerDirection = 1;
  let toolTickerFrame = 0;
  let toolTickerPaused = false;

  function prefersReducedMotion(): boolean {
    if (typeof window === 'undefined' || typeof window.matchMedia !== 'function') return false;
    return window.matchMedia('(prefers-reduced-motion: reduce)').matches;
  }

  function cancelToolTickerFrame(): void {
    if (!toolTickerFrame || typeof cancelAnimationFrame !== 'function') return;
    cancelAnimationFrame(toolTickerFrame);
    toolTickerFrame = 0;
  }

  function resetToolTickerViewport(): void {
    const viewport = toolTickerViewportRef.value;
    if (!viewport) return;
    viewport.scrollLeft = 0;
    toolTickerDirection = 1;
  }

  function resolveToolTickerMaxScroll(viewport: HTMLElement | null): number {
    if (!viewport) return 0;
    return Math.max(0, viewport.scrollWidth - viewport.clientWidth);
  }

  function scheduleToolTickerFrame(): void {
    cancelToolTickerFrame();
    if (toolTickerPaused || prefersReducedMotion() || !props.visible || !props.text) return;
    if (typeof window === 'undefined' || typeof window.requestAnimationFrame !== 'function') return;
    toolTickerFrame = window.requestAnimationFrame(runToolTickerFrame);
  }

  function runToolTickerFrame(): void {
    toolTickerFrame = 0;
    if (toolTickerPaused || prefersReducedMotion() || !props.visible || !props.text) return;
    const viewport = toolTickerViewportRef.value;
    if (!viewport) return;
    const maxScroll = resolveToolTickerMaxScroll(viewport);
    if (maxScroll <= 1) {
      viewport.scrollLeft = 0;
      toolTickerDirection = 1;
      return;
    }
    let next = viewport.scrollLeft + (TOOL_TICKER_SCROLL_STEP_PX * toolTickerDirection);
    if (next >= maxScroll) {
      next = maxScroll;
      toolTickerDirection = -1;
    } else if (next <= 0) {
      next = 0;
      toolTickerDirection = 1;
    }
    viewport.scrollLeft = next;
    scheduleToolTickerFrame();
  }

  function restartToolTicker(): void {
    cancelToolTickerFrame();
    toolTickerPaused = false;
    if (typeof window === 'undefined' || typeof window.requestAnimationFrame !== 'function') return;
    window.requestAnimationFrame(() => {
      resetToolTickerViewport();
      scheduleToolTickerFrame();
    });
  }

  function pauseToolTicker(): void {
    toolTickerPaused = true;
    cancelToolTickerFrame();
  }

  function resumeToolTicker(): void {
    toolTickerPaused = false;
    scheduleToolTickerFrame();
  }

  let wasVisible = false;

  watch(
    () => `${props.visible ? 1 : 0}|${(props.text || '').toString()}`,
    () => {
      const nextVisible = Boolean(props.visible && props.text);
      if (!nextVisible) {
        pauseToolTicker();
        resetToolTickerViewport();
        wasVisible = false;
        return;
      }
      if (!wasVisible) {
        restartToolTicker();
      } else {
        scheduleToolTickerFrame();
      }
      wasVisible = true;
    },
    { immediate: true },
  );

  onMounted(() => {
    if (!props.visible || !props.text) return;
    restartToolTicker();
  });

  onBeforeUnmount(() => {
    cancelToolTickerFrame();
  });

  return { toolTickerViewportRef, pauseToolTicker, resumeToolTicker };
}

export const ToolTickerBar = {
  name: 'ToolTickerBar',
  props: {
    text: { type: String, default: '' },
    visible: { type: Boolean, default: false },
  },
  setup: setupToolTickerBar,
  template: `
    <div
      class="chat-status-tool-ticker"
      :title="text"
      @mouseenter="pauseToolTicker"
      @mouseleave="resumeToolTicker"
    >
      <div ref="toolTickerViewportRef" class="chat-status-tool-ticker__viewport">
        <div class="chat-status-tool-ticker__track">
          <span class="chat-status-tool-ticker__content">{{ text }}</span>
        </div>
      </div>
    </div>
  `,
};
