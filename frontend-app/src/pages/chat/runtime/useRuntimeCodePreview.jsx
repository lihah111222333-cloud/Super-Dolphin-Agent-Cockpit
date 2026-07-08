import { useEffect, useMemo, useRef, useState } from 'react';
import { codeOpenDisplayPath, codePreviewStateAfterSave, codePreviewStateFromOpenResult, emptyCodePreviewState } from '../adapters/codePreviewAdapter.js';
import { codeActionError, emptyPathChoiceState, normalizeCodeLocateOptions, runtimeCodeScopeKey, runtimeCodeScopePayload } from '../adapters/runtimeCodeAdapter.js';
import { firstText } from '../markdown/markdownMessageModel.js';
import { RuntimeCodePreviewDialogs } from './RuntimeCodePreviewDialogs.jsx';

function runtimePathChoice(file, options, result) {
  return { open: true, file, options, truncated: Boolean(result?.truncated) };
}

async function saveRuntimePreviewChanges({
  codeFileActions,
  codePreview,
  isCurrentPreviewRequest,
  previewRequestSeqRef,
  previewScopeKey,
  projectPath,
  projects,
  setCodePreview,
}) {
  if (!codePreview.filePath || codePreview.saving) return;
  if (codePreview.scopeKey && codePreview.scopeKey !== previewScopeKey) {
    setCodePreview((current) => ({ ...current, error: '项目已切换，请重新打开文件预览', status: '' }));
    return;
  }
  if (!codePreview.editable || codePreview.image || codePreview.loading) {
    setCodePreview((current) => ({ ...current, error: '当前预览不是完整文件，不能保存片段内容', status: '' }));
    return;
  }
  const requestSeq = previewRequestSeqRef.current;
  const requestScopeKey = previewScopeKey;
  const savedDraft = codePreview.draft;
  setCodePreview((current) => ({ ...current, saving: true, error: '', status: '' }));
  try {
    const result = await codeFileActions.saveCodeFile({
      ...runtimeCodeScopePayload(codePreview.filePath, projectPath, projects),
      content: savedDraft,
      previewMode: codePreview.previewMode,
      contentVersion: codePreview.contentVersion,
    });
    if (!isCurrentPreviewRequest(requestSeq, requestScopeKey)) return;
    const relative = codeOpenDisplayPath(result, codePreview.relative || codePreview.filePath);
    setCodePreview((current) => codePreviewStateAfterSave(current, result, relative, savedDraft));
  } catch (error) {
    if (!isCurrentPreviewRequest(requestSeq, requestScopeKey)) return;
    setCodePreview((current) => ({ ...current, saving: false, error: codeActionError(error, '保存失败') }));
  }
}

function useRuntimeCodePreview({
  codeFileActions,
  projectPath,
  projects,
  renderMarkdownPreview,
}) {
  const [collapsedDiffFiles, setCollapsedDiffFiles] = useState(() => new Set());
  const [diffActionNotice, setDiffActionNotice] = useState('');
  const [codePreview, setCodePreview] = useState(emptyCodePreviewState);
  const [pathChoice, setPathChoice] = useState(emptyPathChoiceState);
  const previewRequestSeqRef = useRef(0);
  const previewScopeKey = useMemo(() => runtimeCodeScopeKey(projectPath, projects), [projectPath, projects]);
  const previewScopeKeyRef = useRef(previewScopeKey);
  previewScopeKeyRef.current = previewScopeKey;

  const nextPreviewRequestSeq = () => {
    previewRequestSeqRef.current += 1;
    return previewRequestSeqRef.current;
  };

  const isCurrentPreviewRequest = (requestSeq, requestScopeKey) => (
    previewRequestSeqRef.current === requestSeq && previewScopeKeyRef.current === requestScopeKey
  );

  useEffect(() => {
    previewRequestSeqRef.current += 1;
    setCodePreview(emptyCodePreviewState());
    setPathChoice(emptyPathChoiceState());
  }, [previewScopeKey]);

  const toggleDiffFile = (filename) => {
    setCollapsedDiffFiles((current) => {
      const next = new Set(current);
      if (next.has(filename)) next.delete(filename);
      else next.add(filename);
      return next;
    });
  };

  const locateDiffFile = async (file) => {
    setDiffActionNotice(`正在定位 ${file.filename}`);
    try {
      const result = await codeFileActions.locateCodeFile(runtimeCodeScopePayload(file.filename, projectPath, projects));
      const options = normalizeCodeLocateOptions(result);
      if (options.length > 1) {
        setPathChoice(runtimePathChoice(file, options, result));
      }
      setDiffActionNotice(`定位到 ${options.length} 个路径`);
    } catch (error) {
      setDiffActionNotice(codeActionError(error, '定位失败'));
    }
  };

  const openCodePreviewForPath = async (filePath, fallbackRelative = '') => {
    const requestSeq = nextPreviewRequestSeq();
    const requestScopeKey = previewScopeKey;
    const displayPath = firstText(fallbackRelative, filePath);
    setCodePreview({
      ...emptyCodePreviewState(),
      open: true,
      loading: true,
      filePath,
      relative: displayPath,
      scopeKey: requestScopeKey,
    });
    try {
      const result = await codeFileActions.openCodeFile(runtimeCodeScopePayload(filePath, projectPath, projects));
      if (!isCurrentPreviewRequest(requestSeq, requestScopeKey)) return;
      setCodePreview({
        ...codePreviewStateFromOpenResult(result, filePath, displayPath),
        scopeKey: requestScopeKey,
      });
    } catch (error) {
      if (!isCurrentPreviewRequest(requestSeq, requestScopeKey)) return;
      setCodePreview((current) => ({ ...current, loading: false, error: codeActionError(error, '打开失败') }));
    }
  };

  const openChosenPath = async (filePath) => {
    const fallback = firstText(pathChoice.file?.filename, filePath);
    setPathChoice(emptyPathChoiceState());
    await openCodePreviewForPath(filePath, fallback);
  };

  const savePreviewChanges = async () => {
    await saveRuntimePreviewChanges({
      codeFileActions,
      codePreview,
      isCurrentPreviewRequest,
      previewRequestSeqRef,
      previewScopeKey,
      projectPath,
      projects,
      setCodePreview,
    });
  };

  const closeCodePreview = () => {
    nextPreviewRequestSeq();
    setCodePreview(emptyCodePreviewState());
  };

  const onChangeDraft = (draft) => setCodePreview((current) => ({ ...current, draft, error: '' }));
  const onDirtyClose = () => setCodePreview((current) => ({ ...current, error: '请先保存或放弃预览更改' }));
  const dialogs = (
    <RuntimeCodePreviewDialogs
      codePreview={codePreview}
      closeCodePreview={closeCodePreview}
      onChangeDraft={onChangeDraft}
      onDirtyClose={onDirtyClose}
      openChosenPath={openChosenPath}
      pathChoice={pathChoice}
      renderMarkdownPreview={renderMarkdownPreview}
      savePreviewChanges={savePreviewChanges}
      setCodePreview={setCodePreview}
      setPathChoice={setPathChoice}
    />
  );

  return { collapsedDiffFiles, dialogs, diffActionNotice, locateDiffFile, openCodePreviewForPath, toggleDiffFile };
}

export { useRuntimeCodePreview };
