export const EFFORT_MODES = Object.freeze([
  { value: 'xhigh', label: 'xhigh（最强，默认）' },
  { value: 'high', label: 'high' },
  { value: 'medium', label: 'medium' },
  { value: 'low', label: 'low' },
  { value: 'minimal', label: 'minimal' },
  { value: 'none', label: 'none（关闭推理）' },
]);

export const MODEL_OPTIONS = Object.freeze([
  { value: 'gpt-5.4', label: 'gpt-5.4（默认）' },
  { value: 'gpt-5.3-codex', label: 'gpt-5.3-codex' },
  { value: 'gpt-5.2-codex', label: 'gpt-5.2-codex' },
  { value: 'gpt-5.2', label: 'gpt-5.2' },
]);
