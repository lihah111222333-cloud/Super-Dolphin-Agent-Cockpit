import { onFilesDropped } from '../services/api.js';
import { logInfo } from '../services/log.js';

/**
 * 处理 Wails 原生文件拖放事件，将文件附加到 composer。
 *
 * @param {{ attachByPaths: (paths: string[], source: string) => number }} composer
 * @returns {{ registerFileDrop: () => (() => void) }}
 */
export function useFileDrop(composer) {
  function onNativeFilesDropped(evt) {
    const payload = evt && typeof evt === 'object' ? evt : {};
    const files = Array.isArray(payload.files) ? payload.files : [];
    if (files.length === 0) return;

    const details = payload.details && typeof payload.details === 'object'
      ? payload.details
      : {};
    const targetID = (details.id || '').toString().trim();
    if (targetID && targetID !== 'chat-input-bar') return;

    const added = composer.attachByPaths(files, 'wails-drop');
    logInfo('ui', 'chat.nativeFilesDropped.handled', {
      files: files.length,
      added,
      target_id: targetID,
    });
  }

  /**
   * 注册文件拖放监听，返回取消注册的函数。
   * @returns {() => void}
   */
  function registerFileDrop() {
    return onFilesDropped(onNativeFilesDropped);
  }

  return { registerFileDrop };
}
