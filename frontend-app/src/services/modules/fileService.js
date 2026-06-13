import {
  deleteSharedFile as deleteSharedFileBackend,
  listSharedFiles as listSharedFilesBackend,
  openSharedFile as openSharedFileBackend,
  readSharedFile as readSharedFileBackend,
  saveTextFile as saveTextFileBackend,
} from '../../shared/api/backendApi.js';
import { adaptSharedFileDetail, adaptSharedFilesDashboard } from '../../adapters/fileAdapter.js';
import { DEFAULT_REQUEST_TIMEOUT_MS, runServiceRequest, withRequestTimeout } from '../apiClient.js';

/*
 * file service 把 shared file 响应整理给页面用。
 * 打开、删除、保存只转发后端结果。
 */

async function listSharedFilesDashboard() {
  return runServiceRequest(async () => {
    const response = await withRequestTimeout(
      listSharedFilesBackend(),
      DEFAULT_REQUEST_TIMEOUT_MS,
      '共享文件加载超时，请检查文件索引或后端状态。',
    );
    return adaptSharedFilesDashboard(response);
  }, '加载共享文件失败');
}

async function readSharedFile(params, fallbackFile = {}) {
  return runServiceRequest(async () => {
    const response = await readSharedFileBackend(params);
    return adaptSharedFileDetail(response, fallbackFile);
  }, '读取共享文件失败');
}

async function openSharedFile(params) {
  return runServiceRequest(() => openSharedFileBackend(params), '打开共享文件失败');
}

async function deleteSharedFile(params) {
  return runServiceRequest(() => deleteSharedFileBackend(params), '删除共享文件失败');
}

async function saveTextFile(params) {
  return runServiceRequest(() => saveTextFileBackend(params), '保存文本文件失败');
}

export { deleteSharedFile, listSharedFilesDashboard, openSharedFile, readSharedFile, saveTextFile };
