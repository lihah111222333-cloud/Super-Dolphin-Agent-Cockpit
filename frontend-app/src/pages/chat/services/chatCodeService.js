import {
  copyTextToClipboard as copyTextToClipboardBackend,
  locateCodeFile as locateCodeFileBackend,
  onFilesDropped as onFilesDroppedBackend,
  openCodeFile as openCodeFileBackend,
  saveCodeFile as saveCodeFileBackend,
} from '../../../shared/api/backendApi.js';

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
