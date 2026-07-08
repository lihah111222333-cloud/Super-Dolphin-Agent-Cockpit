import { firstText } from './markdownMessageModel.js';

function composerAttachmentKey(item) {
  return firstText(item?.path, item?.previewUrl, item?.url).trim();
}

export { composerAttachmentKey };
