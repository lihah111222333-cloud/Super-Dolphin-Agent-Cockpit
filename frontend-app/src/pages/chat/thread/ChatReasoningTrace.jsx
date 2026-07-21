import { useEffect, useRef, useState } from 'react';
import { CheckCircle2, Copy } from 'lucide-react';
import { APP_COPY } from '../../../shared/i18n/appI18n.js';
import { copyTextToClipboard } from '../services/chatCodeService.js';
import { MessageContent } from '../markdown/MarkdownMessage.jsx';
import { currentTimestampMs, trimmedText } from '../markdown/markdownMessageModel.js';
import { runUIAction } from '../../../shared/ui/runUIAction.js';
import {
  durationLabelFromMs,
  parsePlanItems,
  reasoningKindMeta,
  reasoningStepDescription,
  reasoningTitle,
  timestampMs,
} from './chatReasoningModel.js';

function ExecutionPlan({ message }) {
  const items = parsePlanItems(message?.text);
  const completed = items.filter((item) => item.done).length;
  const summary = items.length > 0 ? `已完成 ${completed}/${items.length} 项任务` : '正在整理执行计划';
  return (
    <section className="execution-plan" aria-label="AI 执行计划">
      <header>
        <span>{reasoningTitle(message)}</span>
        <b>{summary}</b>
      </header>
      {items.length > 0 ? (
        <ol className="execution-plan-list">
          {items.map((item, index) => (
            <li key={`${item.text}-${index}`} data-plan-status={item.done ? 'done' : 'pending'}>
              <span className="execution-plan-check" aria-hidden="true">{item.done ? '✓' : ''}</span>
              <span>{item.text}</span>
            </li>
          ))}
        </ol>
      ) : (
        <MessageContent text={reasoningStepDescription(message)} />
      )}
    </section>
  );
}

function AssistantMessageActions({ copy = APP_COPY.zh.chat, text }) {
  const [copyState, setCopyState] = useState('idle');
  const resetTimerRef = useRef(null);
  useEffect(() => () => {
    if (resetTimerRef.current) window.clearTimeout(resetTimerRef.current);
  }, []);
  const copyableText = text === null || text === undefined ? '' : text.toString();
  const canCopy = copyableText.trim().length > 0;
  const scheduleReset = (delay) => {
    if (resetTimerRef.current) window.clearTimeout(resetTimerRef.current);
    resetTimerRef.current = window.setTimeout(() => {
      resetTimerRef.current = null;
      setCopyState('idle');
    }, delay);
  };
  const copyOutput = () => {
    if (!canCopy) return undefined;
    return runUIAction('message.output.copy', async () => { try {
      await copyTextToClipboard(copyableText);
      setCopyState('copied');
      scheduleReset(1800);
    }
    catch (error) {
      setCopyState('failed');
      scheduleReset(2200);
      throw error;
    } }, { retryable: true });
  };
  if (!canCopy) return null;
  const copied = copyState === 'copied';
  const failed = copyState === 'failed';
  let copyLabel = copy.copyOutput;
  if (copied) {
    copyLabel = copy.copiedOutput;
  } else if (failed) {
    copyLabel = copy.copyOutputFailed;
  }
  return (
    <div className="message-actions" aria-label={copy.assistantOutputActions}>
      <button
        type="button"
        className={`message-copy${copied ? ' is-copied' : ''}${failed ? ' is-failed' : ''}`}
        aria-label={copy.copyAssistantOutput}
        title={copy.copyAssistantOutput}
        onClick={() => { void copyOutput(); }}
      >
        {copied ? <CheckCircle2 size={14} aria-hidden="true" /> : <Copy size={14} aria-hidden="true" />}
        <span>{copyLabel}</span>
      </button>
    </div>
  );
}

function useElapsedLabel(startValue, endValue, active) {
  const [now, setNow] = useState(() => currentTimestampMs());

  useEffect(() => {
    if (!active) return undefined;
    const timer = window.setInterval(() => setNow(currentTimestampMs()), 1000);
    return () => window.clearInterval(timer);
  }, [active]);

  const start = timestampMs(startValue);
  if (!start) return '';
  const completed = timestampMs(endValue);
  if (!active && !completed) return '';
  const end = completed || now;
  if (end < start) return '';
  return durationLabelFromMs(end - start, { showZero: active });
}

function ReasoningTrace({ message, active = false }) {
  const done = !active && message?.done !== false;
  const hideElapsed = message?.hideElapsed === true;
  const hookElapsed = useElapsedLabel(message?.time, message?.completedAt, !done && !hideElapsed);
  const elapsed = hideElapsed
    ? ''
    : (done && typeof message?.elapsedMs === 'number' && message.elapsedMs > 0
      ? durationLabelFromMs(message.elapsedMs)
      : hookElapsed);
  const title = reasoningTitle(message);
  const elapsedSuffix = elapsed ? ` ${elapsed}` : '';
  const explicitStatusLabel = trimmedText(message?.statusLabel);
  const statusLabel = explicitStatusLabel ? explicitStatusLabel : (done
    ? `已处理 ${title}${elapsedSuffix}`
    : (trimmedText(message?.kind).toLowerCase() === 'thinking'
      ? `正在思考${elapsedSuffix}`
      : `正在运行 ${title}${elapsedSuffix}`));
  const meta = reasoningKindMeta(message);
  return (
    <article className={`reasoning-message${done ? '' : ' is-active'} no-avatar`} aria-label="AI 思考记录">
      <details className="reasoning-trace">
        <summary>
          <span className="reasoning-trace-status">
            {statusLabel}
          </span>
        </summary>
        <div className="reasoning-step-list">
          <section className={`reasoning-step reasoning-step--${meta.tone}`} aria-label={`${meta.label}步骤`}>
            <div className="reasoning-step-body">
              {meta.tone === 'plan' ? <ExecutionPlan message={message} /> : <MessageContent text={reasoningStepDescription(message)} />}
            </div>
          </section>
        </div>
      </details>
    </article>
  );
}

export { AssistantMessageActions, ExecutionPlan, ReasoningTrace };
