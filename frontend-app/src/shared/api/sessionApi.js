import {
  forkThread,
  getThreadMessages,
  interruptTurn,
  startThread,
  startTurn,
} from './backendApi.js';

export const sessionApi = Object.freeze({
  fork(params) {
    return forkThread(params);
  },
  start(params) {
    return startThread(params);
  },
  startTurn(params) {
    return startTurn(params);
  },
  interrupt(params) {
    return interruptTurn(params);
  },
  messages(threadId, limit = 100, before = '') {
    return getThreadMessages({ threadId, limit, before });
  },
});
