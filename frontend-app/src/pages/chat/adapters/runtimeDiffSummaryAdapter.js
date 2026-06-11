import { buildDiffSummary, emptyDiffSummary } from './runtimeDiffSummaryResultAdapter.js';
const PATCH_UPDATE_FILE_PREFIX = '*** Update File:';
const PATCH_ADD_FILE_PREFIX = '*** Add File:';
const PATCH_DELETE_FILE_PREFIX = '*** Delete File:';
const PATCH_MOVE_TO_PREFIX = '*** Move to:';
const PATCH_BOUNDARY_PREFIXES = ['*** Begin Patch', '*** End Patch', '*** End of File'];
const DIFF_HEADER_PREFIXES = ['index ', 'new file', 'deleted file', '@@'];

function startsWithAny(value, prefixes) {
  return prefixes.some((prefix) => value.startsWith(prefix));
}

function parseDiffFilename(line, prefix) {
  const raw = line.slice(prefix.length).trim();
  if (!raw || raw === '/dev/null') return '';
  return raw.startsWith('a/') || raw.startsWith('b/') ? raw.slice(2) : raw;
}

function createDiffSummaryState() {
  return { files: [], current: null, pendingFileHeader: null };
}

function startDiffSummaryFile(state, filename) {
  state.current = {
    filename: filename || `file-${state.files.length + 1}`,
    additions: 0,
    deletions: 0,
    lines: [],
  };
  state.files.push(state.current);
}

function ensureDiffSummaryFile(state) {
  if (!state.current) startDiffSummaryFile(state);
}

function appendDiffSummaryLine(state, line) {
  ensureDiffSummaryFile(state);
  state.current.lines.push(line);
}

function diffPatchFilePrefix(line) {
  if (line.startsWith(PATCH_UPDATE_FILE_PREFIX)) return PATCH_UPDATE_FILE_PREFIX;
  if (line.startsWith(PATCH_ADD_FILE_PREFIX)) return PATCH_ADD_FILE_PREFIX;
  if (line.startsWith(PATCH_DELETE_FILE_PREFIX)) return PATCH_DELETE_FILE_PREFIX;
  return '';
}

function handleDiffGitHeader(state, line) {
  const match = line.match(/^diff --git a\/(.+?) b\/(.+)$/);
  state.pendingFileHeader = null;
  startDiffSummaryFile(state, match?.[2] || match?.[1] || `file-${state.files.length + 1}`);
  state.current.lines.push(line);
}

function handlePatchFileHeader(state, line, prefix) {
  state.pendingFileHeader = null;
  startDiffSummaryFile(
    state,
    parseDiffFilename(line, prefix) || state.current?.filename || `file-${state.files.length + 1}`,
  );
  state.current.lines.push(line);
}

function handleOldDiffHeader(state, line) {
  state.pendingFileHeader = {
    oldFilename: parseDiffFilename(line, '---'),
    beginsNewFile: Boolean(state.current && (state.current.additions > 0 || state.current.deletions > 0)),
    line,
  };
  if (state.current && !state.pendingFileHeader.beginsNewFile) state.current.lines.push(line);
}

function handleNewDiffHeader(state, line) {
  const filename = parseDiffFilename(line, '+++');
  const fallback = state.current?.filename || `file-${state.files.length + 1}`;
  const headerFilename = filename || state.pendingFileHeader?.oldFilename || fallback;
  if (!state.current || state.pendingFileHeader?.beginsNewFile) startDiffSummaryFile(state, headerFilename);
  else state.current.filename = headerFilename || state.current.filename;
  if (state.pendingFileHeader?.line && !state.current.lines.includes(state.pendingFileHeader.line)) {
    state.current.lines.push(state.pendingFileHeader.line);
  }
  state.current.lines.push(line);
  state.pendingFileHeader = null;
}

function handleChangedDiffLine(state, line) {
  ensureDiffSummaryFile(state);
  if (line.startsWith('+')) state.current.additions += 1;
  if (line.startsWith('-')) state.current.deletions += 1;
  state.current.lines.push(line);
}

function applyPatchBoundaryLine(state, line) {
  if (!startsWithAny(line, PATCH_BOUNDARY_PREFIXES)) return false;
  state.pendingFileHeader = null;
  return true;
}

function applyPatchFileHeaderLine(state, line) {
  const patchPrefix = diffPatchFilePrefix(line);
  if (!patchPrefix) return false;
  handlePatchFileHeader(state, line, patchPrefix);
  return true;
}

function applyPatchMoveLine(state, line) {
  if (!line.startsWith(PATCH_MOVE_TO_PREFIX)) return false;
  const filename = parseDiffFilename(line, PATCH_MOVE_TO_PREFIX);
  if (state.current && filename) state.current.filename = filename;
  appendDiffSummaryLine(state, line);
  return true;
}

function applyDiffFileHeaderLine(state, line) {
  if (line.startsWith('diff --git')) handleDiffGitHeader(state, line);
  else if (line.startsWith('--- ')) handleOldDiffHeader(state, line);
  else if (line.startsWith('+++ ')) handleNewDiffHeader(state, line);
  else return false;
  return true;
}

function applyDiffMetaLine(state, line) {
  if (!startsWithAny(line, DIFF_HEADER_PREFIXES)) return false;
  appendDiffSummaryLine(state, line);
  return true;
}

function applyDiffContentLine(state, line) {
  const changed = (line.startsWith('+') && !line.startsWith('+++')) || (line.startsWith('-') && !line.startsWith('---'));
  if (!changed) return false;
  handleChangedDiffLine(state, line);
  return true;
}

function applyDiffSummaryLine(state, line) {
  const handlers = [applyDiffFileHeaderLine, applyPatchBoundaryLine, applyPatchFileHeaderLine, applyPatchMoveLine, applyDiffMetaLine, applyDiffContentLine];
  const handled = handlers.some((handler) => handler(state, line));
  if (!handled && state.current) state.current.lines.push(line);
  return handled;
}

function summarizeUnifiedDiff(diffText) {
  if (!diffText || typeof diffText !== 'string') return emptyDiffSummary();
  const state = createDiffSummaryState();
  for (const line of diffText.split('\n')) applyDiffSummaryLine(state, line);
  return buildDiffSummary(state.files);
}

export { summarizeUnifiedDiff };
