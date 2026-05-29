import {
  ref,
  onMounted,
  onBeforeUnmount,
} from '../../lib/vue.esm-browser.prod.js';

import { onFilesDropped, readDroppedTextFiles } from '../services/api.js';
import { logWarn } from '../services/log.js';
import { toErrorMessage } from '../pages/PromptIntentWizard.helpers.js';

export const PROMPT_INTENT_DROP_ZONE_ID = 'prompt-intent-drop-zone';

const MAX_BROWSER_DROPPED_TEXT_FILE_BYTES = 2 << 20;
const DUPLICATE_DROP_WINDOW_MS = 1500;
const TEXT_FILE_EXT_RE = /\.(txt|md|markdown|json|ya?ml|csv|tsv|sql|go|js|jsx|ts|tsx|py|java|rs|toml|xml|html?|css|scss|sh|bash|zsh|log)$/i;

function hasFilesTransfer(event) {
  const transfer = event?.dataTransfer;
  if (!transfer) return false;
  if (transfer.files && transfer.files.length > 0) return true;
  if (!transfer.types) return false;
  return Array.from(transfer.types).includes('Files');
}

function collectDroppedFiles(event) {
  const files = event?.dataTransfer?.files;
  if (files && files.length > 0) return Array.from(files).filter(Boolean);

  const items = event?.dataTransfer?.items;
  if (!items || items.length === 0) return [];
  const out = [];
  for (const item of Array.from(items)) {
    if (item?.kind !== 'file') continue;
    const file = item.getAsFile?.();
    if (file) out.push(file);
  }
  return out;
}

function fileDisplayName(file, index = 0) {
  const name = (file?.name || '').toString().trim();
  if (name) return name;
  const path = (file?.path || '').toString().trim();
  if (path) {
    const parts = path.split(/[\\/]/);
    return parts[parts.length - 1] || path;
  }
  return `dropped-file-${index + 1}`;
}

function isTextLikeBrowserFile(file) {
  const type = (file?.type || '').toString().toLowerCase();
  const name = fileDisplayName(file).toLowerCase();
  if (type.startsWith('text/')) return true;
  if (type.includes('json') || type.includes('xml') || type.includes('yaml') || type.includes('javascript')) return true;
  return TEXT_FILE_EXT_RE.test(name);
}

async function readBrowserFileText(file) {
  if (typeof file?.text === 'function') {
    return file.text();
  }
  if (typeof FileReader === 'undefined') {
    throw new Error('当前环境无法读取拖拽文件内容');
  }
  return new Promise((resolve, reject) => {
    const reader = new FileReader();
    reader.onload = () => resolve((reader.result || '').toString());
    reader.onerror = () => reject(reader.error || new Error('读取拖拽文件失败'));
    reader.readAsText(file);
  });
}

async function readBrowserDroppedTextFiles(files) {
  const out = [];
  for (let index = 0; index < files.length; index += 1) {
    const file = files[index];
    const name = fileDisplayName(file, index);
    const size = Number(file?.size) || 0;
    if (size > MAX_BROWSER_DROPPED_TEXT_FILE_BYTES) {
      throw new Error(`文件过大：${name}`);
    }
    if (!isTextLikeBrowserFile(file)) {
      throw new Error(`不支持的文件类型：${name}`);
    }
    const text = (await readBrowserFileText(file)).replaceAll('\r\n', '\n').replaceAll('\r', '\n');
    if (!text.trim()) {
      throw new Error(`文件没有可读取的文本内容：${name}`);
    }
    out.push({ name, text, sizeBytes: size });
  }
  return out;
}

function droppedItemName(item, index = 0) {
  return (item?.name || fileDisplayName(item, index)).toString().trim() || `dropped-file-${index + 1}`;
}

