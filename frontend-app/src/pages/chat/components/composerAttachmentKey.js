function composerAttachmentKey(item) {
  return (item?.path || item?.previewUrl || item?.url || '').toString().trim();
}

export { composerAttachmentKey };
