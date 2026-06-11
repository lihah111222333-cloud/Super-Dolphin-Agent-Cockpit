function codePreviewFormatBytes(value) {
  const size = Number(value);
  if (!Number.isFinite(size) || size <= 0) return '';
  if (size >= 1024 * 1024) return `${(size / (1024 * 1024)).toFixed(2)} MB`;
  if (size >= 1024) return `${(size / 1024).toFixed(1)} KB`;
  return `${Math.floor(size)} B`;
}

function codePreviewMeta(preview) {
  const parts = [];
  if (preview?.image) {
    if (preview.mediaType) parts.push(preview.mediaType);
    const size = codePreviewFormatBytes(preview.sizeBytes);
    if (size) parts.push(size);
    return parts.join(' · ');
  }
  const startLine = Number(preview?.startLine);
  const endLine = Number(preview?.endLine);
  const totalLines = Number(preview?.totalLines);
  if (Number.isFinite(startLine) && startLine > 0 && Number.isFinite(endLine) && endLine >= startLine) {
    parts.push(startLine === endLine ? `第 ${startLine} 行` : `第 ${startLine}-${endLine} 行`);
  }
  if (Number.isFinite(totalLines) && totalLines > 0) parts.push(`共 ${totalLines} 行`);
  return parts.join(' · ');
}

export { codePreviewMeta };
