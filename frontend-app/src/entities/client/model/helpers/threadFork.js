import { normalizeOptionalTextField, optionalTextField } from '../contractStoreModel.js';

export const FORK_KICKOFF_PROMPT = '请基于已继承的完整对话历史，简要总结当前进展并提出下一步建议。';

function normalizeString(value) {
  return normalizeOptionalTextField(value);
}

export function buildForkKickoffInput(files = []) {
  return [
    { type: 'text', text: FORK_KICKOFF_PROMPT },
    ...files.map((file) => {
      const path = normalizeString(file?.path);
      const content = optionalTextField(file?.content);
      if (!path || !content.trim()) {
        throw new Error('fork shared file path and content are required');
      }
      return { type: 'filecontent', path, name: path, content };
    }),
  ];
}
