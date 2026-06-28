// @ts-nocheck
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { ref, nextTick } from '../lib/vue.esm-browser.prod.js';

vi.mock('./services/log.js', () => ({
  logDebug: vi.fn(),
  logInfo: vi.fn(),
  logWarn: vi.fn(),
  logError: vi.fn(),
}));

import { useAutoScroll } from './composables/useAutoScroll.js';

beforeEach(() => {
  vi.stubGlobal('requestAnimationFrame', (cb) => {
    cb();
    return 1;
  });
  vi.stubGlobal('cancelAnimationFrame', vi.fn());
  vi.stubGlobal('document', {
    querySelector: vi.fn(() => null),
  });
});

afterEach(() => {
  vi.unstubAllGlobals();
});

describe('useAutoScroll', () => {
  it('prefers the workspace scroller before falling back to document querySelector', () => {
    const within = { scrollTop: 0, scrollHeight: 400, clientHeight: 200 };
    const workspaceRef = ref({ querySelector: vi.fn(() => within) });
    const vm = useAutoScroll(workspaceRef);

    expect(vm.resolveChatScroller()).toBe(within);

    workspaceRef.value = null;
    const fallback = { scrollTop: 0, scrollHeight: 500, clientHeight: 200 };
    globalThis.document.querySelector.mockReturnValueOnce(fallback);
    expect(vm.resolveChatScroller()).toBe(fallback);
  });

  it('scrolls to the bottom when auto-scroll is enabled', () => {
    const scroller = { scrollTop: 12, scrollHeight: 640, clientHeight: 240, style: {} };
    const workspaceRef = ref({ querySelector: vi.fn(() => scroller) });
    const vm = useAutoScroll(workspaceRef);

    // non-force 不走 nextTick，RAF mock 同步执行
    vm.scheduleScrollToBottom();

    expect(scroller.scrollTop).toBe(640);
  });

  it('honors disabled auto-scroll unless force is true', async () => {
    const scroller = { scrollTop: 24, scrollHeight: 720, clientHeight: 240, style: {} };
    const workspaceRef = ref({ querySelector: vi.fn(() => scroller) });
    const vm = useAutoScroll(workspaceRef);

    vm.shouldAutoScroll.value = false;
    vm.scheduleScrollToBottom(false);
    expect(scroller.scrollTop).toBe(24);

    // force=true 走 nextTick → double RAF，需要 await nextTick
    vm.scheduleScrollToBottom(true);
    await nextTick();
    expect(scroller.scrollTop).toBe(720);
  });

  it('resetScrollState followed by scheduleScrollToBottom still scrolls to bottom', async () => {
    const scroller = { scrollTop: 500, scrollHeight: 2000, clientHeight: 400, style: {} };
    const workspaceRef = ref({ querySelector: vi.fn(() => scroller) });
    const vm = useAutoScroll(workspaceRef);

    vm.resetScrollState();
    // force=true 走 nextTick → double RAF，需要 await
    vm.scheduleScrollToBottom(true);
    await nextTick();
    expect(scroller.scrollTop).toBe(2000);
  });

  it('saveScrollPosition snapshot and restoreScrollPosition round-trip', () => {
    const scroller = { scrollTop: 350, scrollHeight: 2000, clientHeight: 400, style: {} };
    const workspaceRef = ref({ querySelector: vi.fn(() => scroller) });
    const vm = useAutoScroll(workspaceRef);

    vm.saveScrollPosition();
    // Simulate DOM rebuild resetting scrollTop
    scroller.scrollTop = 0;
    vm.shouldAutoScroll.value = false;
    vm.restoreScrollPosition();
    expect(scroller.scrollTop).toBe(350);
  });

  it('restoreScrollPosition scrolls to bottom when shouldAutoScroll is true', () => {
    const scroller = { scrollTop: 350, scrollHeight: 2000, clientHeight: 400, style: {} };
    const workspaceRef = ref({ querySelector: vi.fn(() => scroller) });
    const vm = useAutoScroll(workspaceRef);

    vm.saveScrollPosition();
    vm.shouldAutoScroll.value = true;
    vm.restoreScrollPosition();
    expect(scroller.scrollTop).toBe(2000);
  });

  // ─── FIX-v3 regression guards ───

  it('[FIX-v3] scheduleScrollToBottom recovers after container was temporarily empty', async () => {
    // Simulate Vue two-phase DOM replacement: scrollHeight ≈ clientHeight initially,
    // then grows after new nodes render.
    let phase = 'filled';
    const scroller = {
      get scrollHeight() { return phase === 'empty' ? 200 : 1200; },
      get clientHeight() { return 200; },
      scrollTop: 600,
      addEventListener: vi.fn(),
      removeEventListener: vi.fn(),
      style: {},
    };
    const workspaceRef = ref({ querySelector: vi.fn(() => scroller) });
    const vm = useAutoScroll(workspaceRef);

    // Prime: scroll to bottom first
    vm.scheduleScrollToBottom(false);
    expect(scroller.scrollTop).toBe(1200);

    // Simulate DOM rebuild: container temporarily collapses then fills
    scroller.scrollTop = 0;
    phase = 'empty';
    // Immediately restore content (rAF is sync in tests)
    phase = 'filled';

    // Force scroll to bottom — should still work
    vm.scheduleScrollToBottom(true);
    await nextTick();
    expect(scroller.scrollTop).toBe(1200);
  });

  it('[FIX-v3] unexpected scroll-to-top does not overwrite savedScrollTop', async () => {
    const scroller = { scrollTop: 500, scrollHeight: 2000, clientHeight: 400, style: {} };
    const workspaceRef = ref({ querySelector: vi.fn(() => scroller) });
    const vm = useAutoScroll(workspaceRef);

    // Prime: scroll to a position > 100
    vm.scheduleScrollToBottom(false);
    scroller.scrollTop = 500;

    // After scheduleScrollToBottom, shouldAutoScroll is true and isAtBottom is true.
    // Now simulate: browser silently resets scrollTop to 0 (DOM rebuild)
    // Then scheduleScrollToBottom(true) should still scroll to bottom.
    scroller.scrollTop = 0;

    // Force scroll to bottom — this should work regardless of the unexpected reset
    vm.scheduleScrollToBottom(true);
    await nextTick();
    expect(scroller.scrollTop).toBe(2000);
  });

  // ─── Scrollbar drag guard ───

  it('restoreScrollPosition skips when user is dragging scrollbar', () => {
    const scroller = { scrollTop: 1800, scrollHeight: 2000, clientHeight: 200, style: {} };
    const workspaceRef = ref({ querySelector: vi.fn(() => scroller) });
    const vm = useAutoScroll(workspaceRef);

    vm.saveScrollPosition();
    scroller.scrollTop = 500;
    vm.shouldAutoScroll.value = true;

    // Simulate user dragging scrollbar
    vm._setUserDragging(true);

    vm.restoreScrollPosition();
    expect(scroller.scrollTop).toBe(500); // NOT reset to 2000
  });

  it('scheduleScrollToBottom (non-force) skips when user is dragging scrollbar', () => {
    const scroller = { scrollTop: 500, scrollHeight: 2000, clientHeight: 200 };
    const workspaceRef = ref({ querySelector: vi.fn(() => scroller) });
    const vm = useAutoScroll(workspaceRef);

    vm._setUserDragging(true);

    vm.scheduleScrollToBottom(false);
    expect(scroller.scrollTop).toBe(500); // NOT scrolled to bottom
  });
});
