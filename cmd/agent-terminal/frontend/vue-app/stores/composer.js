import { reactive, computed } from '../../lib/vue.esm-browser.prod.js';
import { saveClipboardImage, selectFiles } from '../services/api.js';
import { logDebug, logInfo, logWarn } from '../services/log.js';
import { createComposerDraftController } from './composer-drafts.js';

const state = reactive({
  text: '',
  attachments: [],
  attaching: false,
});
const draftController = createComposerDraftController(state, logDebug);

// Phase 2: 「新建继承对话」卡片状态。独立于 composer 输入状态，主要处理：
// - 卡片是否展开
// - 预挂载的共享文件路径列表（内容提交时才拉）
// - 仅记录启动来源（供日志，不影响逻辑）
const forkDraft = reactive({
  active: false,
  sharedFilePaths: /** @type {string[]} */ ([]),
  origin: '',
});

function openForkDraft(options = {}) {
  forkDraft.active = true;
  forkDraft.origin = (options?.origin || '').toString().trim();
  const seedPath = (options?.sharedFilePath || '').toString().trim();
  if (seedPath && !forkDraft.sharedFilePaths.includes(seedPath)) {
    forkDraft.sharedFilePaths.push(seedPath);
  }
  logInfo('composer', 'forkDraft.opened', { origin: forkDraft.origin, seed_path: seedPath, total: forkDraft.sharedFilePaths.length });
}

function closeForkDraft() {
  forkDraft.active = false;
  forkDraft.sharedFilePaths = [];
  forkDraft.origin = '';
  logDebug('composer', 'forkDraft.closed', {});
}

function addForkSharedFile(path) {
  const value = (path || '').toString().trim();
  if (!value) return false;
  if (forkDraft.sharedFilePaths.includes(value)) return false;
  forkDraft.sharedFilePaths.push(value);
  logInfo('composer', 'forkDraft.shared_file.added', { path: value, total: forkDraft.sharedFilePaths.length });
  return true;
}

function removeForkSharedFile(path) {
  const value = (path || '').toString().trim();
  if (!value) return false;
  const idx = forkDraft.sharedFilePaths.indexOf(value);
  if (idx < 0) return false;
  forkDraft.sharedFilePaths.splice(idx, 1);
  logInfo('composer', 'forkDraft.shared_file.removed', { path: value, total: forkDraft.sharedFilePaths.length });
  return true;
}

function clearComposer() {
  const attachmentCount = state.attachments.length;
  draftController.clearCurrentDraft();
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

const IMAGE_EXT_RE = /\.(png|jpe?g|gif|webp|bmp|svg)$/i;
const IMAGE_REF_PLACEHOLDER_RE = /<image\s+name\s*=\s*\[?[^>\]]*\]?\s*>.*?<\/image>/i;

function extractClipboardImages(event) {
  const out = [];
  const seen = new Set();
  const push = (blob) => {
    if (!blob || seen.has(blob)) return;
    seen.add(blob);
    out.push(blob);
  };
  const clipboard = event?.clipboardData;
  if (!clipboard) return out;

  // 1) files[] 是最可靠的图像载体（原生截屏 / 外部应用 copy image）
  const files = clipboard.files;
  if (files && files.length > 0) {
    for (const file of Array.from(files)) {
      const type = (file?.type || '').toLowerCase();
      const name = (file?.name || '').toLowerCase();
      if (type.startsWith('image/') || IMAGE_EXT_RE.test(name)) push(file);
    }
  }

  // 2) items[] 补漏：部分 WebKit 场景下 kind=file 但 type 为空 / 非标准 MIME
  const items = clipboard.items;
  if (items && items.length > 0) {
    for (const item of Array.from(items)) {
      if (!item) continue;
      const type = (item.type || '').toLowerCase();
      if (type.startsWith('image/')) {
        const blob = item.getAsFile?.();
        if (blob) push(blob);
        continue;
      }
      if (item.kind === 'file' && typeof item.getAsFile === 'function') {
        const blob = item.getAsFile();
        if (!blob) continue;
        const blobType = (blob.type || '').toLowerCase();
        const blobName = (blob.name || '').toLowerCase();
        if (blobType.startsWith('image/') || IMAGE_EXT_RE.test(blobName)) push(blob);
      }
    }
  }
  return out;
}

function describeClipboardShape(clipboard) {
  const items = clipboard?.items;
  const files = clipboard?.files;
  const text = typeof clipboard?.getData === 'function' ? (clipboard.getData('text/plain') || '') : '';
  return {
    item_count: items?.length || 0,
    file_count: files?.length || 0,
    item_types: items ? Array.from(items).map((it) => `${it?.kind || '?'}:${it?.type || ''}`) : [],
    text_preview: text.slice(0, 120),
    has_image_ref_placeholder: IMAGE_REF_PLACEHOLDER_RE.test(text),
  };
}

async function saveOnePastedImage(blob, index) {
  const dataUrl = await blobToDataURL(blob);
  const base64 = dataUrl.split(',')[1] || '';
  const tempPath = await saveClipboardImage(base64);
  pushAttachment({
    kind: 'image',
    name: (blob?.name || `screenshot-${Date.now()}-${index}.png`).toString(),
    path: tempPath || '',
    previewUrl: dataUrl,
  });
  logInfo('composer', 'paste.image.added', { has_path: Boolean(tempPath), index });
}

async function handlePaste(event) {
  const blobs = extractClipboardImages(event);
  if (blobs.length === 0) {
    const shape = describeClipboardShape(event?.clipboardData);
    if (shape.has_image_ref_placeholder) {
      logWarn('composer', 'paste.ignored.imageRefPlaceholder', shape);
    } else {
      logDebug('composer', 'paste.ignored', shape);
    }
    return false;
  }

  event.preventDefault();
  let added = 0;
  for (let index = 0; index < blobs.length; index += 1) {
    try {
      await saveOnePastedImage(blobs[index], index);
      added += 1;
    } catch (error) {
      logWarn('composer', 'paste.image.failed', { error, index });
    }
  }
  return added > 0;
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
    activateDraft: draftController.activateDraft,
    clearDraft: draftController.clearDraft,
    restoreDraft: draftController.restoreDraft,
    resetComposerDrafts: draftController.resetDrafts,
    // Phase 2: forkDraft
    forkDraft,
    openForkDraft,
    closeForkDraft,
    addForkSharedFile,
    removeForkSharedFile,
  };
}
