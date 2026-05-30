// @ts-nocheck

export async function resolveBuiltinToolLaunchPolicy(callAPI, cwd) {
  try {
    const res = await callAPI('config/builtinTools/read', { cwd });
    if (!Array.isArray(res?.tools)) return null;
    const policy = { claudeAllowedTools: [], codexDisabledNativeTools: [] };
    for (const tool of res.tools) {
      const id = typeof tool?.id === 'string' ? tool.id.trim() : '';
      if (!id) continue;
      const provider = (tool.provider || 'claude').toString();
      if (provider === 'claude' && tool.enabled === true && !tool.replacedBy) policy.claudeAllowedTools.push(id);
      if (provider === 'codex' && (tool.enabled !== true || !!tool.replacedBy)) policy.codexDisabledNativeTools.push(id);
    }
    return policy;
  } catch {
    return null;
  }
}
