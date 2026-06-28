/**
 * 纯函数辅助模块 — 从 useAutoScroll 中提取的无状态工具。
 * 便于单元测试且减少主 composable 体积。
 */

/**
 * 在 workspaceRef + document 中查找 .chat-messages-vue 滚动容器。
 * @param {{ value: Element | null }} workspaceRef
 * @returns {Element | null}
 */
export function resolveChatScroller(workspaceRef) {
  const root = workspaceRef.value;
  if (root && typeof root.querySelector === 'function') {
    const within = root.querySelector('.chat-messages-vue');
    if (within) return within;
  }
  return document.querySelector('.chat-messages-vue');
}

/** 元素底部距离 */
export function distanceFromBottom(el) {
  if (!el) return 0;
  return el.scrollHeight - el.scrollTop - el.clientHeight;
}

/** 是否在底部附近 */
export function isNearBottom(el, threshold = 96) {
  return distanceFromBottom(el) <= threshold;
}
