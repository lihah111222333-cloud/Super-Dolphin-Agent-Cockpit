// @ts-nocheck
import { describe, expect, it, vi } from 'vitest';

const markdownMock = vi.hoisted(() => ({ render: vi.fn((text) => `<p>${text}</p>`) }));

vi.mock('./services/log.js', () => ({ logDebug: vi.fn(), logInfo: vi.fn(), logWarn: vi.fn() }));
vi.mock('./services/api.js', () => ({ callAPI: vi.fn() }));
vi.mock('./utils/assistant-markdown.js', () => ({ renderAssistantMarkdown: markdownMock.render, injectSentenceBreaks: vi.fn((text) => text) }));

import { ChatTimeline } from './components/ChatTimeline.js';

function setupTimeline(emit = vi.fn()) {
  return ChatTimeline.setup({
    items: [],
    activeStatus: 'idle',
    activeStatusText: '',
    activeStatusMeta: '',
    pinnedPlanVisible: false,
    pinnedPlanItemId: null,
    resolveThreadDisplayName: null,
    presenceTarget: null,
  }, { emit });
}

describe('ChatTimeline citation chip payloads', () => {
  it('emits skill and conversation citation payloads', () => {
    const emit = vi.fn();
    const vm = setupTimeline(emit);

    const skillNode = {
      getAttribute: vi.fn((name) => ({
        'data-citation-kind': 'skill',
        'data-skill-id': 'deploy-skill',
        'data-skill-name': 'DeploySkill',
        'data-skill-path': 'docs/skills/deploy/SKILL.md',
        'data-conversation-id': '',
      }[name] || '')),
      textContent: 'DeploySkill',
    };

    vm.onAssistantBodyClick({
      target: { closest: vi.fn((selector) => (selector.includes('chat-md-citation') ? skillNode : null)) },
      preventDefault: vi.fn(),
      stopPropagation: vi.fn(),
    });

    expect(emit).toHaveBeenCalledWith('citation-click', {
      kind: 'skill',
      skillId: 'deploy-skill',
      skillName: 'DeploySkill',
      path: 'docs/skills/deploy/SKILL.md',
      conversationId: '',
      raw: 'DeploySkill',
    });

    const conversationNode = {
      getAttribute: vi.fn((name) => ({
        'data-citation-kind': 'conversation',
        'data-skill-id': '',
        'data-skill-name': '',
        'data-skill-path': '',
        'data-conversation-id': 'thread-active',
      }[name] || '')),
      textContent: '@thread-active',
    };

    vm.onAssistantBodyClick({
      target: { closest: vi.fn((selector) => (selector.includes('chat-md-citation') ? conversationNode : null)) },
      preventDefault: vi.fn(),
      stopPropagation: vi.fn(),
    });

    expect(emit).toHaveBeenCalledWith('citation-click', {
      kind: 'conversation',
      skillId: '',
      skillName: '',
      path: '',
      conversationId: 'thread-active',
      raw: '@thread-active',
    });
  });

  it('emits structured payloads for automation and code-comment cards', () => {
    const emit = vi.fn();
    const vm = setupTimeline(emit);

    const automationNode = {
      getAttribute: vi.fn((name) => ({
        'data-citation-kind': 'automation-update',
        'data-comment-title': '',
        'data-automation-name': 'Nightly lint',
        'data-message': 'Workflow rerun completed',
        'data-automation-prompt': 'Run lint on main',
        'data-file-path': '',
        'data-line-start': '0',
        'data-line-end': '0',
      }[name] || '')),
      textContent: 'Nightly lint',
    };

    vm.onAssistantBodyClick({
      target: { closest: vi.fn((selector) => (selector.includes('chat-md-citation') ? automationNode : null)) },
      preventDefault: vi.fn(),
      stopPropagation: vi.fn(),
    });

    expect(emit).toHaveBeenCalledWith('citation-click', {
      kind: 'automation-update',
      title: 'Nightly lint',
      message: 'Workflow rerun completed',
      prompt: 'Run lint on main',
      path: '',
      lineStart: 0,
      lineEnd: 0,
      raw: 'Nightly lint',
    });

    const commentNode = {
      getAttribute: vi.fn((name) => ({
        'data-citation-kind': 'code-comment',
        'data-comment-title': 'Naming',
        'data-automation-name': '',
        'data-message': 'Please rename this',
        'data-automation-prompt': '',
        'data-file-path': 'src/main.go',
        'data-line-start': '9',
        'data-line-end': '11',
      }[name] || '')),
      textContent: 'Naming',
    };

    vm.onAssistantBodyClick({
      target: { closest: vi.fn((selector) => (selector.includes('chat-md-citation') ? commentNode : null)) },
      preventDefault: vi.fn(),
      stopPropagation: vi.fn(),
    });

    expect(emit).toHaveBeenCalledWith('citation-click', {
      kind: 'code-comment',
      title: 'Naming',
      message: 'Please rename this',
      prompt: '',
      path: 'src/main.go',
      lineStart: 9,
      lineEnd: 11,
      raw: 'Naming',
    });
  });
});
