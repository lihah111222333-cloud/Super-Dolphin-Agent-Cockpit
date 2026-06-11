export function createComposerFilePickerActions(runtime, deps) {
  const {
    actionNotice,
    appendUniqueAttachments,
    normalizeAttachment,
    selectFiles,
  } = deps;

  return {
    selectFilesForComposer: async () => {
      try {
        const picked = await selectFiles();
        const attachments = (Array.isArray(picked) ? picked : [])
          .map(normalizeAttachment)
          .filter(Boolean);
        runtime.set((state) => ({
          attachments: appendUniqueAttachments(state.attachments, attachments),
          actionNotice: actionNotice(attachments.length > 0 ? `已添加 ${attachments.length} 个附件` : '未选择附件', attachments.length > 0 ? 'success' : 'info'),
        }));
        return attachments;
      }
      catch (error) {
        runtime.notifyAction(`选择附件失败：${error.message || String(error)}`, 'error');
        runtime.addWarning('error', 'attachments.select.failed', { error: error.message || String(error) });
        return [];
      }
    },

    attachPathsForComposer: (paths) => {
      const attachments = (Array.isArray(paths) ? paths : [])
        .map(normalizeAttachment)
        .filter(Boolean);
      runtime.set((state) => ({
        attachments: appendUniqueAttachments(state.attachments, attachments),
        actionNotice: actionNotice(
          attachments.length > 0 ? `已添加 ${attachments.length} 个附件` : '未找到可添加的附件路径',
          attachments.length > 0 ? 'success' : 'info',
        ),
      }));
      return attachments.length;
    },


  };
}

export function createComposerDropActions(runtime, deps) {
  const {
    actionNotice,
    appendUniqueAttachments,
    attachmentKey,
    droppedFilePath,
    fileListOf,
    fileLooksImage,
    imageFileAttachment,
    normalizeFileAttachment,
    normalizeString,
  } = deps;

  return {
    attachDroppedFilesForComposer: async (files) => {
      const list = fileListOf(files);
      if (list.length === 0) return 0;
      const attachments = [];
      const rejected = [];
      for (let index = 0; index < list.length; index += 1) {
        const file = list[index];
        const path = droppedFilePath(file);
        if (path) {
          attachments.push(normalizeFileAttachment(path));
          continue;
        }
        if (fileLooksImage(file)) {
          attachments.push(await imageFileAttachment(file, index, 'dropped-image'));
          continue;
        }
        rejected.push(normalizeString(file?.name) || `file-${index + 1}`);
      }
      runtime.set((state) => ({
        attachments: appendUniqueAttachments(state.attachments, attachments),
        actionNotice: actionNotice(
          attachments.length > 0
            ? `已添加 ${attachments.length} 个附件`
            : '无法添加无路径的非图片文件',
          attachments.length > 0 ? 'success' : 'error',
        ),
      }));
      if (rejected.length > 0) {
        runtime.addWarning('warn', 'attachments.drop.rejected_no_path', { files: rejected });
      }
      return attachments.length;
    },

    attachPastedImagesForComposer: async (files) => {
      const images = fileListOf(files).filter(fileLooksImage);
      if (images.length === 0) return 0;
      const attachments = [];
      for (let index = 0; index < images.length; index += 1) {
        attachments.push(await imageFileAttachment(images[index], index, 'pasted-image'));
      }
      runtime.set((state) => ({
        attachments: appendUniqueAttachments(state.attachments, attachments),
        actionNotice: actionNotice(`已添加 ${attachments.length} 张图片`, 'success'),
      }));
      return attachments.length;
    },

    removeAttachment: (path) => {
      const target = normalizeString(path);
      runtime.set((state) => ({
        attachments: state.attachments.filter((item) => attachmentKey(item) !== target && item.path !== target),
      }));
    },


  };
}
