import React, { useMemo, useState } from 'react';
import { finalOutputKind, finalOutputPath } from '../adapters/workflowDisplayAdapter.js';
import { Panel } from '../../shared/pageComponents.jsx';

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
      const parsed = JSON.parse(trimmed);
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
      const parsed = JSON.parse(trimmed);
      return { formatted: JSON.stringify(parsed, null, 2), isJson: true };
    } catch {
      // Keep the original text when it is not valid JSON.
    }
  }
  return { formatted: text, isJson: false };
}

function WorkflowInlinePreviewText({ text }) {
  const fallback = '当前运行尚未标记最终结果。';
  if (!text) return <span className="workflow-inline-preview-empty">{fallback}</span>;
  const { formatted, isJson } = formatInlinePreviewText(text);
  if (isJson) return <pre className="workflow-final-preview">{formatted}</pre>;
  return <p className="workflow-inline-preview-text">{text}</p>;
}

function WorkflowFinalOutputPanel({ finalOutput, previewText, readFile, openFile }) {
  const [fileContent, setFileContent] = useState('');
  const [fileError, setFileError] = useState('');
  const [openError, setOpenError] = useState('');
  const [opening, setOpening] = useState(false);
  const [reading, setReading] = useState(false);
  const outputPath = finalOutputPath(finalOutput);
  const isImage = useMemo(() => /\.(png|jpe?g|webp|gif|svg)$/i.test(outputPath || ''), [outputPath]);
  const isVideo = useMemo(() => /\.(mp4|webm|ogg|mov)$/i.test(outputPath || ''), [outputPath]);
  const isMedia = isImage || isVideo;
  const isSystemOpenOnly = useMemo(() => isMedia || /\.(pdf|docx?|pptx?|xlsx?)$/i.test(outputPath || ''), [isMedia, outputPath]);
  const mediaKindLabel = isVideo ? '视频' : '图片';
  const formattedContent = useMemo(() => formatWorkflowFileContent(fileContent), [fileContent]);

  const readFinalOutput = async () => {
    if (!outputPath) return;
    setOpenError('');
    if (fileContent) {
      setFileContent('');
      return;
    }
    setReading(true);
    setFileError('');
    try {
      const response = await readFile({ path: outputPath });
      setFileContent((response?.content || '').toString());
    } catch {
      setFileError('无法读取最终结果文件，请稍后重试。');
    } finally {
      setReading(false);
    }
  };
  const openFinalOutput = async () => {
    if (!outputPath) return;
    setOpening(true);
    setOpenError('');
    try {
      await openFile({ path: outputPath });
    } catch (err) {
      setOpenError(`打开最终结果文件失败：${err?.message || String(err)}`);
    } finally {
      setOpening(false);
    }
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
          mediaKindLabel={mediaKindLabel}
          onOpen={openFinalOutput}
          onRead={readFinalOutput}
          openError={openError}
          opening={opening}
          outputPath={outputPath}
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
            disabled={props.isSystemOpenOnly ? props.opening : props.reading}
            onClick={() => { void (props.isSystemOpenOnly ? props.onOpen() : props.onRead()); }}
            title={workflowPrimaryActionTitle(props)}
          >
            {workflowPrimaryActionLabel(props)}
          </button>
          {props.isMedia && !props.isSystemOpenOnly ? (
            <button type="button" className="workflow-output-action workflow-output-action-system" disabled={props.opening} onClick={() => { void props.onOpen(); }} title={`用系统默认应用打开${props.mediaKindLabel}`}>
              {props.opening ? '打开中...' : '系统打开'}
            </button>
          ) : null}
        </div>
      </div>
      {props.fileError ? <p className="danger-text">{props.fileError}</p> : null}
      {props.openError ? <p className="danger-text">{props.openError}</p> : null}
      {previewBlock}
    </div>
  );
}

function workflowPrimaryActionTitle(props) {
  if (props.isSystemOpenOnly) return '用系统默认应用打开最终结果文件';
  if (props.isMedia) return `用系统默认应用打开${props.mediaKindLabel}`;
  return '读取最终结果内容';
}

function workflowPrimaryActionLabel(props) {
  if (props.isSystemOpenOnly) return props.opening ? '打开中...' : '系统打开';
  return workflowPreviewButtonLabel(props);
}

function workflowPreviewButtonLabel({ fileContent, isImage, isMedia, isVideo, reading }) {
  if (reading) return '读取中...';
  if (fileContent) return isMedia ? '系统打开' : '收起最终结果';
  if (isVideo) return '页内播放';
  if (isImage) return '页内预览';
  return '读取最终结果';
}

function workflowPreviewBlock({ fileContent, formattedContent }) {
  if (!fileContent) return null;
  return <pre className="workflow-final-preview">{formattedContent}</pre>;
}

export { WorkflowFinalOutputPanel };
