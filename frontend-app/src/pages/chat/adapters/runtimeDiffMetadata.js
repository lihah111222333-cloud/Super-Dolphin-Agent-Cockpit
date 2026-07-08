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

export { isUnifiedDiffMetadataLine };
