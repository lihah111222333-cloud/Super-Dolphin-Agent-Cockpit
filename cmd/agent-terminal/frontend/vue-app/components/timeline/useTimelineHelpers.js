import { hasJsonRenderSpec, extractSpecBlocks } from '../../services/json-render-engine.js';
import { logInfo, logWarn } from '../../services/log.js';
import { summarizeToolActivity, toolActivityDetail } from '../../utils/format-utils.js';

function displayFilePath(path) {
  const raw = (path || '').toString().trim();
  if (!raw) return '';
  return raw
    .replace(/^\/Users\/[^/]+\//, '~/')
    .replace(/^\/home\/[^/]+\//, '~/')
    .replace(/^C:\\Users\\[^\\]+\\/i, '~\\');
}

function compactPopoverText(value, maxLen = 140) {
  const text = (value || '').toString().replace(/\s+/g, ' ').trim();
  if (!text) return '';
  if (text.length <= maxLen) return text;
  return `${text.slice(0, Math.max(0, maxLen - 1)).trimEnd()}…`;
}

function toolSummaryKindLabel(item) {
  if (item?.kind === 'tool') return '工具';
  if (item?.kind === 'command') return '命令';
  if (item?.kind === 'file') return '文件';
  return '过程';
}

function formatTime(ts) {
  if (!ts) return '';
  const date = new Date(ts);
  if (Number.isNaN(date.getTime())) return '';
  return date.toLocaleTimeString([], {
    hour: '2-digit',
    minute: '2-digit',
  });
}

async function copyTextToClipboard(text) {
  const value = (text || '').toString();
  if (!value) return false;
  try {
    if (navigator?.clipboard?.writeText) {
      await navigator.clipboard.writeText(value);
      return true;
    }
  } catch (error) {
    logWarn('ui', 'timeline.copy.clipboard_api_failed', { error: String(error || '') });
  }
  logWarn('ui', 'timeline.copy.clipboard_unavailable', {
    has_clipboard_api: Boolean(navigator?.clipboard?.writeText),
    secure_context: typeof window !== 'undefined' ? Boolean(window.isSecureContext) : false,
  });
  return false;
}

function itemHasSpec(text) {
  return hasJsonRenderSpec(text);
}

function splitBySpec(text) {
  return extractSpecBlocks(text);
}

function bubbleRole(item) {
  if (item?.kind === 'user' && item?.internal) return 'role-internal';
  if (item?.kind === 'user') return 'role-user';
  if (item?.kind === 'assistant') return 'role-assistant';
  return 'role-system';
}

function isDialog(item) {
  return item?.kind === 'user' || item?.kind === 'assistant';
}

function hasAvatar(item, index, items) {
  if (!isDialog(item)) return false;
  if (item?.kind === 'assistant' && Array.isArray(items) && typeof index === 'number') {
    for (let i = index - 1; i >= 0; i--) {
      const prev = items[i];
      if (prev?.kind === 'user') return true;
      if (prev?.kind === 'assistant') return false;
    }
  }
  return true;
}

function avatarText(item) {
  if (item?.kind === 'user' && item?.internal) return '↔';
  if (item?.kind === 'user') return 'U';
  if (item?.kind === 'assistant') return 'AI';
  return '';
}

function createStateLabel(approvalRequestId, approvalResolvedByRequestId) {
  return function stateLabel(item) {
    if (!item) return '';
    if (item.kind === 'thinking') return item.done ? '完成' : '处理中';
    if (item.kind === 'command') {
      if (item.status === 'running') return '执行中';
      if (item.status === 'failed') return '失败';
      return '完成';
    }
    if (item.kind === 'tool') return item.status === 'failed' ? '失败' : '调用';
    if (item.kind === 'file') {
      if (item.status === 'saved') return '已保存';
      if (item.status === 'failed') return '失败';
      if (['completed', 'done', 'ok'].includes(item.status)) return '已修改';
      return '修改中';
    }
    if (item.kind === 'plan') return item.done ? '完成' : '进行中';
    if (item.kind === 'approval') {
      const requestId = approvalRequestId(item);
      if (requestId > 0 && approvalResolvedByRequestId.value[requestId]) return '已提交';
      if (requestId <= 0) return '不可交互';
      return '待确认';
    }
    return '';
  };
}

function createToolSummaryText(stateLabel, commandTitle) {
  return function toolSummaryText(item) {
    if (!item || typeof item !== 'object') return '';
    if (item.kind === 'tool') {
      const tool = summarizeToolActivity(item.tool || item.toolName || item.name, item);
      const toolName = compactPopoverText(tool.name || '未知工具', 56);
      const elapsed = Number(item.elapsedMs);
      const elapsedText = Number.isFinite(elapsed) && elapsed > 0 ? ` · ${Math.round(elapsed)}ms` : '';
      const summary = compactPopoverText(tool.summary, 56);
      const detail = compactPopoverText(toolActivityDetail(item), 96) || compactPopoverText(displayFilePath(item.file), 96);
      const head = summary ? `${toolName} · ${summary}` : toolName;
      return detail ? `${head}${elapsedText} · ${detail}` : `${head}${elapsedText}`;
    }
    if (item.kind === 'command') {
      const status = stateLabel(item) || '命令';
      const title = compactPopoverText(commandTitle(item), 96);
      const output = compactPopoverText(item.output, 96);
      return output ? `${status} · ${title} · ${output}` : `${status} · ${title}`;
    }
    if (item.kind === 'file') {
      const status = stateLabel(item) || '文件';
      const file = compactPopoverText(displayFilePath(item.file) || '未知文件', 108);
      return `${status} · ${file}`;
    }
    return '';
  };
}

function toolTickerText(item) {
  if (!item || item.kind !== 'tool') return '';
  const tool = summarizeToolActivity(item.tool || item.toolName || item.name, item);
  const statusPrefix = tool.status === 'failed' ? '失败 · ' : '';
  const toolName = compactPopoverText(tool.name || '未知工具', 32);
  const elapsed = Number(item.elapsedMs);
  const elapsedText = Number.isFinite(elapsed) && elapsed > 0 ? ` ${Math.round(elapsed)}ms` : '';
  const detail = compactPopoverText(toolActivityDetail(item), 68) || compactPopoverText(displayFilePath(item.file), 68);
  return detail ? `${statusPrefix}${toolName}${elapsedText} · ${detail}` : `${statusPrefix}${toolName}${elapsedText}`;
}

function createCopyFilePath(copy) {
  return async function copyFilePath(path) {
    const target = (path || '').toString().trim();
    if (!target) return;
    const copied = await copy(target);
    if (copied) {
      logInfo('ui', 'timeline.file_path_copied', { path: target });
    } else {
      logWarn('ui', 'timeline.file_path_copy_failed', { path: target });
    }
  };
}

function createCopyPlanText(copy) {
  return async function copyPlanText(text) {
    const target = (text || '').toString().trim();
    if (!target) return;
    const copied = await copy(target);
    if (copied) {
      logInfo('ui', 'timeline.plan_text_copied', { length: target.length });
    } else {
      logWarn('ui', 'timeline.plan_text_copy_failed', { length: target.length });
    }
  };
}

function createPlanCardSpec(stateLabel) {
  return function planCardSpec(item) {
    const status = stateLabel(item) || (item?.done ? '完成' : '进行中');
    const timeText = formatTime(item?.ts);
    const children = [];
    const metaChildren = [
      { type: 'Badge', text: status, variant: item?.done ? 'success' : 'primary' },
    ];
    if (timeText) {
      metaChildren.push({ type: 'Text', text: timeText });
    }
    children.push({ type: 'Stack', direction: 'row', gap: 8, children: metaChildren });
    children.push({ type: 'Separator' });

    const rawText = (item?.text || '').toString();
    if (itemHasSpec(rawText)) {
      const parts = splitBySpec(rawText);
      parts.forEach((part) => {
        if (part?.type === 'text' && (part.content || '').toString().trim()) {
          children.push({ type: 'Markdown', text: part.content });
          return;
        }
        if (part?.type === 'spec' && part.spec && typeof part.spec === 'object') {
          children.push(part.spec);
        }
      });
    } else if (rawText.trim()) {
      children.push({ type: 'Markdown', text: rawText });
    } else {
      children.push({ type: 'Text', text: '(空计划)' });
    }

    return {
      type: 'Card',
      title: 'PLAN',
      description: item?.done ? '已完成' : '进行中',
      children,
    };
  };
}

function resolveThreadNameById(props, threadId, fallback = '') {
  const id = (threadId || '').toString().trim();
  if (!id) return (fallback || '').toString().trim();
  if (typeof props.resolveThreadDisplayName === 'function') {
    const resolved = props.resolveThreadDisplayName(id);
    const text = (resolved || '').toString().trim();
    if (text) return text;
  }
  return (fallback || id).toString().trim();
}

function internalSourceName(props, item) {
  return item?.kind === 'user' && item?.internal
    ? resolveThreadNameById(props, item.fromThreadId, item.fromDisplay || '系统')
    : '';
}

function createInternalRouteLabel(props) {
  return function internalRouteLabel(item) {
    const toName = item?.kind === 'user' && item?.internal
      ? resolveThreadNameById(props, item.toThreadId, item.toDisplay || '')
      : '';
    return toName ? `→ ${toName}` : '';
  };
}

function createRoleLabel(props) {
  return function roleLabel(item) {
    switch (item?.kind) {
      case 'user':
        if (item?.internal) return internalSourceName(props, item) || '内部消息';
        return '你';
      case 'assistant':
        return '助手';
      case 'thinking':
        return '思考';
      case 'command':
        return '命令';
      case 'tool':
        return '工具';
      case 'file':
        return '文件';
      case 'approval':
        return '审批';
      case 'plan':
        return '计划';
      case 'error':
        return '错误';
      case 'turn_start':
      case 'turn_end':
      case 'turn_interrupted':
        return '回合';
      default:
        logWarn('ui', 'timeline.roleLabel.unknown_kind', {
          kind: item?.kind, id: item?.id, text_len: (item?.text || '').length,
        });
        return '事件';
    }
  };
}

export function useTimelineHelpers(props, {
  approvalRequestId,
  approvalResolvedByRequestId,
  commandTitle,
}) {
  const stateLabel = createStateLabel(approvalRequestId, approvalResolvedByRequestId);
  const toolSummaryText = createToolSummaryText(stateLabel, commandTitle);
  const copyFilePath = createCopyFilePath(copyTextToClipboard);
  const copyPlanText = createCopyPlanText(copyTextToClipboard);
  const planCardSpec = createPlanCardSpec(stateLabel);
  const roleLabel = createRoleLabel(props);
  const internalRouteLabel = createInternalRouteLabel(props);

  return {
    formatTime,
    copyTextToClipboard,
    copyFilePath,
    copyPlanText,
    displayFilePath,
    compactPopoverText,
    stateLabel,
    toolSummaryKindLabel,
    toolSummaryText,
    toolTickerText,
    roleLabel,
    bubbleRole,
    isDialog,
    hasAvatar,
    avatarText,
    internalRouteLabel,
    planCardSpec,
    itemHasSpec,
    splitBySpec,
  };
}
