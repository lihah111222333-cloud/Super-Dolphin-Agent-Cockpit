import { describe, expect, it } from 'vitest';
import './SkillsPageTestSupport.jsx';
import { SkillsPage } from './SkillsPage.jsx';

describe('SkillsPage module', () => {
  it('exports the skills page component', () => {
    expect(SkillsPage).toBeTypeOf('function');
  });
});
