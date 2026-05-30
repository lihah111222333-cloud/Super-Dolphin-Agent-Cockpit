import {
  ref,
  computed,
} from '../../lib/vue.esm-browser.prod.js';

export function useDiffPreview(opts) {
  const {
    activeThreadDiffText,
    threadStore,
    buildFocusedDiffSelection,
  } = opts;

  const focusedDiffPath = ref('');
  const focusedDiffLine = ref(0);
  const fallbackDiffText = ref('');
  const fallbackMediaPreview = ref(null);
  // Legacy name kept for page-level compatibility; value may be markdown or plain-text preview state.
  const fallbackMarkdownPreview = ref(null);

  const activeMediaPreview = computed(() => fallbackMediaPreview.value);
  // Downstream components still consume `markdownPreview`, but the payload now also supports text previews.
  const activeMarkdownPreview = computed(() => fallbackMarkdownPreview.value);
  const activeDiffText = computed(() => {
    if (activeMediaPreview.value?.src) return '';
    if (activeMarkdownPreview.value?.text) return '';
    const rawDiffText = (activeThreadDiffText.value || '').toString();
    const targetPath = (focusedDiffPath.value || '').toString().trim();
    if (!targetPath) return rawDiffText;
    if (buildFocusedDiffSelection(rawDiffText, targetPath)) return rawDiffText;
    const fallbackText = (fallbackDiffText.value || '').toString();
    if (!fallbackText) return rawDiffText;
    if (buildFocusedDiffSelection(fallbackText, targetPath)) return fallbackText;
    return fallbackText;
  });
  const activeDiffFocusFile = computed(() => (focusedDiffPath.value || '').toString().trim());
  const activeDiffFocusLine = computed(() => {
    const line = Number(focusedDiffLine.value);
    return Number.isFinite(line) && line > 0 ? Math.floor(line) : 0;
  });

  function timelinePreview(threadId) {
    const items = threadStore.getThreadTimeline(threadId) || [];
    return items
      .filter((item) => ['user', 'assistant', 'thinking', 'command', 'error'].includes(item.kind))
      .slice(-3)
      .map((item, index) => {
        const text = (item.text || item.command || '').toString().trim();
        if (!text) return null;
        const isInternalMessage = item.kind === 'user' && item.internal;
        const prefix = isInternalMessage
          ? '内部消息: '
          : item.kind === 'user'
            ? '你: '
            : item.kind === 'assistant'
              ? '助手: '
              : item.kind === 'thinking'
                ? '思考: '
                : item.kind === 'command'
                  ? '$ '
                  : '错误: ';
        return {
          key: `${item.id || 'i'}-${index}`,
          text: `${prefix}${text}`.slice(0, 140),
        };
      })
      .filter(Boolean);
  }

  function diffPreview(threadId) {
    const text = (threadStore.getThreadDiff(threadId) || '').toString().trim();
    if (!text) return '';
    const lines = text.split('\n').slice(0, 4);
    return lines.join('\n');
  }

  return {
    focusedDiffPath,
    focusedDiffLine,
    fallbackDiffText,
    fallbackMediaPreview,
    fallbackMarkdownPreview,
    activeMediaPreview,
    activeMarkdownPreview,
    activeDiffText,
    activeDiffFocusFile,
    activeDiffFocusLine,
    timelinePreview,
    diffPreview,
  };
}
