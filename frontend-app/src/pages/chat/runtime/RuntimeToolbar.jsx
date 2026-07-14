import React from 'react';
import { Code2, FileText } from 'lucide-react';

function RuntimeToolbar({ diffSummary }) {
  return (
    <div className="runtime-toolbar">
      <output className="runtime-stat" aria-label="代码变更文件数" title={`代码变更文件数: ${diffSummary.fileCount}`}>
        <FileText size={14} /> {diffSummary.fileCount}
      </output>
      <output className="runtime-stat" aria-label="代码变更行数" title={`代码变更行数: ${diffSummary.changedLines}`}>
        <Code2 size={14} /> {diffSummary.changedLines}
      </output>
      <span className="score good" aria-label="代码新增行数" title={`代码新增行数: ${diffSummary.additions}`}>+{diffSummary.additions}</span>
      <span className="score bad" aria-label="代码删除行数" title={`代码删除行数: ${diffSummary.deletions}`}>-{diffSummary.deletions}</span>
    </div>
  );
}

export { RuntimeToolbar };
