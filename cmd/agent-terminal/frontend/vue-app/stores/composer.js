import { reactive, computed } from '../../lib/vue.esm-browser.prod.js';
import { saveClipboardImage, selectFiles } from '../services/api.js';
import { logDebug, logInfo, logWarn } from '../services/log.js';

const state = reactive({
  text: '',
  attachments: [],
  attaching: false,
});

function clearComposer() {
  const attachmentCount = state.attachments.length;
  state.text = '';
  state.attachments = [];
  logDebug('composer', 'cleared', { attachment_count: attachmentCount });
}

function removeAttachment(index) {
  const target = state.attachments[index];
  state.attachments.splice(index, 1);
  logDebug('composer', 'attachment.removed', {
    index,
    name: target?.name || '',
    count: state.attachments.length,
  });
}

function pushAttachment(attachment) {
  const path = (attachment?.path || '').trim();
  const previewUrl = (attachment?.previewUrl || '').trim();
  const key = path || previewUrl;
  if (!key) return;
  if (state.attachments.some((item) => ((item.path || '').trim() || (item.previewUrl || '').trim()) === key)) return;
  state.attachments.push({
    ...attachment,
    path,
    previewUrl,
  });
  logInfo('composer', 'attachment.added', {
    kind: attachment.kind,
    name: attachment.name,
    count: state.attachments.length,
    has_path: Boolean(path),
  });
}

function normalizeFileAttachment(path) {
  const value = (path || '').trim();
  if (!value) return null;
  const parts = value.split(/[\\/]/);
  const name = parts[parts.length - 1] || value;
  const lower = name.toLowerCase();
  const image = /\.(png|jpg|jpeg|gif|webp|bmp|svg)$/.test(lower);
  return {
    kind: image ? 'image' : 'file',
    name,
    path: value,
    previewUrl: image ? `file://${value}` : '',
  };
}

function collectDroppedFiles(event) {
  const files = event?.dataTransfer?.files;
  if (files && files.length > 0) return Array.from(files).filter(Boolean);

  const items = event?.dataTransfer?.items;
  if (!items || items.length === 0) return [];
  const normalized = [];
  for (const item of items) {
    if (item?.kind !== 'file') continue;
    const file = item.getAsFile?.();
    if (file) normalized.push(file);
  }
  return normalized;
}

async function normalizeDroppedFileAttachment(file, index) {
  const path = (file?.path || '').toString().trim();
  if (path) return normalizeFileAttachment(path);

  const type = (file?.type || '').toString().toLowerCase();
  if (!type.startsWith('image/')) {
    logWarn('composer', 'drop.file.ignored.noPath', {
      name: (file?.name || '').toString(),
      type,
    });
    return null;
  }

  const dataUrl = await blobToDataURL(file);
  const base64 = dataUrl.split(',')[1] || '';
  const tempPath = await saveClipboardImage(base64);
  return {
    kind: 'image',
    name: (file?.name || `dropped-image-${Date.now()}-${index}.png`).toString(),
    path: (tempPath || '').toString(),
    previewUrl: dataUrl,
  };
}

async function attachByPicker() {
  // UI intent only: actual file chooser is provided by Wails bridge (Go).
  state.attaching = true;
  const start = Date.now();
  logInfo('composer', 'picker.start', {});
  try {
    const paths = await selectFiles();
    const added = attachByPaths(paths, 'picker');
    logInfo('composer', 'picker.done', {
      selected: paths.length,
      added,
      duration_ms: Date.now() - start,
    });
  } catch (error) {
    logWarn('composer', 'picker.failed', {
      error,
      duration_ms: Date.now() - start,
    });
  } finally {
    state.attaching = false;
  }
}

function attachByPaths(paths, source = 'external') {
  const list = Array.isArray(paths)
    ? paths.map((item) => (item || '').toString().trim()).filter(Boolean)
    : [];
  if (list.length === 0) {
    logDebug('composer', 'paths.ignored.empty', { source });
    return 0;
  }

  let added = 0;
  list.forEach((path) => {
    const before = state.attachments.length;
    const attachment = normalizeFileAttachment(path);
    if (!attachment) return;
    pushAttachment(attachment);
    if (state.attachments.length > before) added += 1;
  });

  logInfo('composer', 'paths.done', {
    source,
    files: list.length,
    added,
    dropped: Math.max(list.length - added, 0),
  });
  return added;
}

async function handlePaste(event) {
  const items = event?.clipboardData?.items;
  if (!items || items.length === 0) return false;

  for (const item of items) {
    if (!item.type.startsWith('image/')) continue;
    event.preventDefault();

    const blob = item.getAsFile();
    if (!blob) continue;
    try {
      const dataUrl = await blobToDataURL(blob);
      const base64 = dataUrl.split(',')[1] || '';
      const tempPath = await saveClipboardImage(base64);

      pushAttachment({
        kind: 'image',
        name: `screenshot-${Date.now()}.png`,
        path: tempPath || '',
        previewUrl: dataUrl,
      });
      logInfo('composer', 'paste.image.added', { has_path: Boolean(tempPath) });
      return true;
    } catch (error) {
      logWarn('composer', 'paste.image.failed', { error });
      return false;
    }
  }

  logDebug('composer', 'paste.ignored', {});
  return false;
}

async function handleDrop(event) {
  const files = collectDroppedFiles(event);
  if (files.length === 0) {
    logDebug('composer', 'drop.ignored.noFiles', {});
    return false;
  }

  event.preventDefault();

  let added = 0;
  for (let index = 0; index < files.length; index += 1) {
    const file = files[index];
    try {
      const attachment = await normalizeDroppedFileAttachment(file, index);
      if (!attachment) continue;
      pushAttachment(attachment);
      added += 1;
    } catch (error) {
      logWarn('composer', 'drop.file.failed', {
        name: (file?.name || '').toString(),
        error,
      });
    }
  }

  logInfo('composer', 'drop.done', {
    files: files.length,
    added,
    dropped: Math.max(files.length - added, 0),
  });
  return added > 0;
}

async function blobToDataURL(blob) {
  return new Promise((resolve, reject) => {
    const reader = new FileReader();
    reader.onload = () => resolve(reader.result || '');
    reader.onerror = () => reject(reader.error || new Error('read blob failed'));
    reader.readAsDataURL(blob);
  });
}


export function useComposerStore() {
  return {
    state,
    canSend: computed(() => {
      const text = (state.text || '').trim();
      return Boolean(text) || state.attachments.length > 0;
    }),
    clearComposer,
    removeAttachment,
    attachByPicker,
    attachByPaths,
    handlePaste,
    handleDrop,

  };
}
