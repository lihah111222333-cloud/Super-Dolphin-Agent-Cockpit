const ROLE_TEMPLATES = Object.freeze({
  general: [
    ['requirement', '需求分析', '分析用户目标、边界条件、验收标准，并指出任务成功的判断依据。'],
    ['context', '资料搜索', '搜索和梳理项目/资料上下文，找出现有实现、关键文件和可复用能力。'],
    ['solution', '方案设计', '提出可执行方案、步骤拆分、关键改动点和实现顺序。'],
    ['risk', '风险审查', '从反方视角检查遗漏、风险、冲突、过度设计和需要澄清的问题。'],
    ['synthesis', '整合汇总', '等待其他子 Agent 的结果，整合为最终建议、行动清单和验证方式。'],
  ],
  writing: [
    ['idea', '构思', '确定主题、中心思想、结构和每段承担的表达功能。'],
    ['opening', '写开头', '根据构思写出简洁、有吸引力的开头。'],
    ['body', '写主体', '完成主体内容，保证信息具体、层次清晰。'],
    ['ending', '写结尾', '写出收束自然、点题明确的结尾。'],
    ['editor', '整合校对', '等待前面结果，整合全文、压缩字数、润色语言并检查是否符合要求。'],
  ],
  'prompt-rag': [
    ['goal', '目标拆解', '分析 RAG/提示词任务的业务目标、输入输出和验收标准。'],
    ['redundancy', '冗余检查', '判断现有提示词中重复、抽象、不可执行或会污染输出的部分。'],
    ['constraints', '必要约束', '提炼必须保留的事实边界、格式、合规和平台约束。'],
    ['structure', '结构设计', '设计新的提示词模块、调用顺序和结构化输出格式。'],
    ['synthesis', '整合模板', '等待前面结果，整合成可直接落地的提示词方案和验证方法。'],
  ],
  coding: [
    ['scope', '范围分析', '分析需求影响范围、入口、验收标准和可能涉及的模块。'],
    ['search', '代码搜索', '搜索相关文件、接口、状态管理、测试和既有模式。'],
    ['implementation', '实现方案', '设计改动步骤、数据流、关键函数和最小改动路径。'],
    ['tests', '测试验证', '设计单元测试、E2E、手动验证和回归风险清单。'],
    ['review', '整合复核', '等待其他结果，整合最终实施建议并检查方案一致性。'],
  ],
});

function roleTemplateFor(kind) {
  return ROLE_TEMPLATES[kind] || ROLE_TEMPLATES.general;
}

function roleForIndex(kind, index) {
  const template = roleTemplateFor(kind);
  if (index < template.length) return template[index];
  return [`worker-${index + 1}`, `子任务 ${index + 1}`, '独立完成一个可并行的子任务，并输出结论、依据和下一步建议。'];
}

function buildChildPrompt(intent, role, index, count) {
  const [key, title, responsibility] = role;
  const isFinal = index === count - 1;
  const lines = [
    `你是 ${index + 1}/${count} 子 Agent：${title}。`,
    '',
    `你的分工：${responsibility}`,
    '',
    '用户原始任务：',
    intent.task,
    '',
    '工作要求：',
    '- 只专注自己的分工，不要代替其他子 Agent 完成全部工作。',
    '- 输出要具体、可执行，必要时列出依据、文件、风险或待确认问题。',
    '- 如果任务涉及代码，先分析和说明，不要做无关改动。',
  ];
  if (isFinal) {
    lines.push('- 你是汇总 Agent：请先基于当前任务和前面子 Agent 的分工产出一个汇总框架；如果暂时没有拿到其他 Agent 的完整结果，也要先给出可用的初版汇总，并说明后续可继续吸收其他结果迭代。');
  } else {
    lines.push('- 完成后给汇总 Agent 留一段“可汇总摘要”。');
  }
  return lines.join('\n');
}

function buildBaseInstructions(title, responsibility) {
  return [
    `你是一个独立子 Agent，角色是：${title}。`,
    `你的唯一职责：${responsibility}`,
    '你有独立上下文，请保持聚焦，避免输出与分工无关的内容。',
    '输出语言跟随用户，默认中文。',
  ].join('\n');
}

export function buildMultiAgentPlan(intent) {
  const count = Math.max(2, Math.trunc(Number(intent?.agentCount || 5)) || 5);
  const kind = (intent?.taskKind || 'general').toString();
  const agents = [];
  for (let index = 0; index < count; index += 1) {
    const role = roleForIndex(kind, index);
    const [roleKey, title, responsibility] = role;
    const isFinal = index === count - 1;
    agents.push({
      index,
      key: `agent${index + 1}-${roleKey}`,
      name: `agent${index + 1} · ${title}`,
      title,
      roleKey,
      responsibility,
      prompt: buildChildPrompt(intent, role, index, count),
      baseInstructions: buildBaseInstructions(title, responsibility),
      waitFor: [],
      autoStart: true,
    });
  }
  return {
    groupTitle: `${count} 子 Agent 并行任务`,
    task: intent.task,
    taskKind: kind,
    agents,
  };
}

export function formatMultiAgentLaunchSummary(plan, launchedAgents) {
  const rows = (Array.isArray(launchedAgents) ? launchedAgents : []).map((item, index) => {
    const agent = item?.agent || plan?.agents?.[index] || {};
    const status = agent.autoStart ? 'running' : 'waiting';
    return `- ${agent.name || `agent${index + 1}`}：${agent.responsibility || ''}（${status}）`;
  });
  return [
    `已创建 ${rows.length} 个子 Agent。`,
    '',
    ...rows,
    '',
    '你可以点击左侧任一子 Agent 查看它的独立执行过程。',
  ].join('\n');
}
