export type ThreadTimelineEntry = {
  kind?: string;
};

export type ThreadStoreLike = {
  loadMessages?: (threadId: string, limit?: number, options?: { syncRuntime?: boolean }) => Promise<any>;
  getThreadTimeline?: (threadId: string) => ThreadTimelineEntry[];
  shouldReloadThreadHistory?: (threadId: string) => boolean;
  syncThreadState?: (threadId: string) => Promise<any>;
};

export type ThreadSelectionFreshness = {
  requestedHistory: boolean;
  syncedThreadState: boolean;
  forcedHistoryReload: boolean;
};

export type ThreadSelectionFreshnessOptions = {
  reason?: 'selection' | 'page-enter' | 'bootstrap';
  previousThreadId?: string | null | undefined;
};

export type ThreadCardSource = {
  id?: string;
  name?: string;
};

export type ThreadRuntimeInfo = {
  cwdMismatch?: boolean;
  cwdMismatchReason?: string;
  provider?: string;
};

export type VisibleChatThreadCard = {
  id: string;
  name: string;
  showId: boolean;
  status: string;
  statusHeader: string;
  interruptible: boolean;
  pinnedAt: number;
  archivedAt: number;
  isArchived: boolean;
  isPinned: boolean;
  selected: boolean;

  cwdMismatch: boolean;
  cwdMismatchReason: string;
  provider: string;
};

export type BuildVisibleChatThreadCardsOptions = {
  threads?: ThreadCardSource[];
  selectedThreadId?: string;

  pinnedMap?: Record<string, number> | null;
  archivedMap?: Record<string, number> | null;
  runtimeById?: Record<string, ThreadRuntimeInfo> | null;
  showArchived?: boolean;
  displayNameOf?: (thread: ThreadCardSource) => string;
  statusOf?: (threadId: string) => string;
  statusHeaderOf?: (threadId: string) => string;
  interruptibleOf?: (threadId: string) => boolean;
};

export type VisibleChatThreadCardState = {
  cards: VisibleChatThreadCard[];
  activeCount: number;
  archivedCount: number;
};

export type RefLike<T> = {
  value: T;
};

export type TextPreviewState = {
  previewKind: 'markdown' | 'text';
  path: string;
  filePath: string;
  text: string;
  language: string;
  startLine: number;
  endLine: number;
  totalLines: number;
  editable: boolean;
};

export type ThreadSelectionOptions = {
  selectedThreadId: RefLike<string>;
  threadStore: ThreadStoreLike | null | undefined;
  focusedDiffPath: RefLike<string>;
  focusedDiffLine: RefLike<number>;
  fallbackDiffText: RefLike<string>;
  fallbackMediaPreview: RefLike<any>;
  fallbackMarkdownPreview: RefLike<TextPreviewState | null>;
  scheduleScrollToBottom: (force?: boolean) => void;
};

export type ProcessActivityItem = {
  id: string;
  time: string;
  message: string;
  status: 'active' | 'done' | 'failed';
  kind?: 'thinking' | 'command';
  title?: string;
  command?: string;
  output?: string;
  exitCode?: number;
  multiline?: boolean;
};
