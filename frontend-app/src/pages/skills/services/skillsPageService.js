import {
  applySkillResolution as applySkillResolutionBackend,
  createSkill as createSkillBackend,
  deleteDatasourceDocument as deleteDatasourceDocumentBackend,
  deleteSkill as deleteSkillBackend,
  getDashboardPage as getDashboardPageBackend,
  getDatasourceDocument as getDatasourceDocumentBackend,
  importDatasourceLocalFile as importDatasourceLocalFileBackend,
  importSkillDirectories as importSkillDirectoriesBackend,
  listDatasourceChunks as listDatasourceChunksBackend,
  listDatasourceDocuments as listDatasourceDocumentsBackend,
  listMCPServers as listMCPServersBackend,
  listSkillFiles as listSkillFilesBackend,
  listSkillResolutions as listSkillResolutionsBackend,
  listSkillTools as listSkillToolsBackend,
  previewSkillResolution as previewSkillResolutionBackend,
  readSkill as readSkillBackend,
  selectFiles as selectFilesBackend,
  selectProjectDirs as selectProjectDirsBackend,
  startPlaywrightMCPServer as startPlaywrightMCPServerBackend,
  startSQLiteMCPServer as startSQLiteMCPServerBackend,
  stopPlaywrightMCPServer as stopPlaywrightMCPServerBackend,
  stopSQLiteMCPServer as stopSQLiteMCPServerBackend,
  suggestSkillSummary as suggestSkillSummaryBackend,
  updateDatasourceDocument as updateDatasourceDocumentBackend,
  writeSkill as writeSkillBackend,
} from '../../../shared/api/backendApi.js';

/*
 * skills page service only forwards page actions to backendApi.
 * SkillsPage keeps UI state, query invalidation, and error wording locally.
 */

export function applySkillResolution(payload) {
  return applySkillResolutionBackend(payload);
}

export function createSkill(payload) {
  return createSkillBackend(payload);
}

export function deleteDatasourceDocument(payload) {
  return deleteDatasourceDocumentBackend(payload);
}

export function deleteSkill(payload) {
  return deleteSkillBackend(payload);
}

export function getDashboardPage(payload) {
  return getDashboardPageBackend(payload);
}

export function getDatasourceDocument(payload) {
  return getDatasourceDocumentBackend(payload);
}

export function importDatasourceLocalFile(payload) {
  return importDatasourceLocalFileBackend(payload);
}

export function importSkillDirectories(payload) {
  return importSkillDirectoriesBackend(payload);
}

export function listDatasourceDocuments(payload) {
  return listDatasourceDocumentsBackend(payload);
}

export function listDatasourceChunks(payload) {
  return listDatasourceChunksBackend(payload);
}

export function listMCPServers(payload) {
  return listMCPServersBackend(payload);
}

export function listSkillFiles(payload) {
  return listSkillFilesBackend(payload);
}

export function listSkillResolutions(payload) {
  return listSkillResolutionsBackend(payload);
}

export function listSkillTools(payload) {
  return listSkillToolsBackend(payload);
}

export function previewSkillResolution(payload) {
  return previewSkillResolutionBackend(payload);
}

export function readSkill(payload) {
  return readSkillBackend(payload);
}

export function selectFiles(payload) {
  return selectFilesBackend(payload);
}

export function selectProjectDirs(payload) {
  return selectProjectDirsBackend(payload);
}

export function startPlaywrightMCPServer(payload) {
  return startPlaywrightMCPServerBackend(payload);
}

export function startSQLiteMCPServer(payload) {
  return startSQLiteMCPServerBackend(payload);
}

export function stopPlaywrightMCPServer(payload) {
  return stopPlaywrightMCPServerBackend(payload);
}

export function stopSQLiteMCPServer(payload) {
  return stopSQLiteMCPServerBackend(payload);
}

export function suggestSkillSummary(payload) {
  return suggestSkillSummaryBackend(payload);
}

export function updateDatasourceDocument(payload) {
  return updateDatasourceDocumentBackend(payload);
}

export function writeSkill(payload) {
  return writeSkillBackend(payload);
}
