const DEFAULT_AGENT_COUNT = 5;
const MIN_AGENT_COUNT = 2;
const MAX_AGENT_COUNT = 8;

const CHINESE_DIGITS = Object.freeze({
  一: 1,
  二: 2,
  两: 2,
  三: 3,
  四: 4,
  五: 5,
  六: 6,
  七: 7,
  八: 8,
});

function parseCountToken(value) {
  const token = (value || '').toString().trim();
  if (!token) return 0;
  if (/^\d+$/.test(token)) return Number.parseInt(token, 10);
  return CHINESE_DIGITS[token] || 0;
}

function clampAgentCount(count) {
  const value = Number.isFinite(Number(count)) ? Math.trunc(Number(count)) : DEFAULT_AGENT_COUNT;
  return Math.max(MIN_AGENT_COUNT, Math.min(MAX_AGENT_COUNT, value || DEFAULT_AGENT_COUNT));
}

function detectExplicitMultiAgentText(text) {
  const raw = (text || '').toString();
  if (!raw.trim()) return null;
  const compact = raw.replace(/\s+/g, '');
  const countToken = '(\\d+|[一二两三四五六七八])';
  const patterns = [
    new RegExp(`(?:拉|开|创建|启动|召唤|叫|派|分配)${countToken}个(?:子)?(?:agent|Agent|代理|智能体)`, 'i'),
    new RegExp(`${countToken}个(?:子)?(?:agent|Agent|代理|智能体).{0,8}(?:并行|分工|各自|一起|帮我|协作)`, 'i'),
    new RegExp(`(?:子)?(?:agent|Agent|代理|智能体).{0,8}${countToken}个.{0,8}(?:并行|分工|各自|一起|帮我|协作)`, 'i'),
  ];
  for (const pattern of patterns) {
    const match = compact.match(pattern);
    if (match) return match;
  }
  return null;
}

function inferTaskKind(text) {
  const raw = (text || '').toString().toLowerCase();
  if (/作文|文章|开头|正文|结尾|润色|写作/.test(raw)) return 'writing';
  if (/rag|提示词|prompt|爆款|抖音|文案/.test(raw)) return 'prompt-rag';
  if (/代码|实现|bug|修复|测试|接口|组件|项目|文件|函数/.test(raw)) return 'coding';
  return 'general';
}

export function parseMultiAgentIntent(text) {
  const raw = (text || '').toString().trim();
  if (!raw) return { enabled: false };
  const match = detectExplicitMultiAgentText(raw);
  if (!match) return { enabled: false };
  const count = clampAgentCount(parseCountToken(match[1]));
  return {
    enabled: true,
    agentCount: count,
    task: raw,
    taskKind: inferTaskKind(raw),
    mode: 'parallel-with-synthesis',
  };
}

export const multiAgentIntentLimits = Object.freeze({
  min: MIN_AGENT_COUNT,
  max: MAX_AGENT_COUNT,
  defaultCount: DEFAULT_AGENT_COUNT,
});
