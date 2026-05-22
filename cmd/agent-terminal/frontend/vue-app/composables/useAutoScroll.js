import { ref, nextTick, onMounted, onBeforeUnmount } from '../../lib/vue.esm-browser.prod.js';
import { logDebug, logInfo, logWarn } from '../services/log.js';
import { resolveChatScroller as _resolveScroller, distanceFromBottom, isNearBottom } from './scroll-helpers.js';

function createUserScrollIntentHandlers(ctx) {
  function onScrollbarMouseDown(e) {
    if (e.offsetX <= e.currentTarget.clientWidth) return;
    ctx.drag.userDraggingScrollbar = true;
    if (ctx.drag.dragEndTimer) { clearTimeout(ctx.drag.dragEndTimer); ctx.drag.dragEndTimer = 0; }
    logDebug('scroll', 'scroll.drag.start', { offsetX: e.offsetX, clientWidth: e.currentTarget.clientWidth });
  }

  function onScrollbarMouseUp() {
    if (!ctx.drag.userDraggingScrollbar) return;
    if (ctx.drag.dragEndTimer) clearTimeout(ctx.drag.dragEndTimer);
    ctx.drag.dragEndTimer = setTimeout(() => {
      ctx.drag.userDraggingScrollbar = false;
      ctx.drag.dragEndTimer = 0;
      const el = ctx.resolveChatScroller();
      if (!el) return;
      ctx.setSavedScrollTop(el.scrollTop);
      ctx.setSavedDistFromBottom(el.scrollHeight - el.scrollTop - el.clientHeight);
      if (!isNearBottom(el) && ctx.shouldAutoScroll.value) {
        logDebug('scroll', 'scroll.user.drag_up_end', { savedScrollTop: el.scrollTop, savedDistFromBottom: el.scrollHeight - el.scrollTop - el.clientHeight });
        ctx.shouldAutoScroll.value = false;
      }
    }, 100);
  }

  function onUserWheel(e) {
    if (e.deltaY < 0 && ctx.shouldAutoScroll.value) {
      logDebug('scroll', 'scroll.user.wheel_up', { deltaY: e.deltaY });
      ctx.shouldAutoScroll.value = false;
    }
  }

  function onUserKeyDown(e) {
    if (['ArrowUp', 'PageUp', 'Home'].includes(e.key) && ctx.shouldAutoScroll.value) {
      logDebug('scroll', 'scroll.user.keydown_up', { key: e.key });
      ctx.shouldAutoScroll.value = false;
    }
  }

  function cleanup() {
    if (ctx.drag.dragEndTimer) clearTimeout(ctx.drag.dragEndTimer);
  }

  return { onScrollbarMouseDown, onScrollbarMouseUp, onUserWheel, onUserKeyDown, cleanup };
}

