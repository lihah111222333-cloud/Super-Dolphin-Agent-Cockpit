import React, { useEffect, useMemo, useState } from 'react';
import { finalOutputKind, finalOutputPath } from '../adapters/workflowDisplayAdapter.js';
import { Panel } from '../../shared/pageComponents.jsx';
import { parseStrictJsonValue } from '../../shared/pageShared.js';
import { previewUrlFromResponse } from './workflowPreviewUrl.js';
import { runBackgroundAction, runUIAction } from '../../../shared/ui/runUIAction.js';

function normalizedText(value) {
  return value == null ? '' : String(value);
}

function parseJsonForDisplay(value, label) {
  try {
    return parseStrictJsonValue(value, label);
  } catch (error) {
    throw new Error(`${label} JSON parse failed: ${error?.message || String(error)}`, { cause: error });
  }
}

function formatWorkflowFileContent(content) {
  if (!content) return '';
  let trimmed = content.trim();
  const fenceMatch = trimmed.match(/^`{3,}([a-zA-Z0-9_-]*)\n([\s\S]*?)\n`{3,}$/);
  let isJson = false;
  if (fenceMatch) {
    trimmed = fenceMatch[2].trim();
    if (fenceMatch[1] && fenceMatch[1].toLowerCase() === 'json') isJson = true;
  }
  if (isJson || trimmed.startsWith('{') || trimmed.startsWith('[')) {
    try {
      const parsed = parseJsonForDisplay(trimmed, 'workflow final output file content');
      return JSON.stringify(parsed, null, 2);
    } catch {
      // Keep the raw content when a result only looks like JSON.
    }
  }
  return fenceMatch ? fenceMatch[2] : content;
}

function formatInlinePreviewText(text) {
  if (!text) return { formatted: '', isJson: false };
  const trimmed = text.trim();
  if (trimmed.startsWith('{') || trimmed.startsWith('[')) {
    try {
      const parsed = parseJsonForDisplay(trimmed, 'workflow final output preview text');
      return { formatted: JSON.stringify(parsed, null, 2), isJson: true };
    } catch {
      // Keep the original text when it is not valid JSON.
    }
  }
  return { formatted: text, isJson: false };
}

async function loadWorkflowMediaPreview(outputPath, previewFile) {
  if (typeof previewFile !== 'function') {
    throw new Error('后端预览接口不可用。');
  }
  return previewUrlFromResponse(await previewFile({ path: outputPath }));
}

async function readWorkflowOutput(readFile, outputPath) {
  return readFile({ path: outputPath });
}

async function openWorkflowOutput(openFile, outputPath) {
  return openFile({ path: outputPath });
}

function WorkflowInlinePreviewText({ text }) {
  const fallback = '当前运行尚未标记最终结果。';
  if (!text) return <span className="workflow-inline-preview-empty">{fallback}</span>;
  const { formatted, isJson } = formatInlinePreviewText(text);
  if (isJson) return <pre className="workflow-final-preview">{formatted}</pre>;
  return <p className="workflow-inline-preview-text">{text}</p>;
}

function WorkflowFinalOutputPanel({ finalOutput, previewText, readFile, openFile, previewFile }) {
  const [fileContent, setFileContent] = useState('');
  const [fileError, setFileError] = useState('');
  const [openError, setOpenError] = useState('');
  const [previewError, setPreviewError] = useState('');
  const [mediaPreview, setMediaPreview] = useState(null);
  const [opening, setOpening] = useState(false);
  const [previewing, setPreviewing] = useState(false);
  const [reading, setReading] = useState(false);
  const outputPath = finalOutputPath(finalOutput);
  const isImage = useMemo(() => /\.(png|jpe?g|webp|gif)$/i.test(outputPath), [outputPath]);
  const isVideo = useMemo(() => /\.(mp4|webm|ogg|mov)$/i.test(outputPath), [outputPath]);
  const isMedia = isImage || isVideo;
  const isSystemOpenOnly = useMemo(() => !isMedia && /\.(pdf|docx?|pptx?|xlsx?)$/i.test(outputPath), [isMedia, outputPath]);
  const mediaKindLabel = isVideo ? '视频' : '图片';
  const formattedContent = useMemo(() => formatWorkflowFileContent(fileContent), [fileContent]);

  const loadMediaPreview = () => {
    if (!outputPath || !isMedia) return undefined;
    return runUIAction('workflow.output.preview', async () => { setPreviewing(true);
    setPreviewError('');
    try {
      setMediaPreview(await loadWorkflowMediaPreview(outputPath, previewFile));
    } catch (err) {
      setMediaPreview(null);
      setPreviewError('无法生成最终结果预览，请重试。');
      throw err;
    } finally {
      setPreviewing(false);
    } });
  };

  useEffect(() => {
    let active = true;
    const load = async () => {
      setMediaPreview(null);
      setPreviewError('');
      if (!outputPath || !isMedia) return;
      setPreviewing(true);
      try {
        const preview = await loadWorkflowMediaPreview(outputPath, previewFile);
        if (!active) return;
        setMediaPreview(preview);
      } catch (err) {
        if (active) setPreviewError('无法生成最终结果预览，请查看 Health。');
        throw err;
      } finally {
        if (active) setPreviewing(false);
      }
    };
    runBackgroundAction('workflow.output.preview.load', load);
    return () => {
      active = false;
    };
  }, [isMedia, outputPath, previewFile]);

  const readFinalOutput = () => {
    if (!outputPath) return undefined;
    setOpenError('');
    if (fileContent) {
      setFileContent('');
      return undefined;
    }
    return runUIAction('workflow.output.read', async () => { setReading(true);
    setFileError('');
    try {
      const response = await readWorkflowOutput(readFile, outputPath);
      setFileContent(normalizedText(response?.content));
    } catch (error) {
      setFileError('无法读取最终结果文件，请稍后重试。');
      throw error;
    } finally {
      setReading(false);
    } });
  };
  const openFinalOutput = () => {
    if (!outputPath) return undefined;
    return runUIAction('workflow.output.open', async () => { setOpening(true);
    setOpenError('');
    try {
      await openWorkflowOutput(openFile, outputPath);
    } catch (err) {
      setOpenError('打开最终结果文件失败，请重试。');
      throw err;
    } finally {
      setOpening(false);
    } });
  };

  return (
    <Panel title="最终结果">
      {outputPath ? (
        <WorkflowFinalOutputFile
          fileContent={fileContent}
          fileError={fileError}
          finalOutput={finalOutput}
          formattedContent={formattedContent}
          isImage={isImage}
          isMedia={isMedia}
          isSystemOpenOnly={isSystemOpenOnly}
          isVideo={isVideo}
          mediaPreview={mediaPreview}
          mediaKindLabel={mediaKindLabel}
          onOpen={openFinalOutput}
          onPreview={loadMediaPreview}
          onRead={readFinalOutput}
          openError={openError}
          opening={opening}
          outputPath={outputPath}
          previewError={previewError}
          previewing={previewing}
          reading={reading}
        />
      ) : (
        <WorkflowInlinePreviewText text={previewText} />
      )}
    </Panel>
  );
}

