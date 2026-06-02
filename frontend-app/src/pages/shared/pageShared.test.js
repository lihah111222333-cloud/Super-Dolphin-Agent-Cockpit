import { describe, expect, it } from 'vitest';
import { optionalSettingsCwd, textValue, wordListFromText } from './pageShared.js';

describe('pageShared utilities', () => {
  it('normalizes shared page text helpers', () => {
    expect(textValue(' value ')).toBe('value');
    expect(optionalSettingsCwd('selected-project')).toBe('selected-project');
    expect(wordListFromText('alpha, beta gamma')).toEqual(['alpha', 'beta gamma']);
  });
});
