import { isUnifiedDiffMetadataLine } from './runtimeDiffMetadata.js';

function diffLineEntry({
  index,
  type,
  oldNo = '',
  newNo = '',
  prefix = '',
  content,
}) {
  return { key: `${index}:${type}`, type, oldNo, newNo, prefix, content };
}

function lineNumberValue(value) {
  return value === null || value === undefined ? '' : value;
}

function parseHunkLineEntry(state, line, index) {
  const match = line.match(/^@@ -(\d+)(?:,\d+)? \+(\d+)(?:,\d+)? @@/);
  state.oldLine = match ? Number(match[1]) : null;
  state.newLine = match ? Number(match[2]) : null;
  return diffLineEntry({ index, type: 'hunk', content: line });
}

function parseChangedDiffLineEntry(state, line, index) {
  if (line.startsWith('+') && !line.startsWith('+++')) {
    const entry = diffLineEntry({ index, type: 'add', newNo: lineNumberValue(state.newLine), prefix: '+', content: line.slice(1) });
    if (state.newLine !== null) state.newLine += 1;
    return entry;
  }
  if (line.startsWith('-') && !line.startsWith('---')) {
    const entry = diffLineEntry({ index, type: 'del', oldNo: lineNumberValue(state.oldLine), prefix: '-', content: line.slice(1) });
    if (state.oldLine !== null) state.oldLine += 1;
    return entry;
  }
  return null;
}

function parseContextDiffLineEntry(state, line, index) {
  const entry = diffLineEntry({
    index,
    type: 'context',
    oldNo: lineNumberValue(state.oldLine),
    newNo: lineNumberValue(state.newLine),
    content: line.slice(1),
  });
  if (state.oldLine !== null) state.oldLine += 1;
  if (state.newLine !== null) state.newLine += 1;
  return entry;
}

function parseUnifiedDiffLineEntry(state, line, index) {
  if (isUnifiedDiffMetadataLine(line)) return [];
  if (line.startsWith('@@')) return parseHunkLineEntry(state, line, index);
  const changed = parseChangedDiffLineEntry(state, line, index);
  if (changed) return changed;
  if (line.startsWith(' ')) return parseContextDiffLineEntry(state, line, index);
  return diffLineEntry({ index, type: 'meta', content: line });
}

function parseUnifiedDiffLineEntries(fileText) {
  const state = { oldLine: null, newLine: null };
  const text = fileText === null || fileText === undefined ? '' : String(fileText);
  return text.split('\n').flatMap((line, index) => parseUnifiedDiffLineEntry(state, line, index));
}

export { parseUnifiedDiffLineEntries };
