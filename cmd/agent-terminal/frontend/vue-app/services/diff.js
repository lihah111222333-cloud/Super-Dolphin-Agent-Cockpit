export function parseUnifiedDiff(diffText) {
  if (!diffText || typeof diffText !== 'string') return [];

  const lines = diffText.split('\n');
  const files = [];
  let current = null;
  let oldLine = 1;
  let newLine = 1;
  let pendingFileHeader = null;


  function ensureCurrent(filename = 'file') {
    if (current) return current;
    current = { filename, lines: [] };
    files.push(current);
    oldLine = 1;
    newLine = 1;
    return current;
  }

  function startFile(filename) {
    current = { filename: filename || `file-${files.length + 1}`, lines: [] };
    files.push(current);
    oldLine = 1;
    newLine = 1;
    return current;
  }

  function parseDiffFilename(line, prefix) {
    const raw = line.slice(prefix.length).trim();
    if (!raw || raw === '/dev/null') return '';
    return raw.startsWith('a/') || raw.startsWith('b/') ? raw.slice(2) : raw;
  }

  for (const line of lines) {
    if (line.startsWith('diff --git')) {
      const match = line.match(/^diff --git a\/(.+?) b\/(.+)$/);
      const filename = match?.[2] || match?.[1] || `file-${files.length + 1}`;
      pendingFileHeader = null;
      startFile(filename);
      continue;
    }

    if (line.startsWith('*** Begin Patch') || line.startsWith('*** End Patch') || line.startsWith('*** End of File')) {
      pendingFileHeader = null;
      continue;
    }

    if (line.startsWith('*** Update File:') || line.startsWith('*** Add File:') || line.startsWith('*** Delete File:')) {
      const prefix = line.startsWith('*** Update File:')
        ? '*** Update File:'
        : line.startsWith('*** Add File:')
          ? '*** Add File:'
          : '*** Delete File:';
      const filename = parseDiffFilename(line, prefix);
      pendingFileHeader = null;
      startFile(filename || current?.filename || `file-${files.length + 1}`);
      continue;
    }

    if (line.startsWith('*** Move to:')) {
      const filename = parseDiffFilename(line, '*** Move to:');
      if (current && filename) current.filename = filename;
      continue;
    }

    if (line.startsWith('--- ')) {
      pendingFileHeader = {
        oldFilename: parseDiffFilename(line, '---'),
        beginsNewFile: Boolean(current?.lines?.length),
      };
      continue;
    }

    if (line.startsWith('+++ ')) {
      const filename = parseDiffFilename(line, '+++');
      const headerFilename = filename || pendingFileHeader?.oldFilename || current?.filename || `file-${files.length + 1}`;
      if (!current || pendingFileHeader?.beginsNewFile) startFile(headerFilename);
      else current.filename = headerFilename || current.filename;
      pendingFileHeader = null;
      continue;
    }

    if (line.startsWith('index ') || line.startsWith('new file') || line.startsWith('deleted file')) {
      continue;
    }


    if (line.startsWith('@@')) {
      ensureCurrent();
      const hunkHeaderRe = new RegExp('^@@\\s+\\-(\\d+)(?:,\\d+)?\\s+\\+(\\d+)(?:,\\d+)?\\s+@@');
      const match = line.match(hunkHeaderRe);
      oldLine = Number(match?.[1] || 1);
      newLine = Number(match?.[2] || 1);
      current.lines.push({
        type: 'hunk',
        text: line,
        oldNo: '',
        newNo: '',
      });
      continue;
    }

    if (line.startsWith('+')) {
      if (line.startsWith('+++')) continue;
      ensureCurrent();
      current.lines.push({
        type: 'add',
        text: line.slice(1),
        oldNo: '',
        newNo: newLine,
      });
      newLine += 1;
      continue;
    }

    if (line.startsWith('-')) {
      if (line.startsWith('---')) continue;
      ensureCurrent();
      current.lines.push({
        type: 'del',
        text: line.slice(1),
        oldNo: oldLine,
        newNo: '',
      });
      oldLine += 1;
      continue;
    }

    if (!current) {
      continue;
    }

    if (line.startsWith('\\')) {
      current.lines.push({
        type: 'meta',
        text: line,
        oldNo: '',
        newNo: '',
      });
      continue;
    }

    current.lines.push({
      type: 'ctx',
      text: line.startsWith(' ') ? line.slice(1) : line,
      oldNo: oldLine,
      newNo: newLine,
    });
    oldLine += 1;
    newLine += 1;
  }

  return files;
}

export function diffStats(file) {
  const add = file.lines.filter((item) => item.type === 'add').length;
  const del = file.lines.filter((item) => item.type === 'del').length;
  return { add, del };
}
