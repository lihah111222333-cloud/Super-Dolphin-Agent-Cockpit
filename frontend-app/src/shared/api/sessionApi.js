import {
  getThreadMessages,
  interruptTurn,
  startThread,
  startTurn,
} from './backendApi.js';

export const sessionApi = Object.freeze({
  start(params) {
    return startThread(params);
  },
  startTurn(params) {
    return startTurn(params);
  },
  interruptTurn(params) {
    return interruptTurn(params);
  },
  interrupt(threadId, cwd, source = 'frontend') {
    return interruptTurn({ threadId, cwd, source });
  },
  getThreadMessages(params) {
    return getThreadMessages(params);
  },
  messages(threadId, limit = 100, before = '') {
    return getThreadMessages({ threadId, limit, before });
  },
});
