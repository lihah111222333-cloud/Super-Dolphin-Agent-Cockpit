const UNIFIED_DIFF_METADATA_PREFIXES = [
  'diff --git',
  'index ',
  '--- ',
  '+++ ',
  '*** Begin Patch',
  '*** Update File:',
  '*** Add File:',
  '*** Delete File:',
  '*** Move to:',
  '*** End Patch',
  '*** End of File',
];

function isUnifiedDiffMetadataLine(line) {
  return UNIFIED_DIFF_METADATA_PREFIXES.some((prefix) => line.startsWith(prefix));
}

function diffLineEntry({ index, type, oldNo = '', newNo = '', prefix = '', content }) {
  return { key: `${index}:${type}`, type, oldNo, newNo, prefix, content };
}

function parseHunkLineEntry(state, line, index) {
  const match = line.match(/^@@ -(\d+)(?:,\d+)? \+(\d+)(?:,\d+)? @@/);
  state.oldLine = match ? Number(match[1]) : null;
  state.newLine = match ? Number(match[2]) : null;
  return diffLineEntry({ index, type: 'hunk', content: line });
}

function parseChangedDiffLineEntry(state, line, index) {
  if (line.startsWith('+') && !line.startsWith('+++')) {
    const entry = diffLineEntry({ index, type: 'add', newNo: state.newLine ?? '', prefix: '+', content: line.slice(1) });
    if (state.newLine !== null) state.newLine += 1;
    return entry;
  }
  if (line.startsWith('-') && !line.startsWith('---')) {
    const entry = diffLineEntry({ index, type: 'del', oldNo: state.oldLine ?? '', prefix: '-', content: line.slice(1) });
    if (state.oldLine !== null) state.oldLine += 1;
    return entry;
  }
  return null;
}

function parseContextDiffLineEntry(state, line, index) {
  const entry = diffLineEntry({
    index,
    type: 'context',
    oldNo: state.oldLine ?? '',
    newNo: state.newLine ?? '',
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
  return String(fileText || '').split('\n').flatMap((line, index) => parseUnifiedDiffLineEntry(state, line, index));
}

export { parseUnifiedDiffLineEntries };