export function useAutoScroll(workspaceRef) {
  const shouldAutoScroll = ref(true), isAtBottom = ref(true);
  let scrollTimer = 0, lastScrollTop = 0, scrollListenerCleanup = () => {}, lastScrollerIdentity = null, reattachCheckTimer = 0;
  let savedScrollTop = 0, savedDistFromBottom = 0, mutationObserver = null, programmaticScrollInProgress = false, mutationRestoreRaf = 0, domRebuildInProgress = false;
  let snapshotGuardActive = false, snapshotGuardTimer = 0;
  const drag = { userDraggingScrollbar: false, dragEndTimer: 0 };

  const resolveChatScroller = () => _resolveScroller(workspaceRef);
  const userScrollHandlers = createUserScrollIntentHandlers({ drag, shouldAutoScroll, resolveChatScroller, setSavedScrollTop: (value) => { savedScrollTop = value; }, setSavedDistFromBottom: (value) => { savedDistFromBottom = value; } });

  const MAX_REBUILD_RETRIES = 6;

  function handleCollapsedContainer(el, retryCount, tryRestore) {
    domRebuildInProgress = true;
    if (retryCount < MAX_REBUILD_RETRIES) { tryRestore(); return; }
    domRebuildInProgress = false;
    logWarn('scroll', 'scroll.mutation.rebuild_timeout', { retryCount, scrollHeight: el.scrollHeight, clientHeight: el.clientHeight, savedScrollTop });
  }

  function performMutationRestore(tryRestore, retryState) {
    mutationRestoreRaf = 0;
    if (programmaticScrollInProgress || drag.userDraggingScrollbar) return;
    const el = resolveChatScroller();
    if (!el) return;

    // 必须要在检测前临时移除 minHeight 锁，否则 minHeight 会导致 scrollHeight === clientHeight，误判为塌缩！
    const prevMinHeight = el.style.minHeight;
    if (prevMinHeight) el.style.minHeight = '';

    if (el.scrollHeight <= el.clientHeight + 2) {
      if (prevMinHeight) el.style.minHeight = prevMinHeight; // 恢复锁
      retryState.count += 1;
      handleCollapsedContainer(el, retryState.count, tryRestore);
      return;
    }
    domRebuildInProgress = false;

    if (savedDistFromBottom <= 96) {
      if (shouldAutoScroll.value) { 
        el.scrollTop = savedScrollTop = el.scrollHeight; 
        savedDistFromBottom = 0; 
      } else {
        logWarn('scroll', 'scroll.mutation.aborted.bottom', { 
          savedDistFromBottom, 
          shouldAutoScroll: shouldAutoScroll.value,
          scrollHeight: el.scrollHeight,
          scrollTop: el.scrollTop
        });
      }
      unlockContainerHeight();
      if (snapshotGuardActive) { snapshotGuardActive = false; if (snapshotGuardTimer) { clearTimeout(snapshotGuardTimer); snapshotGuardTimer = 0; } }
      return;
    }
    
    logWarn('scroll', 'scroll.mutation.restoring', { savedScrollTop, savedDistFromBottom, shouldAutoScroll: shouldAutoScroll.value, scrollHeight: el.scrollHeight, clientHeight: el.clientHeight });
    
    if (savedScrollTop > 0 && Math.abs(el.scrollTop - savedScrollTop) > 4) {
      if (el.scrollTop === 0 && savedScrollTop > 100) {
        logWarn('scroll', 'scroll.mutation.unexpected_reset', { savedScrollTop, scrollHeight: el.scrollHeight, clientHeight: el.clientHeight, shouldAutoScroll: shouldAutoScroll.value, stack: new Error('[diag]').stack });
      }
      el.scrollTop = savedScrollTop;
      logWarn('scroll', 'scroll.mutation.restored', { savedScrollTop, currentScrollTop: el.scrollTop, scrollHeight: el.scrollHeight, stack: new Error('[diag]').stack });
    }
    unlockContainerHeight();
    if (snapshotGuardActive) { snapshotGuardActive = false; if (snapshotGuardTimer) { clearTimeout(snapshotGuardTimer); snapshotGuardTimer = 0; } }
  }

  function onDOMMutation() {
    if (programmaticScrollInProgress || mutationRestoreRaf) return;
    const retryState = { count: 0 };
    const tryRestore = () => { mutationRestoreRaf = requestAnimationFrame(() => performMutationRestore(tryRestore, retryState)); };
    tryRestore();
  }

  function attachMutationObserver(el) {
    if (mutationObserver) mutationObserver.disconnect();
    (mutationObserver = new MutationObserver(onDOMMutation)).observe(el, { childList: true, subtree: false });
  }

  function scheduleScrollToBottom(force = false) {
    if (scrollTimer) cancelAnimationFrame(scrollTimer);
    logDebug('scroll', 'scroll.to_bottom.called', { force, shouldAutoScroll: shouldAutoScroll.value, isAtBottom: isAtBottom.value, userDragging: drag.userDraggingScrollbar });
    if (drag.userDraggingScrollbar && !force) return;
    programmaticScrollInProgress = true;
    const doScroll = () => {
      scrollTimer = requestAnimationFrame(() => {
        unlockContainerHeight();
        scrollTimer = requestAnimationFrame(() => {
          const el = resolveChatScroller();
          if (!el || (!force && !shouldAutoScroll.value)) return programmaticScrollInProgress = false;
          const prevTop = el.scrollTop;
          el.scrollTop = savedScrollTop = el.scrollHeight;
          isAtBottom.value = true;
          savedDistFromBottom = 0;
          if (force) shouldAutoScroll.value = true;
          if (Math.abs(el.scrollTop - prevTop) > 2) logDebug('scroll', 'scroll.to_bottom.done', { from: prevTop, to: el.scrollTop, scrollHeight: el.scrollHeight, force });
          requestAnimationFrame(() => programmaticScrollInProgress = false);
        });
      });
    };
    force ? nextTick(doScroll) : doScroll();
  }

  function scrollToTop() {
    logInfo('scroll', 'scroll.to_top.called', { stack: new Error('[diag]').stack });
    if (scrollTimer) cancelAnimationFrame(scrollTimer);
    programmaticScrollInProgress = true;
    scrollTimer = requestAnimationFrame(() => {
      const el = resolveChatScroller();
      if (!el) return programmaticScrollInProgress = false;
      el.scrollTop = savedScrollTop = 0;
      isAtBottom.value = false;
      savedDistFromBottom = el.scrollHeight - el.clientHeight;
      requestAnimationFrame(() => programmaticScrollInProgress = false);
    });
  }

  function onChatScroll() {
    const el = resolveChatScroller();
    if (!el || domRebuildInProgress || snapshotGuardActive) return;

    const currentTop = el.scrollTop, prevTop = lastScrollTop, isUp = currentTop < lastScrollTop;
    lastScrollTop = currentTop;

    if (prevTop > 100 && currentTop === 0 && !programmaticScrollInProgress) {
      const childCount = el.childElementCount || 0;
      const firstChildTag = el.firstElementChild?.tagName || '';
      const lastChildTag = el.lastElementChild?.tagName || '';
      logWarn('scroll', 'scroll.unexpected_top', {
        prevScrollTop: prevTop, curScrollTop: currentTop,
        scrollHeight: el.scrollHeight, clientHeight: el.clientHeight,
        childCount, firstChildTag, lastChildTag,
        containerEmpty: el.scrollHeight <= el.clientHeight + 2,
        shouldAutoScroll: shouldAutoScroll.value,
        savedScrollTop,
        stack: new Error('[diag]').stack,
      });
      savedDistFromBottom = el.scrollHeight - el.clientHeight;
      return;
    }

    savedScrollTop = currentTop;
    savedDistFromBottom = el.scrollHeight - currentTop - el.clientHeight;
    const nearBottom = isNearBottom(el);
    isAtBottom.value = nearBottom;

    if (isUp && !nearBottom) {
      if (shouldAutoScroll.value) logDebug('scroll', 'scroll.user.up', { scrollTop: currentTop, distFromBottom: distanceFromBottom(el) });
      shouldAutoScroll.value = false;
    } else if (nearBottom) {
      shouldAutoScroll.value = true;
    }
  }

  function resetScrollState() {
    if (snapshotGuardActive) {
      logWarn('scroll', 'scroll.state.reset.skipped_guard', { savedScrollTop, snapshotGuardActive });
      return;
    }
    logWarn('scroll', 'scroll.state.reset', { savedScrollTop, savedDistFromBottom, stack: new Error('[diag]').stack });
    const el = resolveChatScroller();
    if (el) {
      savedScrollTop = el.scrollTop;
      savedDistFromBottom = el.scrollHeight - el.scrollTop - el.clientHeight;
      // 锁住容器高度，防止线程切换 DOM 重建时视觉塌缩导致浏览器强制 scrollTop=0
      el.style.minHeight = Math.max(el.scrollHeight, el.clientHeight) + 'px';
      // 激活快照保护，防范 DOM patch 期间浏览器原生 onscroll 事件污染 autoScroll 状态
      snapshotGuardActive = true;
      if (snapshotGuardTimer) clearTimeout(snapshotGuardTimer);
      snapshotGuardTimer = setTimeout(() => { snapshotGuardActive = false; snapshotGuardTimer = 0; unlockContainerHeight(); }, 2000);
    } else {
      savedScrollTop = savedDistFromBottom = 0;
    }
    isAtBottom.value = shouldAutoScroll.value = programmaticScrollInProgress = true;
    requestAnimationFrame(() => programmaticScrollInProgress = false);
  }

  function saveScrollPosition() {
    const el = resolveChatScroller();
    if (!el) return;
    savedScrollTop = el.scrollTop;
    savedDistFromBottom = el.scrollHeight - el.scrollTop - el.clientHeight;
    // 锁住容器高度，防止 DOM 重建时视觉塌缩闪烁
    el.style.minHeight = el.scrollHeight + 'px';
    if (el.scrollTop === 0 && el.scrollHeight > el.clientHeight + 2) {
      logWarn('scroll', 'scroll.save.zero_top', { scrollHeight: el.scrollHeight, clientHeight: el.clientHeight, shouldAutoScroll: shouldAutoScroll.value, stack: new Error('[diag]').stack });
    }
    // 激活快照保护：阻止 onChatScroll 污染 savedScrollTop，但不阻止 MutationObserver 恢复
    snapshotGuardActive = true;
    if (snapshotGuardTimer) clearTimeout(snapshotGuardTimer);
    snapshotGuardTimer = setTimeout(() => { snapshotGuardActive = false; snapshotGuardTimer = 0; unlockContainerHeight(); }, 2000);
  }

  function unlockContainerHeight() {
    const el = resolveChatScroller();
    if (el && el.style.minHeight) el.style.minHeight = '';
  }

  function restoreScrollPosition() {
    // 用户正在拖动滚动条时，跳过所有自动恢复
    if (drag.userDraggingScrollbar) return;
    // 同步尝试恢复（如果 DOM 已就绪）
    const el = resolveChatScroller();
    if (el) {
      // 临时移除高度锁进行真实测量
      const prevMinHeight = el.style.minHeight;
      if (prevMinHeight) el.style.minHeight = '';

      if (el.scrollHeight > el.clientHeight + 2) {
        if (shouldAutoScroll.value) {
          el.scrollTop = savedScrollTop = el.scrollHeight;
          savedDistFromBottom = 0;
        } else if (savedScrollTop > 0) {
          el.scrollTop = savedScrollTop;
        } else {
          logWarn('scroll', 'scroll.restore.noop_zero', { savedScrollTop, scrollTop: el.scrollTop, scrollHeight: el.scrollHeight, shouldAutoScroll: shouldAutoScroll.value, stack: new Error('[diag]').stack });
        }
        snapshotGuardActive = false;
        if (snapshotGuardTimer) { clearTimeout(snapshotGuardTimer); snapshotGuardTimer = 0; }
        // 成功恢复不需要再加锁
      } else {
        if (prevMinHeight) el.style.minHeight = prevMinHeight; // 恢复锁，等待 MutationObserver
        logWarn('scroll', 'scroll.restore.collapsed', { scrollHeight: el.scrollHeight, clientHeight: el.clientHeight, savedScrollTop, snapshotGuardActive, stack: new Error('[diag]').stack });
      }
    }
    // 如果 DOM 还塌缩（scrollHeight <= clientHeight），
    // snapshotGuardActive 保持 true，由 MutationObserver 在 DOM 重建后恢复
  }

  function attachScrollListener() {
    scrollListenerCleanup();
    scrollListenerCleanup = () => {};
    const el = resolveChatScroller();
    if (!el || el === lastScrollerIdentity) return;
    const isReattach = lastScrollerIdentity !== null;
    lastScrollerIdentity = el;
    el.addEventListener('scroll', onChatScroll, { passive: true });
    el.addEventListener('mousedown', userScrollHandlers.onScrollbarMouseDown);
    el.addEventListener('wheel', userScrollHandlers.onUserWheel, { passive: true });
    el.addEventListener('keydown', userScrollHandlers.onUserKeyDown, { passive: true });
    if (typeof window !== 'undefined') window.addEventListener('mouseup', userScrollHandlers.onScrollbarMouseUp);
    if (isReattach && savedScrollTop > 0 && el.scrollTop === 0 && el.scrollHeight > el.clientHeight + 2) {
      // DOM 重建后新元素 scrollTop=0，恢复之前保存的位置
      el.scrollTop = savedScrollTop;
      logWarn('scroll', 'scroll.reattach.restored', { savedScrollTop, scrollHeight: el.scrollHeight, clientHeight: el.clientHeight });
    } else if (isReattach) {
      logWarn('scroll', 'scroll.reattach.overwrite', { prevSavedScrollTop: savedScrollTop, newScrollTop: el.scrollTop, scrollHeight: el.scrollHeight, clientHeight: el.clientHeight });
      savedScrollTop = el.scrollTop;
      savedDistFromBottom = el.scrollHeight - el.scrollTop - el.clientHeight;
    } else {
      savedScrollTop = el.scrollTop;
      savedDistFromBottom = el.scrollHeight - el.scrollTop - el.clientHeight;
    }
    attachMutationObserver(el);
    scrollListenerCleanup = () => {
      el.removeEventListener('scroll', onChatScroll);
      el.removeEventListener('mousedown', userScrollHandlers.onScrollbarMouseDown);
      el.removeEventListener('wheel', userScrollHandlers.onUserWheel);
      el.removeEventListener('keydown', userScrollHandlers.onUserKeyDown);
      if (typeof window !== 'undefined') window.removeEventListener('mouseup', userScrollHandlers.onScrollbarMouseUp);
      if (mutationObserver) mutationObserver.disconnect(), mutationObserver = null;
      lastScrollerIdentity = null;
    };
    logDebug('scroll', 'scroll.listener.attached', { scrollHeight: el.scrollHeight, clientHeight: el.clientHeight, isReattach });
  }

  onMounted(() => {
    setTimeout(attachScrollListener, 100);
    reattachCheckTimer = setInterval(() => {
      const el = resolveChatScroller();
      if (el && el !== lastScrollerIdentity) {
        logWarn('scroll', 'scroll.reattach.detected', { savedScrollTop, newElScrollTop: el.scrollTop, scrollHeight: el.scrollHeight, clientHeight: el.clientHeight });
        attachScrollListener();
      }
    }, 2000);
  });

  onBeforeUnmount(() => {
    scrollListenerCleanup();
    if (scrollTimer) cancelAnimationFrame(scrollTimer);
    if (mutationRestoreRaf) cancelAnimationFrame(mutationRestoreRaf);
    if (reattachCheckTimer) clearInterval(reattachCheckTimer);
    if (mutationObserver) mutationObserver.disconnect();
    if (snapshotGuardTimer) clearTimeout(snapshotGuardTimer);
    userScrollHandlers.cleanup();
  });

  return { shouldAutoScroll, isAtBottom, scheduleScrollToBottom, scrollToTop, resetScrollState, saveScrollPosition, restoreScrollPosition, resolveChatScroller, /** @internal test-only */ _setUserDragging: (v) => { drag.userDraggingScrollbar = v; } };
}
