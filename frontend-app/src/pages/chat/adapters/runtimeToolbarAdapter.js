/*
 * Runtime toolbar 概览指标模型：把 diffSummary 映射为带名称、单位和解释的指标。
 * 数值完全来自 diffSummary，不伪造任何 runtime 数据。
 */
function runtimeToolbarMetrics(diffSummary) {
  return [
    {
      key: 'files',
      label: '文件',
      value: `${diffSummary.fileCount}`,
      unit: '个',
      tone: 'neutral',
      ariaLabel: `代码变更文件数：${diffSummary.fileCount} 个`,
      tooltip: '本次运行中产生代码变更的文件总数。',
    },
    {
      key: 'changedLines',
      label: '变更',
      value: `${diffSummary.changedLines}`,
      unit: '行',
      tone: 'neutral',
      ariaLabel: `代码变更行数：${diffSummary.changedLines} 行`,
      tooltip: '本次变更中新增与删除的代码行数合计。',
    },
    {
      key: 'additions',
      label: '新增',
      value: `+${diffSummary.additions}`,
      unit: '行',
      tone: 'good',
      ariaLabel: `代码新增行数：+${diffSummary.additions} 行`,
      tooltip: '本次变更新增的代码行数。',
    },
    {
      key: 'deletions',
      label: '删除',
      value: `-${diffSummary.deletions}`,
      unit: '行',
      tone: 'bad',
      ariaLabel: `代码删除行数：-${diffSummary.deletions} 行`,
      tooltip: '本次变更删除的代码行数。',
    },
  ];
}

export { runtimeToolbarMetrics };
