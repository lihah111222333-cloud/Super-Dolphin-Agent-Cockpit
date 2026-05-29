// @ts-nocheck
import { beforeEach, describe, expect, it, vi } from 'vitest';

const apiMock = vi.hoisted(() => ({
  callAPI: vi.fn(),
}));

vi.mock('./services/api.js', () => ({
  callAPI: apiMock.callAPI,
}));

import {
  applySkillResolution,
  importSkills,
  listSkillResolutions,
  previewSkillResolution,
  writeSkill,
} from './services/skills-api.js';
import * as skillsApi from './services/skills-api.js';

beforeEach(() => {
  apiMock.callAPI.mockReset().mockResolvedValue({});
});

describe('skills-api resolution wrappers', () => {
  it('does not expose the legacy skill candidate RPC wrappers', () => {
    expect(Object.keys(skillsApi).filter((key) => key.toLowerCase().includes('candidate'))).toEqual([]);
    expect(skillsApi.listSkills).toBeUndefined();
    expect(skillsApi.previewSkillMatches).toBeUndefined();
  });

  it('writes personal skills with personal_type', async () => {
    await writeSkill('/repo', 'DocsSkill', '# docs', 'personal', 'user');

    expect(apiMock.callAPI).toHaveBeenCalledWith('skills/local/write', {
      cwd: '/repo',
      path: 'DocsSkill',
      content: '# docs',
      scope: 'personal',
      personal_type: 'user',
    });
  });

  it('imports personal skills with personal_type', async () => {
    await importSkills('/repo', ['/imports/DocsSkill'], 'personal', 'imported');

    expect(apiMock.callAPI).toHaveBeenCalledWith('skills/local/importDir', {
      cwd: '/repo',
      paths: ['/imports/DocsSkill'],
      scope: 'personal',
      personal_type: 'imported',
    });
  });

  it('lists skill resolution conflicts with the strict backend payload', async () => {
    apiMock.callAPI.mockResolvedValueOnce({ items: [{ conflict_id: 'c1' }] });

    await expect(listSkillResolutions('/repo', 25)).resolves.toEqual([{ conflict_id: 'c1' }]);

    expect(apiMock.callAPI).toHaveBeenCalledWith('skills/resolution_list', {
      cwd: '/repo',
    });
  });

  it('keeps a legacy conflicts fallback for resolution list responses', async () => {
    apiMock.callAPI.mockResolvedValueOnce({ conflicts: [{ conflict_id: 'legacy' }] });

    await expect(listSkillResolutions('/repo', 10)).resolves.toEqual([{ conflict_id: 'legacy' }]);
  });

  it('previews a skill resolution with backend snake_case fields', async () => {
    await previewSkillResolution({
      cwd: '/repo',
      conflictId: 'conflict-1',
      name: 'drift',
      scope: 'project',
      provider: 'codex',
      sourceProvider: 'claude',
      sourcePathId: 'provider:claude',
      newName: 'drift-copy',
      keepSourceID: 'project/drift',
      mergeContentHash: 'sha256:merged',
      disablePolicyTarget: 'personal/user/drift',
      action: 'keep_project',
    });

    expect(apiMock.callAPI).toHaveBeenCalledWith('skills/resolution_preview', {
      cwd: '/repo',
      conflict_id: 'conflict-1',
      name: 'drift',
      scope: 'project',
      provider: 'codex',
      source_provider: 'claude',
      source_path_id: 'provider:claude',
      new_name: 'drift-copy',
      keep_source_id: 'project/drift',
      merge_content_hash: 'sha256:merged',
      disable_policy_target: 'personal/user/drift',
      action: 'keep_project',
    });
  });

  it('applies a skill resolution with preview id, hash and action fields', async () => {
    await applySkillResolution({
      cwd: '/repo',
      conflictId: 'conflict-1',
      name: 'drift',
      scope: 'project',
      provider: 'codex',
      sourceProvider: 'claude',
      sourcePathId: 'provider:claude',
      newName: 'drift-copy',
      previewId: 'preview-1',
      previewHash: 'hash-1',
      action: 'apply_preview',
    });

    expect(apiMock.callAPI).toHaveBeenCalledWith('skills/resolution_apply', {
      cwd: '/repo',
      conflict_id: 'conflict-1',
      name: 'drift',
      scope: 'project',
      provider: 'codex',
      source_provider: 'claude',
      source_path_id: 'provider:claude',
      new_name: 'drift-copy',
      preview_id: 'preview-1',
      preview_hash: 'hash-1',
      action: 'apply_preview',
    });
  });
});
