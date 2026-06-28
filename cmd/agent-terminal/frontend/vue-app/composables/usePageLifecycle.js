import { onMounted, onBeforeUnmount } from '../../lib/vue.esm-browser.prod.js';

/**
 * 统一注册 UnifiedChatPage 的生命周期副作用：
 * - 全局键盘 Escape 监听
 * - Wails 原生文件拖放监听
 * - Provider 偏好加载
 * - 卸载时清理（timers、clipboard、listeners）
 *
 * @param {{
 *   keyboardShortcuts: { onGlobalEscape: (e: KeyboardEvent) => void },
 *   registerFileDrop: () => (() => void),
 *   loadProviderPreference: () => void,
 *   copyThreadInfoCleanup: () => void,
 *   stopStatusTickTimer: () => void,
 * }} options
 */
export function usePageLifecycle({
  keyboardShortcuts,
  registerFileDrop,
  loadProviderPreference,
  copyThreadInfoCleanup,
  stopStatusTickTimer,
}) {
  let offFilesDropped = () => { };

  onMounted(() => {
    window.addEventListener('keydown', keyboardShortcuts.onGlobalEscape, true);
    document.addEventListener('keydown', keyboardShortcuts.onGlobalEscape, true);
    offFilesDropped = registerFileDrop();
    loadProviderPreference();
  });

  onBeforeUnmount(() => {
    window.removeEventListener('keydown', keyboardShortcuts.onGlobalEscape, true);
    document.removeEventListener('keydown', keyboardShortcuts.onGlobalEscape, true);
    offFilesDropped();
    offFilesDropped = () => { };
    copyThreadInfoCleanup();
    stopStatusTickTimer();
  });
}
