import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import { codeOpenDisplayPath, codePreviewStateAfterSave, codePreviewStateFromOpenResult, emptyCodePreviewState } from '../adapters/codePreviewAdapter.js';
import { emptyPathChoiceState, fileRefPosition, normalizeCodeLocateOptions, runtimeCodeScopeKey, runtimeCodeScopePayload } from '../adapters/runtimeCodeAdapter.js';
import { firstText, firstTrimmedText } from '../markdown/markdownMessageModel.js';
import { locateCodeFile, openCodeFile, openPath, saveCodeFile } from '../services/chatCodeService.js';
import { CodePreviewControllerDialogs } from './CodePreviewControllerDialogs.jsx';
import { runUIAction } from '../../../shared/ui/runUIAction.js';

function codePreviewPathChoice(filePath, position, options, locateResult) {
  return { open: true, file: { filename: filePath, position }, options, truncated: Boolean(locateResult?.truncated) };
}

export function saveCodePreviewChanges({
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
  return runUIAction('file.code-preview.save', async () => { try {
    const result = await saveCodeFile({
      ...runtimeCodeScopePayload(codePreview.filePath, projectPath, projects),
      content: savedDraft,
      previewMode: codePreview.previewMode,
      contentVersion: codePreview.contentVersion,
    });
    if (!isCurrentPreviewRequest(requestSeq, requestScopeKey)) return;
    const relative = codeOpenDisplayPath(result, codePreview.relative || codePreview.filePath);
    setCodePreview((current) => codePreviewStateAfterSave(current, result, relative, savedDraft));
  } catch (error) {
    if (isCurrentPreviewRequest(requestSeq, requestScopeKey)) setCodePreview((current) => ({ ...current, saving: false, error: '保存失败，请重试。' }));
    throw error;
  } });
}

function useCodePreviewController({ projectPath, projects }) {
  const [codePreview, setCodePreview] = useState(emptyCodePreviewState);
  const [pathChoice, setPathChoice] = useState(emptyPathChoiceState);
  const previewRequestSeqRef = useRef(0);
  const previewScopeKey = useMemo(() => runtimeCodeScopeKey(projectPath, projects), [projectPath, projects]);
  const previewScopeKeyRef = useRef(previewScopeKey);

  const nextPreviewRequestSeq = useCallback(() => {
    previewRequestSeqRef.current += 1;
    return previewRequestSeqRef.current;
  }, []);

  const isCurrentPreviewRequest = useCallback((requestSeq, requestScopeKey) => (
    previewRequestSeqRef.current === requestSeq && previewScopeKeyRef.current === requestScopeKey
  ), []);

  useEffect(() => {
    previewScopeKeyRef.current = previewScopeKey;
    previewRequestSeqRef.current += 1;
    setCodePreview(emptyCodePreviewState());
    setPathChoice(emptyPathChoiceState());
  }, [previewScopeKey]);

  const openCodePreviewForPath = useCallback(async (filePath, fallbackRelative = '', position = null) => {
    const requestSeq = nextPreviewRequestSeq();
    const requestScopeKey = previewScopeKey;
    const displayPath = firstText(fallbackRelative, filePath);
    setCodePreview({ ...emptyCodePreviewState(), open: true, loading: true, filePath, relative: displayPath, scopeKey: requestScopeKey });
    try {
      const result = await openCodeFile(runtimeCodeScopePayload(filePath, projectPath, projects, position));
      if (!isCurrentPreviewRequest(requestSeq, requestScopeKey)) return;
      setCodePreview({
        ...codePreviewStateFromOpenResult(result, filePath, displayPath),
        scopeKey: requestScopeKey,
      });
    } catch (error) {
      if (isCurrentPreviewRequest(requestSeq, requestScopeKey)) setCodePreview((current) => ({ ...current, loading: false, error: '打开失败，请重试。' }));
      throw error;
    }
  }, [isCurrentPreviewRequest, nextPreviewRequestSeq, previewScopeKey, projectPath, projects]);

  const openFileRef = useCallback((payload = {}) => runUIAction('file.code-preview.locate', async () => {
    const filePath = firstTrimmedText(payload.path, payload.filePath);
    if (!filePath) return;
    const requestSeq = nextPreviewRequestSeq();
    const requestScopeKey = previewScopeKey;
    const position = fileRefPosition(payload);
    setCodePreview({ ...emptyCodePreviewState(), open: true, loading: true, filePath, relative: filePath, scopeKey: requestScopeKey });
    try {
      const locateResult = await locateCodeFile(runtimeCodeScopePayload(filePath, projectPath, projects, position));
      if (!isCurrentPreviewRequest(requestSeq, requestScopeKey)) return;
      const options = normalizeCodeLocateOptions(locateResult);
      if (options.length > 1) {
        setCodePreview(emptyCodePreviewState());
        setPathChoice(codePreviewPathChoice(filePath, position, options, locateResult));
        return;
      }
      await openCodePreviewForPath(options[0] || filePath, filePath, position);
    } catch (error) {
      if (isCurrentPreviewRequest(requestSeq, requestScopeKey)) setCodePreview((current) => ({ ...current, loading: false, error: '定位失败，请重试。' }));
      throw error;
    }
  }), [isCurrentPreviewRequest, nextPreviewRequestSeq, openCodePreviewForPath, previewScopeKey, projectPath, projects]);

  const openLocalPath = useCallback((payload = {}) => runUIAction('file.local.open', async () => {
    const filePath = firstTrimmedText(payload.path, payload.filePath);
    if (!filePath) return;
    const requestSeq = nextPreviewRequestSeq();
    const requestScopeKey = previewScopeKey;
    const position = fileRefPosition(payload);
    try {
      await openPath(runtimeCodeScopePayload(filePath, projectPath, projects, position));
    } catch (error) {
      if (isCurrentPreviewRequest(requestSeq, requestScopeKey)) setCodePreview({
        ...emptyCodePreviewState(),
        open: true,
        loading: false,
        filePath,
        relative: filePath,
        error: '打开失败，请重试。',
        scopeKey: requestScopeKey,
      });
      throw error;
    }
  }), [isCurrentPreviewRequest, nextPreviewRequestSeq, previewScopeKey, projectPath, projects]);

  const openChosenPath = useCallback((filePath) => {
    const fallback = firstText(pathChoice.file?.filename, filePath);
    const position = pathChoice.file?.position ?? null;
    setPathChoice(emptyPathChoiceState());
    void runUIAction('file.code-preview.open', () => openCodePreviewForPath(filePath, fallback, position));
  }, [openCodePreviewForPath, pathChoice.file]);

  const savePreviewChanges = useCallback(() => {
    void saveCodePreviewChanges({
      codePreview,
      isCurrentPreviewRequest,
      previewRequestSeqRef,
      previewScopeKey,
      projectPath,
      projects,
      setCodePreview,
    });
  }, [codePreview, isCurrentPreviewRequest, previewScopeKey, projectPath, projects]);

  const closeCodePreview = useCallback(() => {
    nextPreviewRequestSeq();
    setCodePreview(emptyCodePreviewState());
  }, [nextPreviewRequestSeq]);

  const onChangeDraft = (draft) => setCodePreview((current) => ({ ...current, draft, error: '' }));
  const onDirtyClose = () => setCodePreview((current) => ({ ...current, error: '请先保存或放弃预览更改' }));
  const dialogs = (
    <CodePreviewControllerDialogs
      codePreview={codePreview}
      closeCodePreview={closeCodePreview}
      onChangeDraft={onChangeDraft}
      onDirtyClose={onDirtyClose}
      openChosenPath={openChosenPath}
      pathChoice={pathChoice}
      savePreviewChanges={savePreviewChanges}
      setCodePreview={setCodePreview}
      setPathChoice={setPathChoice}
    />
  );

  return { dialogs, openFileRef, openLocalPath };
}


export { useCodePreviewController };
