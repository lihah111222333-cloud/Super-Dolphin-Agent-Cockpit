import fs from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { describe, expect, it, vi } from 'vitest';
import { createPromptPageService } from './promptPageService.js';

function createApi() {
  return {
    commitPromptIntent: vi.fn().mockResolvedValue({ ok: true }),
    copyTextToClipboard: vi.fn().mockResolvedValue(true),
    deletePrompt: vi.fn().mockResolvedValue({ ok: true }),
    discardPromptIntent: vi.fn().mockResolvedValue({ ok: true }),
    draftPromptIntent: vi.fn().mockResolvedValue({ draftKey: 'intent/expert/review' }),
    dryRunPromptIntent: vi.fn().mockResolvedValue({ would_use: true }),
    getDashboardPrompts: vi.fn().mockResolvedValue({ prompts: [] }),
    getPersonalizationProfile: vi.fn().mockResolvedValue({ profile: {} }),
    getPreference: vi.fn().mockResolvedValue(''),
    getPrompt: vi.fn().mockResolvedValue({ prompt: {} }),
    listPromptAssets: vi.fn().mockResolvedValue({ prompts: [] }),
    savePersonalizationProfile: vi.fn().mockResolvedValue({ profile: {} }),
    setPreference: vi.fn().mockResolvedValue({ ok: true }),
    writePrompt: vi.fn().mockResolvedValue({ ok: true }),
  };
}