function WorkflowFinalOutputFile(props) {
  const previewBlock = workflowPreviewBlock(props);
  return (
    <div className="workflow-final-output">
      <div className="workflow-file-row">
        <span>{finalOutputKind(props.finalOutput) || '文件'}</span>
        <code>{props.outputPath}</code>
        <div className="workflow-output-actions" aria-label="最终结果操作">
          <button
            type="button"
            className={'workflow-output-action ' + (props.isSystemOpenOnly ? 'workflow-output-action-system' : 'workflow-output-action-preview')}
            disabled={workflowPrimaryActionDisabled(props)}
            onClick={() => { void workflowRunPrimaryAction(props); }}
            title={workflowPrimaryActionTitle(props)}
          >
            {workflowPrimaryActionLabel(props)}
          </button>
          {props.isMedia ? (
            <button type="button" className="workflow-output-action workflow-output-action-system" disabled={props.opening} onClick={() => { void props.onOpen(); }} title={`用系统默认应用打开${props.mediaKindLabel}`}>
              {props.opening ? '打开中...' : '系统打开'}
            </button>
          ) : null}
        </div>
      </div>
      {props.fileError ? <p className="danger-text">{props.fileError}</p> : null}
      {props.openError ? <p className="danger-text">{props.openError}</p> : null}
      {props.previewError ? <p className="danger-text">{props.previewError}</p> : null}
      {previewBlock}
    </div>
  );
}

function workflowPrimaryActionDisabled(props) {
  if (props.isSystemOpenOnly) return props.opening;
  if (props.isMedia) return props.previewing;
  return props.reading;
}

function workflowRunPrimaryAction(props) {
  if (props.isSystemOpenOnly) return props.onOpen();
  if (props.isMedia) return props.onPreview();
  return props.onRead();
}

function workflowPrimaryActionTitle(props) {
  if (props.isSystemOpenOnly) return '用系统默认应用打开最终结果文件';
  if (props.isMedia) return `刷新${props.mediaKindLabel}预览`;
  return '读取最终结果内容';
}

function workflowPrimaryActionLabel(props) {
  if (props.isSystemOpenOnly) return props.opening ? '打开中...' : '系统打开';
  if (props.isMedia) return props.previewing ? '生成中...' : '刷新预览';
  return workflowPreviewButtonLabel(props);
}

function workflowPreviewButtonLabel({ fileContent, isImage, isMedia, isVideo, reading }) {
  if (reading) return '读取中...';
  if (fileContent) return isMedia ? '系统打开' : '收起最终结果';
  if (isVideo) return '页内播放';
  if (isImage) return '页内预览';
  return '读取最终结果';
}

function workflowPreviewBlock(props) {
  const { fileContent, formattedContent, isImage, isMedia, isVideo, mediaPreview, previewing } = props;
  if (isMedia) {
    if (mediaPreview?.url && isImage) {
      return <img className="workflow-final-media" src={mediaPreview.url} alt="" />;
    }
    if (mediaPreview?.url && isVideo) {
      return <video className="workflow-final-media" src={mediaPreview.url} controls />;
    }
    return previewing ? <p className="workflow-inline-preview-text">正在生成预览...</p> : null;
  }
  if (!fileContent) return null;
  return <pre className="workflow-final-preview">{formattedContent}</pre>;
}

export { WorkflowFinalOutputPanel };
