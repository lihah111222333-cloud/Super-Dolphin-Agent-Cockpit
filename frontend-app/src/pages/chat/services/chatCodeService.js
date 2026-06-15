import {
  copyTextToClipboard as copyTextToClipboardBackend,
  locateCodeFile as locateCodeFileBackend,
  onFilesDropped as onFilesDroppedBackend,
  openCodeFile as openCodeFileBackend,
  saveCodeFile as saveCodeFileBackend,
} from '../../../shared/api/backendApi.js';

/*
 * chat code service 只是把代码预览动作转给 backendApi。
 * cwd/project scope 由后端校验，UI 只传 runtimeCodeScopePayload。
 */

export function copyTextToClipboard(text) {
  return copyTextToClipboardBackend(text);
}

export function locateCodeFile(payload) {
  return locateCodeFileBackend(payload);
}

export function onFilesDropped(callback) {
  return onFilesDroppedBackend(callback);
}

export function openCodeFile(payload) {
  return openCodeFileBackend(payload);
}

export function saveCodeFile(payload) {
  return saveCodeFileBackend(payload);
}