describe('promptPageService', () => {
  it('forwards prompt list, detail, update, and preference request shapes', async () => {
    const api = createApi();
    const service = createPromptPageService(api);

    await service.listPromptAssets({ cwd: '/repo/app' });
    await service.getDashboardPrompts({ cwd: '/repo/app' });
    await service.getPrompt({ cwd: '/repo/app', id: 'main/reviewer' });
    await service.writePrompt({
      cwd: '/repo/app',
      id: 'main/reviewer',
      name: '审查提示词',
      description: '审查代码质量',
      agentType: 'coder',
      priority: 5,
      when_to_use: '用户要求代码审查时使用',
      content: '先检查阻塞问题',
      tags: ['review'],
      enabled: true,
      scope: 'project',
    });
    await service.setPreference({ cwd: '/repo/app', key: 'settings.activePromptKey', value: '' });
    await service.getPreference({ cwd: '/repo/app', key: 'settings.activePromptKey' });

    expect(api.listPromptAssets).toHaveBeenCalledWith({ cwd: '/repo/app' });
    expect(api.getDashboardPrompts).toHaveBeenCalledWith({ cwd: '/repo/app' });
    expect(api.getPrompt).toHaveBeenCalledWith({ cwd: '/repo/app', id: 'main/reviewer' });
    expect(api.writePrompt).toHaveBeenCalledWith({
      cwd: '/repo/app',
      id: 'main/reviewer',
      name: '审查提示词',
      description: '审查代码质量',
      agentType: 'coder',
      priority: 5,
      when_to_use: '用户要求代码审查时使用',
      content: '先检查阻塞问题',
      tags: ['review'],
      enabled: true,
      scope: 'project',
    });
    expect(api.setPreference).toHaveBeenCalledWith({ cwd: '/repo/app', key: 'settings.activePromptKey', value: '' });
    expect(api.getPreference).toHaveBeenCalledWith({ cwd: '/repo/app', key: 'settings.activePromptKey' });
  });

  it('forwards prompt intent, profile, clipboard, and deletion request shapes', async () => {
    const api = createApi();
    const service = createPromptPageService(api);

    await service.draftPromptIntent({ cwd: '/repo/app', kind: 'expert', rawInput: 'review', sourceType: 'user_input', scope: 'project' });
    await service.dryRunPromptIntent({ cwd: '/repo/app', draftKey: 'intent/expert/review', kind: 'expert', card: { title: '审查' }, question: '如何审查？' });
    await service.commitPromptIntent({ cwd: '/repo/app', draftKey: 'intent/expert/review', scope: 'project', confirmRisk: true });
    await service.discardPromptIntent({ cwd: '/repo/app', draftKey: 'intent/expert/review' });
    await service.deletePrompt({ cwd: '/repo/app', id: 'main/reviewer', scope: 'project' });
    await service.copyTextToClipboard('完整提示词内容');
    await service.getPersonalizationProfile({ cwd: '/repo/app' });
    await service.savePersonalizationProfile({ cwd: '/repo/app', profile: { role: '架构师' } });

    expect(api.draftPromptIntent).toHaveBeenCalledWith({ cwd: '/repo/app', kind: 'expert', rawInput: 'review', sourceType: 'user_input', scope: 'project' });
    expect(api.dryRunPromptIntent).toHaveBeenCalledWith({ cwd: '/repo/app', draftKey: 'intent/expert/review', kind: 'expert', card: { title: '审查' }, question: '如何审查？' });
    expect(api.commitPromptIntent).toHaveBeenCalledWith({ cwd: '/repo/app', draftKey: 'intent/expert/review', scope: 'project', confirmRisk: true });
    expect(api.discardPromptIntent).toHaveBeenCalledWith({ cwd: '/repo/app', draftKey: 'intent/expert/review' });
    expect(api.deletePrompt).toHaveBeenCalledWith({ cwd: '/repo/app', id: 'main/reviewer', scope: 'project' });
    expect(api.copyTextToClipboard).toHaveBeenCalledWith('完整提示词内容');
    expect(api.getPersonalizationProfile).toHaveBeenCalledWith({ cwd: '/repo/app' });
    expect(api.savePersonalizationProfile).toHaveBeenCalledWith({ cwd: '/repo/app', profile: { role: '架构师' } });
  });

  it('throws synchronously before API calls when cwd is blank', () => {
    const api = createApi();
    const service = createPromptPageService(api);

    expect(() => service.listPromptAssets({ cwd: ' ' })).toThrow('cwd is required');
    expect(api.listPromptAssets).not.toHaveBeenCalled();
    expect(() => service.getDashboardPrompts({ cwd: '' })).toThrow('cwd is required');
    expect(api.getDashboardPrompts).not.toHaveBeenCalled();
  });

  it('throws synchronously before prompt id/key API calls when identifiers are blank', () => {
    const api = createApi();
    const service = createPromptPageService(api);

    expect(() => service.getPrompt({ cwd: '/repo/app', id: '' })).toThrow('id is required');
    expect(api.getPrompt).not.toHaveBeenCalled();
    expect(() => service.writePrompt({ cwd: '/repo/app', id: ' ', name: 'empty id' })).toThrow('id is required');
    expect(api.writePrompt).not.toHaveBeenCalled();
    expect(() => service.deletePrompt({ cwd: '/repo/app', id: ' ' })).toThrow('id is required');
    expect(api.deletePrompt).not.toHaveBeenCalled();
  });

  it('throws synchronously before preference API calls when key or cwd is blank', () => {
    const api = createApi();
    const service = createPromptPageService(api);

    expect(() => service.getPreference({ cwd: '/repo/app', key: '' })).toThrow('key is required');
    expect(api.getPreference).not.toHaveBeenCalled();
    expect(() => service.setPreference({ cwd: '', key: 'settings.activePromptKey', value: '' })).toThrow('cwd is required');
    expect(api.setPreference).not.toHaveBeenCalled();
    expect(() => service.setPreference({ cwd: '/repo/app', key: ' ', value: '' })).toThrow('key is required');
    expect(api.setPreference).not.toHaveBeenCalled();
  });

  it('throws synchronously before prompt intent API calls when required fields are blank', () => {
    const api = createApi();
    const service = createPromptPageService(api);

    expect(() => service.draftPromptIntent({ cwd: '/repo/app', kind: 'expert', rawInput: ' ' })).toThrow('rawInput is required');
    expect(api.draftPromptIntent).not.toHaveBeenCalled();
    expect(() => service.draftPromptIntent({ cwd: '/repo/app', kind: '', rawInput: 'review' })).toThrow('kind is required');
    expect(api.draftPromptIntent).not.toHaveBeenCalled();
    expect(() => service.dryRunPromptIntent({ cwd: '/repo/app', draftKey: '', question: '如何审查？' })).toThrow('draftKey is required');
    expect(api.dryRunPromptIntent).not.toHaveBeenCalled();
    expect(() => service.dryRunPromptIntent({ cwd: '/repo/app', draftKey: 'intent/expert/review', question: ' ' })).toThrow('question is required');
    expect(api.dryRunPromptIntent).not.toHaveBeenCalled();
    expect(() => service.commitPromptIntent({ cwd: '/repo/app', draftKey: '' })).toThrow('draftKey is required');
    expect(api.commitPromptIntent).not.toHaveBeenCalled();
    expect(() => service.discardPromptIntent({ cwd: '/repo/app', draftKey: ' ' })).toThrow('draftKey is required');
    expect(api.discardPromptIntent).not.toHaveBeenCalled();
  });

  it('throws synchronously before profile and clipboard API calls when data is invalid', () => {
    const api = createApi();
    const service = createPromptPageService(api);

    expect(() => service.getPersonalizationProfile({ cwd: '' })).toThrow('cwd is required');
    expect(api.getPersonalizationProfile).not.toHaveBeenCalled();
    expect(() => service.savePersonalizationProfile({ cwd: '/repo/app', profile: null })).toThrow('profile is required');
    expect(api.savePersonalizationProfile).not.toHaveBeenCalled();
    expect(() => service.savePersonalizationProfile({ cwd: '/repo/app', profile: [] })).toThrow('profile is required');
    expect(api.savePersonalizationProfile).not.toHaveBeenCalled();
    expect(() => service.copyTextToClipboard(' ')).toThrow('text is required');
    expect(api.copyTextToClipboard).not.toHaveBeenCalled();
  });

  it('keeps PromptPageView behind promptPageService instead of raw backend API', () => {
    const sourceRoot = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '../../..');
    const source = fs.readFileSync(path.join(sourceRoot, 'features/prompts/PromptPageView.jsx'), 'utf8');

    expect(source).toContain("from '../../pages/prompts/services/promptPageService.js'");
    expect(source).not.toContain('shared/api/backendApi.js');
  });
});
