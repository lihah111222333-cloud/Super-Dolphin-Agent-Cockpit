import * as fileService from '../../../services/modules/fileService.js';

function assertPlainObject(value, message) {
  if (!value || typeof value !== 'object' || Array.isArray(value)) {
    throw new Error(message);
  }
  return value;
}

function normalizeRequiredPath(path) {
  if (typeof path !== 'string') throw new Error('file path is required');
  const normalized = path.trim();
  if (!normalized) throw new Error('file path is required');
  return normalized;
}

function normalizeRequiredFilename(filename) {
  if (typeof filename !== 'string') throw new Error('default filename is required');
  const normalized = filename.trim();
  if (!normalized) throw new Error('default filename is required');
  return normalized;
}

function assertSharedFileDetail(value) {
  const detail = assertPlainObject(value, 'shared file detail response must be an object');
  normalizeRequiredPath(detail.path);
  return detail;
}

function normalizeSaveTextFileParams(params) {
  const payload = assertPlainObject(params, 'file save params are required');
  const defaultFilename = normalizeRequiredFilename(payload.defaultFilename);
  if ('defaultPath' in payload && typeof payload.defaultPath !== 'string') {
    throw new Error('default path must be a string');
  }
  if (typeof payload.content !== 'string') throw new Error('file content is required');
  return { ...payload, defaultFilename, content: payload.content };
}

function createFilesPageService(api = fileService) {
  return {
    async listSharedFilesDashboard() {
      return assertPlainObject(await api.listSharedFilesDashboard(), 'shared files dashboard response must be an object');
    },
    async readSharedFile(path, fallbackFile) {
      const normalized = normalizeRequiredPath(path);
      return assertSharedFileDetail(await api.readSharedFile({ path: normalized }, fallbackFile));
    },
    openSharedFile(path) {
      return api.openSharedFile({ path: normalizeRequiredPath(path) });
    },
    deleteSharedFile(path) {
      return api.deleteSharedFile({ path: normalizeRequiredPath(path) });
    },
    saveTextFile(params) {
      return api.saveTextFile(normalizeSaveTextFileParams(params));
    },
  };
}

const filesPageService = createFilesPageService();

const {
  deleteSharedFile,
  listSharedFilesDashboard,
  openSharedFile,
  readSharedFile,
  saveTextFile,
} = filesPageService;

export {
  createFilesPageService,
  deleteSharedFile,
  filesPageService,
  listSharedFilesDashboard,
  openSharedFile,
  readSharedFile,
  saveTextFile,
};
