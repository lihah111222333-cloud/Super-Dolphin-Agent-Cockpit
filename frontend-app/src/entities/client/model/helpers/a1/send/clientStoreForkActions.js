import { listSharedFiles, readSharedFile } from '../../../../../../shared/api/backendApi.js';
import { sessionApi } from '../../../../../../shared/api/sessionApi.js';
import {
  buildForkThreadState,
  cachedForkSharedFiles,
  createLoadForkSharedFiles,
  forkSourceTitle,
  initialForkSharedFilePaths,
  mergeForkSharedFilesWithSelected,
  normalizeForkSharedFiles,
} from '../../../threadForkState.js';
import { normalizeThreadIdentity } from '../../threadIdentity.js';
import { threadActivityTimestamp } from '../../threadActivityMetrics.js';
import { DEFAULT_PROVIDER, emptyForkDraft, normalizeString } from '../clientStoreUtils.js';
import { backendThreadIdForState, threadMatchesIdentifier } from '../clientStoreRuntimeThreadModel.js';
import { actionNotice } from './clientStoreActionNotice.js';
import { forkSourceThread } from './clientStoreSendInput.js';

function addForkThreadState(options) {
  const {
    state,
    threadId,
    sourceThreadId,
    sourceThread,
    identity,
    provisionalName,
    kickoffText,
  } = options;
  return buildForkThreadState({
    state,
    threadId,
    sourceThreadId,
    sourceThread,
    identity,
    provisionalName,
    kickoffText,
    deps: {
      actionNotice,
      defaultProvider: DEFAULT_PROVIDER,
      emptyForkDraft,
      threadActivityTimestamp,
      threadMatchesIdentifier,
    },
  });
}

const loadForkSharedFiles = createLoadForkSharedFiles({ readSharedFile });

const forkActionDeps = {
  actionNotice,
  addForkThreadState,
  backendThreadIdForState,
  cachedForkSharedFiles,
  emptyForkDraft,
  forkThread: (payload) => sessionApi.fork(payload),
  forkSourceThread,
  forkSourceTitle,
  initialForkSharedFilePaths,
  listSharedFiles: () => listSharedFiles(),
  loadForkSharedFiles,
  mergeForkSharedFilesWithSelected,
  normalizeForkSharedFiles,
  normalizeString,
  normalizeThreadIdentity,
  startTurn: (payload) => sessionApi.startTurn(payload),
};

export { addForkThreadState, forkActionDeps, loadForkSharedFiles };