export function usePromptIntentFileDrop({
  props,
  state,
  rawInput,
  notice,
  noticeLevel = null,
}) {
  const dropActive = ref(false);
  const dropDepth = ref(0);
  let offFilesDropped = () => {};
  let lastDropKey = '';
  let lastDropAt = 0;

  function isDropDisabled() {
    return props.fallbackMode || ['drafting', 'dry_running', 'committing'].includes(state.value);
  }

  function resetDropState() {
    dropDepth.value = 0;
    dropActive.value = false;
  }

  function droppedItemsKey(items) {
    return (Array.isArray(items) ? items : [])
      .map((item, index) => droppedItemName(item, index).toLowerCase())
      .filter(Boolean)
      .sort()
      .join('\n');
  }

  function markDropIngest(items) {
    const key = droppedItemsKey(items);
    if (!key) return true;
    const now = Date.now();
    if (key === lastDropKey && now - lastDropAt < DUPLICATE_DROP_WINDOW_MS) {
      return false;
    }
    lastDropKey = key;
    lastDropAt = now;
    return true;
  }

  function setNotice(message, level = 'error') {
    if (noticeLevel && typeof noticeLevel === 'object') {
      noticeLevel.value = level;
    }
    notice.value = message;
  }

  function appendDroppedTextFiles(files) {
    const items = (Array.isArray(files) ? files : [])
      .map((file, index) => {
        const name = droppedItemName(file, index);
        const text = (file?.text || '').toString().replaceAll('\r\n', '\n').replaceAll('\r', '\n').trim();
        return { name, text };
      })
      .filter((item) => item.name && item.text);
    if (items.length === 0) {
      setNotice('没有可读取的文本内容', 'error');
      return false;
    }
    if (!markDropIngest(items)) return false;
    const blocks = items.map((item) => `文件：${item.name}\n${item.text}`);
    const current = (rawInput.value || '').toString();
    const separator = current.trim() ? '\n\n' : '';
    rawInput.value = `${current}${separator}${blocks.join('\n\n---\n\n')}`;
    setNotice(`已读取 ${items.length} 个文件`, 'info');
    return true;
  }

  function setDropError(error) {
    logWarn('prompt-intent-wizard', 'drop.read.failed', { error });
    setNotice(`读取文件失败：${toErrorMessage(error)}`, 'error');
  }

  async function ingestBrowserDroppedFiles(files) {
    const items = await readBrowserDroppedTextFiles(files);
    appendDroppedTextFiles(items);
  }

  async function ingestNativeDroppedFiles(paths, targetID) {
    const items = await readDroppedTextFiles(paths, targetID || PROMPT_INTENT_DROP_ZONE_ID);
    appendDroppedTextFiles(items);
  }

  function onDragEnter(event) {
    if (isDropDisabled()) return;
    if (!hasFilesTransfer(event)) return;
    if (typeof event.preventDefault === 'function') event.preventDefault();
    dropDepth.value += 1;
    dropActive.value = true;
  }

  function onDragOver(event) {
    if (isDropDisabled()) return;
    if (!hasFilesTransfer(event)) return;
    if (typeof event.preventDefault === 'function') event.preventDefault();
    const transfer = event?.dataTransfer;
    if (transfer) transfer.dropEffect = 'copy';
    dropActive.value = true;
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
    if (isDropDisabled()) return;
    const files = collectDroppedFiles(event);
    if (files.length === 0) return;
    try {
      await ingestBrowserDroppedFiles(files);
    } catch (error) {
      setDropError(error);
    }
  }

  function onNativeFilesDropped(evt) {
    if (!props.visible || isDropDisabled()) return;
    const payload = evt && typeof evt === 'object' ? evt : {};
    const files = Array.isArray(payload.files)
      ? payload.files.map((item) => (item || '').toString().trim()).filter(Boolean)
      : [];
    if (files.length === 0) return;
    const details = payload.details && typeof payload.details === 'object' ? payload.details : {};
    const targetID = (details.id || '').toString().trim();
    if (targetID && targetID !== PROMPT_INTENT_DROP_ZONE_ID) return;
    resetDropState();
    ingestNativeDroppedFiles(files, targetID).catch(setDropError);
  }

  function bindNativeFileDrop() {
    offFilesDropped = onFilesDropped(onNativeFilesDropped);
  }

  function unbindNativeFileDrop() {
    offFilesDropped();
    offFilesDropped = () => {};
  }

  onMounted(bindNativeFileDrop);
  onBeforeUnmount(unbindNativeFileDrop);

  return {
    dropActive,
    dropDepth,
    resetDropState,
    onDragEnter,
    onDragOver,
    onDragLeave,
    onDrop,
  };
}
