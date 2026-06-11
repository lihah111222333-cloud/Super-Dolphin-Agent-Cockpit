function emptyDiffSummary() {
  return { fileCount: 0, additions: 0, deletions: 0, changedLines: 0, files: [] };
}

function buildDiffSummary(files) {
  const changedFiles = files.filter((file) => file.additions > 0 || file.deletions > 0 || file.filename);
  const additions = changedFiles.reduce((sum, file) => sum + file.additions, 0);
  const deletions = changedFiles.reduce((sum, file) => sum + file.deletions, 0);
  return {
    fileCount: changedFiles.length,
    additions,
    deletions,
    changedLines: additions + deletions,
    files: changedFiles.map((file) => ({
      filename: file.filename,
      additions: file.additions,
      deletions: file.deletions,
      text: file.lines.join('\n'),
    })),
  };
}

export { buildDiffSummary, emptyDiffSummary };
