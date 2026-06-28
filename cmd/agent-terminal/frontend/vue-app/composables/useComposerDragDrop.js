import { ref } from '../../lib/vue.esm-browser.prod.js';
import { onFilesDropped } from '../services/api.js';
import { logDebug, logInfo } from '../services/log.js';

/**
 * 管理 ComposerBar 的附件拖拽与原生文件投递。
 *
 * @param {object} props - ComposerBar props
 */
export function useComposerDragDrop(props) {
  const dropActive = ref(false);
  const dropDepth = ref(0);
  let offFilesDropped = () => { };

  function hasFilesTransfer(event) {
    const transfer = event?.dataTransfer;
    if (!transfer) return false;
    if (transfer.files && transfer.files.length > 0) return true;
    if (!transfer.types) return false;
    return Array.from(transfer.types).includes('Files');
  }

  function resetDropState() {
    dropDepth.value = 0;
    dropActive.value = false;
  }

  function onDragEnter(event) {
    if (props.disabled) return;
    if (!hasFilesTransfer(event)) return;
    if (typeof event.preventDefault === 'function') event.preventDefault();
    dropDepth.value += 1;
    dropActive.value = true;
  }

  function onDragOver(event) {
    if (props.disabled) return;
    if (!hasFilesTransfer(event)) return;
    if (typeof event.preventDefault === 'function') event.preventDefault();
    const transfer = event?.dataTransfer;
    if (transfer) transfer.dropEffect = 'copy';
    if (!dropActive.value) dropActive.value = true;
  }

  function onDragLeave(event) {
    if (!dropActive.value) return;
    if (typeof event.preventDefault === 'function') event.preventDefault();
    dropDepth.value = Math.max(dropDepth.value - 1, 0);
    if (dropDepth.value === 0) dropActive.value = false;
  }

  async function onDrop(event) {
    if (!hasFilesTransfer(event)) return;
    if (typeof event.preventDefault === 'function') event.preventDefault();
    resetDropState();
    if (props.disabled) return;
    try {
      await props.composer.handleDrop(event);
    } catch (error) {
      logDebug('ui', 'composerBar.drop.failed', { error });
    }
  }

  function onNativeFilesDropped(evt) {
    if (props.disabled) return;
    const payload = evt && typeof evt === 'object' ? evt : {};
    const files = Array.isArray(payload.files) ? payload.files : [];
    if (files.length === 0) return;

    const details = payload.details && typeof payload.details === 'object' ? payload.details : {};
    const targetID = (details.id || '').toString().trim();
    if (targetID && targetID !== 'chat-input-bar') return;

    const added = props.composer.attachByPaths(files, 'wails-drop');
    if (added > 0) resetDropState();
    logInfo('ui', 'composerBar.nativeDrop.handled', {
      files: files.length,
      added,
      target_id: targetID,
    });
  }

  function bindNativeFileDrop() {
    offFilesDropped = onFilesDropped(onNativeFilesDropped);
  }

  function unbindNativeFileDrop() {
    offFilesDropped();
    offFilesDropped = () => { };
  }

  return {
    dropActive,
    dropDepth,
    resetDropState,
    onDragEnter,
    onDragOver,
    onDragLeave,
    onDrop,
    bindNativeFileDrop,
    unbindNativeFileDrop,
  };
}
