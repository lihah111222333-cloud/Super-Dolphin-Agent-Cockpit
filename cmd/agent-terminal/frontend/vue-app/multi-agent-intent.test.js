import { describe, expect, it } from 'vitest';
import { parseMultiAgentIntent } from './utils/multi-agent-intent.js';
import { buildMultiAgentPlan } from './utils/multi-agent-plan.js';

describe('multi agent intent', () => {
  it('parses explicit numeric child agent requests', () => {
    const intent = parseMultiAgentIntent('拉5个子agent出来帮我工作，分工明确');
    expect(intent.enabled).toBe(true);
    expect(intent.agentCount).toBe(5);
    expect(intent.mode).toBe('parallel-with-synthesis');
  });

  it('parses chinese number child agent requests', () => {
    const intent = parseMultiAgentIntent('开五个子代理并行帮我写100字作文');
    expect(intent.enabled).toBe(true);
    expect(intent.agentCount).toBe(5);
    expect(intent.taskKind).toBe('writing');
  });

  it('does not trigger on normal chat', () => {
    expect(parseMultiAgentIntent('帮我解释一下这个功能').enabled).toBe(false);
  });
});

describe('multi agent plan', () => {
  it('builds writing roles and starts the synthesis agent with a usable summary task', () => {
    const plan = buildMultiAgentPlan({ agentCount: 5, taskKind: 'writing', task: '开5个子agent写作文' });
    expect(plan.agents).toHaveLength(5);
    expect(plan.agents[0].name).toContain('构思');
    expect(plan.agents[4].autoStart).toBe(true);
    expect(plan.agents[4].waitFor).toEqual([]);
    expect(plan.agents[4].prompt).toContain('初版汇总');
  });
});
