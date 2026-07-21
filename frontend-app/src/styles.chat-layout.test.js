import { describe, expect, it } from 'vitest';
import {
  css,
  declarationsFor,
  topLevelDeclarationsFor,
  mediaDeclarationsFor,
  mediaDeclarationFor,
} from './styles.test.fixture.js';
describe('theme-aware component styles', () => {
  describe('suiyuan design tokens', () => {
    it('maps the light theme to exported DESIGN.md tokens', () => {
      const light = declarationsFor('.sa-window[data-theme="light"]');
      const lightSpecific = declarationsFor('.sa-window[data-theme="light"].sa-window');

      expect(light['--bg']).toBe('#fbf9f3');
      expect(light['--bg-elevated']).toBe('#fbf9f3');
      expect(light['--surface']).toBe('#ffffff');
      expect(light['--surface-2']).toBe('#f5f4ed');
      expect(light['--surface-3']).toBe('#f0eee7');
      expect(light['--primary']).toBe('#a03b00');
      expect(light['--primary-2']).toBe('#792b00');
      expect(light['--text-pri']).toBe('#1b1c18');
      expect(light['--text-sec']).toBe('#584238');
      expect(light['--text-muted']).toBe('#8b7268');
      expect(lightSpecific['--bg']).toBe('#fbf9f3');
      expect(lightSpecific['--bg-elevated']).toBe('#fbf9f3');
      expect(lightSpecific['--surface-2']).toBe('#f5f4ed');
      expect(lightSpecific['--surface-3']).toBe('#f0eee7');
      expect(lightSpecific['--text-sec']).toBe('#584238');
    });

    it('keeps the Suiyuan workbench aliases available for shell and chat surfaces', () => {
      const rootTokens = declarationsFor(':root');

      expect(rootTokens['--suiyuan-sidebar-width']).toBe('280px');
      expect(rootTokens['--suiyuan-content-max-width']).toBe('1100px');
      expect(rootTokens['--suiyuan-gutter']).toBe('24px');
      expect(rootTokens['--suiyuan-card-shadow']).toBe('0 20px 40px -10px rgba(0, 0, 0, 0.05)');
      expect(rootTokens['--suiyuan-input-shadow']).toBe('0 8px 30px rgba(0, 0, 0, 0.04)');
      expect(rootTokens['--suiyuan-input-highlight']).toBe('inset 0 1px 0 rgba(255, 255, 255, 0.82)');
    });
  });

  it('uses theme-aware colors for skill filter active buttons', () => {
    const active = declarationsFor('.skill-filter .active');

    expect(active.background).toBe('color-mix(in srgb, var(--accent-2) 16%, var(--surface-3))');
    expect(active.color).toBe('var(--text-pri)');
    expect(active['border-color']).toBe('var(--border-strong)');
    expect(active.background).not.toBe('#4d4f55');
  });

  it('keeps skill editor controls on theme token surfaces', () => {
    const modalButton = declarationsFor('.skills-editor-modal button');
    const scopeButton = declarationsFor('.skills-scope-segmented button');
    const activeScopeButton = declarationsFor('.skills-scope-segmented button.active');
    const bodyPreview = declarationsFor('.skills-body-preview');
    const bodyPreviewHeading = declarationsFor('.skills-body-preview h3');

    expect(modalButton.background).toBe('var(--surface-2)');
    expect(modalButton.color).toBe('var(--text-pri)');
    expect(scopeButton.background).toBe('var(--surface-2)');
    expect(scopeButton.color).toBe('var(--text-sec)');
    expect(activeScopeButton.background).toBe('color-mix(in srgb, var(--success) 12%, var(--surface))');
    expect(activeScopeButton.color).toBe('var(--success)');
    expect(bodyPreview.background).toBe('var(--surface-2)');
    expect(bodyPreview.color).toBe('var(--text-sec)');
    expect(bodyPreviewHeading.color).toBe('var(--text-pri)');
  });

  it('shows a readable generated-image fallback instead of a bare broken image icon', () => {
    const fallback = declarationsFor('.message-image-fallback');
    const fallbackCode = declarationsFor('.message-image-fallback code');

    expect(fallback.display).toBe('inline-grid');
    expect(fallback.background).toBe('var(--surface-2)');
    expect(fallback.color).toBe('var(--text-sec)');
    expect(fallbackCode.overflow).toBe('hidden');
    expect(fallbackCode['text-overflow']).toBe('ellipsis');
  });

  it('styles generated image previews with an enlarge affordance', () => {
    const preview = declarationsFor('.message-image-preview');
    const hint = declarationsFor('.message-image-preview span');
    const lightbox = declarationsFor('.image-lightbox');
    const hostLightbox = declarationsFor('#overlay-root .image-lightbox');
    const panel = declarationsFor('.image-lightbox-panel');

    expect(preview.cursor).toBe('zoom-in');
    expect(preview.background).toBe('transparent');
    expect(hint.opacity).toBe('0');
    expect(lightbox.position).toBe('fixed');
    expect(lightbox['z-index']).toBeUndefined();
    expect(hostLightbox['z-index']).toBe('var(--z-overlay-lightbox)');
    expect(panel.width).toBe('min(1180px, 94vw)');
    expect(panel['max-height']).toBe('92vh');
  });

  it('styles mermaid diagrams as bounded readable timeline content', () => {
    const diagram = declarationsFor('.mermaid-diagram');
    const preview = declarationsFor('.mermaid-diagram-preview');
    const hint = declarationsFor('.mermaid-diagram-preview span');
    const lightboxSvg = declarationsFor('.mermaid-lightbox-svg');
    const image = declarationsFor('.mermaid-diagram img');

    expect(diagram['max-width']).toBe('100%');
    expect(diagram.overflow).toBe('auto');
    expect(diagram.background).toBe('var(--surface)');
    expect(preview.cursor).toBe('zoom-in');
    expect(preview.background).toBe('transparent');
    expect(hint.opacity).toBe('0');
    expect(lightboxSvg.overflow).toBe('auto');
    expect(image.display).toBe('block');
    expect(image['max-width']).toBe('100%');
  });

  it('keeps shared file rows compact while the preview modal can scroll content', () => {
    const rowTitle = declarationsFor('.file-row h3');
    const badge = declarationsFor('.file-row header span');
    const summary = declarationsFor('.file-row .shared-file-summary');
    const preview = declarationsFor('.shared-file-content-preview');

    expect(rowTitle['min-width']).toBe('0');
    expect(rowTitle.overflow).toBe('hidden');
    expect(rowTitle['text-overflow']).toBe('ellipsis');
    expect(badge.flex).toBe('0 0 auto');
    expect(summary['max-height']).toBe('calc(1.45em * 3)');
    expect(summary.overflow).toBe('hidden');
    expect(preview['max-height']).toBe('52vh');
    expect(preview.overflow).toBe('auto');
  });
});

describe('conversation grid styles', () => {
  it('does not override the computed chat grid at medium widths', () => {
    const mediumChatLayouts = mediaDeclarationsFor('(max-width: 1280px)', '.chat-layout');
    const mediumRuntimePanels = mediaDeclarationsFor('(max-width: 1280px)', '.runtime-panel');
    const mediumRightSplitters = mediaDeclarationsFor('(max-width: 1280px)', '.splitter--right');

    for (const declarations of mediumChatLayouts) {
      expect(declarations['grid-template-columns']).toBeUndefined();
    }
    for (const declarations of [...mediumRuntimePanels, ...mediumRightSplitters]) {
      expect(declarations.display).not.toBe('none');
    }
    expect(css).not.toContain('280px minmax(0, 1fr) !important');
    expect(css).not.toContain('.chat-layout {\n    grid-template-columns');
  });

  it('prevents long chat content from widening the conversation grid', () => {
    const conversation = declarationsFor('.conversation');
    const timelineShell = declarationsFor('.timeline-shell');
    const timeline = declarationsFor('.timeline');
    const message = declarationsFor('.message');
    const bubble = declarationsFor('.bubble');
    const composer = declarationsFor('.composer');

    expect(conversation['min-width']).toBe('0');
    expect(conversation.overflow).toBe('hidden');
    expect(timelineShell.position).toBe('relative');
    expect(timelineShell['min-width']).toBe('0');
    expect(timelineShell['min-height']).toBe('0');
    expect(timelineShell.overflow).toBe('hidden');
    expect(timeline['min-width']).toBe('0');
    expect(timeline.height).toBe('100%');
    expect(timeline['max-width']).toBe('100%');
    expect(message['min-width']).toBe('0');
    expect(bubble['min-width']).toBe('0');
    expect(bubble['max-width']).toBe('100%');
    expect(composer['min-width']).toBe('0');
    expect(composer['max-width']).toBe('100%');
  });
});

describe('suiyuan chat canvas', () => {
  it('centers the chat canvas with unframed assistant responses and compact user bubbles', () => {
    const conversation = declarationsFor('.conversation');
    const activeConversation = declarationsFor('.conversation:not(.conversation--intro)');
    const timeline = declarationsFor('.timeline');
    const message = declarationsFor('.message');
    const assistantMessage = declarationsFor('.message.assistant');
    const userMessage = declarationsFor('.message.user');
    const assistantBubble = declarationsFor('.message.assistant .bubble');
    const userBubble = declarationsFor('.message.user .bubble');
    const markdownPre = declarationsFor('.message-markdown pre');

    expect(conversation.background).toBe('var(--bg)');
    expect(activeConversation['grid-template-rows']).toBe('auto minmax(0, 1fr) auto');
    expect(timeline['align-items']).toBe('center');
    expect(message.border).toBe('0');
    expect(message.background).toBe('transparent');
    expect(message['box-shadow']).toBe('none');
    expect(assistantMessage.background).toBe('transparent');
    expect(assistantMessage['box-shadow']).toBe('none');
    expect(assistantBubble.width).toBe('100%');
    expect(assistantBubble['max-width']).toBe('min(840px, 100%)');
    expect(userMessage.background).toBe('transparent');
    expect(userBubble.width).toBe('fit-content');
    expect(userBubble.background).toBe('var(--surface-3)');
    expect(markdownPre.background).toBe('var(--surface-code)');
  });
});

describe('suiyuan responsive chat workbench', () => {
  it('collapses side surfaces before the message canvas becomes unreadable', () => {
    const narrowConversation = mediaDeclarationFor('(max-width: 760px)', '.conversation', 'border-right');
    const narrowFloatingComposer = mediaDeclarationFor('(max-width: 760px)', '.composer--floating', 'width');
    const narrowTimeline = mediaDeclarationFor('(max-width: 760px)', '.timeline', 'padding-bottom');

    expect(narrowConversation['border-right']).toBe('0');
    expect(narrowFloatingComposer.width).toBe('calc(100% - 24px)');
    expect(narrowTimeline['padding-bottom']).toBe('clamp(104px, 18vh, 156px)');
  });
});

describe('conversation content column styles', () => {
  it('keeps timeline messages centered while the docked composer fills the footer frame', () => {
    const conversation = declarationsFor('.conversation');
    const activeConversation = declarationsFor('.conversation:not(.conversation--intro)');
    const activeTimelineShell = declarationsFor('.conversation:not(.conversation--intro) .timeline-shell');
    const activeTimeline = declarationsFor('.conversation:not(.conversation--intro) .timeline');
    const timeline = declarationsFor('.timeline');
    const message = declarationsFor('.message');
    const userMessage = declarationsFor('.message.user');
    const userBubble = declarationsFor('.message.user .bubble');
    const composer = declarationsFor('.composer');
    const dockedComposer = declarationsFor('.composer.composer--docked');
    const activeDockedComposer = declarationsFor('.conversation:not(.conversation--intro) .composer.composer--docked');
    const dockedComposerCard = declarationsFor('.composer--docked .composer-card');
    const composerTextarea = declarationsFor('.composer textarea');
    const composerMeta = declarationsFor('.composer-meta');
    const composerAttach = declarationsFor('.composer-attach');
    const composerSend = declarationsFor('.composer .send');
    const scrollButton = declarationsFor('.chat-scroll-bottom-btn');
    const headerTools = declarationsFor('.chat-header-tools');
    const headerTool = declarationsFor('.chat-header-tool');
    const disabledHeaderTool = declarationsFor('.chat-header-tool:disabled');

    expect(conversation['--conversation-content-width']).toBe('min(var(--suiyuan-content-max-width), max(0px, calc(100% - clamp(32px, 7vw, 112px))))');
    expect(activeConversation.display).toBe('grid');
    expect(activeConversation['grid-template-rows']).toBe('auto minmax(0, 1fr) auto');
    expect(activeConversation.overflow).toBe('hidden');
    expect(activeTimelineShell['min-height']).toBe('0');
    expect(activeTimelineShell.overflow).toBe('hidden');
    expect(activeTimeline.height).toBe('100%');
    expect(activeTimeline['overflow-y']).toBe('auto');
    expect(headerTools.display).toBe('inline-flex');
    expect(headerTools['align-items']).toBe('center');
    expect(headerTools.gap).toBe('22px');
    expect(headerTools['margin-top']).toBe('-6px');
    expect(headerTool.width).toBe('32px');
    expect(headerTool.height).toBe('32px');
    expect(headerTool.background).toBe('transparent');
    expect(headerTool.color).toBe('var(--text-sec)');
    expect(disabledHeaderTool.opacity).toBe('1');
    expect(timeline.display).toBe('flex');
    expect(timeline['flex-direction']).toBe('column');
    expect(timeline['align-items']).toBe('center');
    expect(message.width).toBe('var(--conversation-content-width)');
    expect(message.margin).toBe('12px auto');
    expect(userMessage['margin-left']).toBeUndefined();
    expect(userMessage.width).toBe('var(--conversation-content-width)');
    expect(userBubble['margin-left']).toBe('auto');
    expect(userBubble['max-width']).toBe('min(720px, 78%)');
    expect(userBubble.background).toBe('var(--surface-3)');
    expect(userBubble.color).toBe('var(--text-pri)');
    expect(composer.width).toBe('min(900px, max(0px, calc(100% - clamp(24px, 6vw, 96px))))');
    expect(dockedComposer.padding).toBe('0');
    expect(dockedComposer['border-top']).toBe('0');
    expect(dockedComposer.background).toBe('transparent');
    expect(activeDockedComposer.padding).toBe('14px 0 18px');
    expect(activeDockedComposer['border-top']).toBe('0');
    expect(activeDockedComposer.background).toBe('transparent');
    expect(dockedComposerCard.width).toBe('100%');
    expect(dockedComposerCard.margin).toBe('0 auto');
    expect(dockedComposerCard.border).toBe('1px solid color-mix(in srgb, var(--border) 88%, var(--surface))');
    expect(dockedComposerCard['border-radius']).toBe('20px');
    expect(dockedComposerCard['box-shadow']).toBe('var(--composer-shadow)');
    expect(composerTextarea.height).toBe('76px');
    expect(composerTextarea['min-height']).toBe('76px');
    expect(composerMeta['min-height']).toBe('48px');
    expect(composerMeta['flex-wrap']).toBe('nowrap');
    expect(composerAttach.flex).toBe('0 0 36px');
    expect(composerAttach.width).toBe('36px');
    expect(composerAttach['min-width']).toBe('36px');
    expect(composerAttach.height).toBe('36px');
    expect(composerAttach.padding).toBe('0');
    expect(composerSend.flex).toBe('0 0 40px');
    expect(composerSend.width).toBe('40px');
    expect(composerSend['min-width']).toBe('40px');
    expect(composerSend.height).toBe('40px');
    expect(scrollButton.position).toBe('absolute');
    expect(scrollButton.right).toBe('max(18px, var(--conversation-content-right-gutter))');
    expect(scrollButton.bottom).toBe('18px');
    expect(scrollButton.width).toBe('32px');
    expect(scrollButton.height).toBe('32px');
  });

  it('keeps the new-chat intro stage positioned and full width', () => {
    const introConversation = declarationsFor('.conversation--intro');
    const introTimelineShell = declarationsFor('.conversation--intro .timeline-shell');
    const introTimeline = declarationsFor('.conversation--intro .timeline');
    const stitchIntroTimeline = declarationsFor('.sa-window .chat-page.chat-page--intro .conversation--intro .timeline');
    const introStage = declarationsFor('.conversation--intro .intro-chat-stage');
    const introTitle = topLevelDeclarationsFor('.empty-chat h2');
    const scopedIntroTitle = declarationsFor('.conversation--intro .empty-chat h2');
    const introCopy = topLevelDeclarationsFor('.empty-chat p');
    const floatingComposer = declarationsFor('.conversation--intro .composer--floating');
    const stitchScopedFloatingComposer = declarationsFor('.sa-window .chat-page.chat-page--intro .conversation--intro .composer--floating');
    const floatingCard = declarationsFor('.composer--floating .composer-card');

    expect(introConversation['--conversation-intro-width']).toBe('min(920px, max(0px, calc(100% - clamp(24px, 6vw, 88px))))');
    expect(introConversation['--conversation-composer-width']).toBe('min(820px, 100%)');
    expect(introConversation['--conversation-content-width']).toBe('var(--conversation-intro-width)');
    expect(introConversation['--conversation-content-left-nudge']).toBe('0px');
    expect(introTimelineShell.display).toBe('block');
    expect(introTimelineShell.width).toBe('100%');
    expect(introTimelineShell.height).toBe('100%');
    expect(introTimeline.display).toBe('flex');
    expect(introTimeline.width).toBe('100%');
    expect(introTimeline.height).toBe('100%');
    expect(introTimeline['align-items']).toBe('center');
    expect(introTimeline['justify-content']).toBe('flex-start');
    expect(introTimeline.padding).toBe('0');
    expect(stitchIntroTimeline.transform).toBe('none');
    expect(stitchIntroTimeline['-webkit-transform']).toBe('none');
    expect(introStage.width).toBe('min(100%, var(--conversation-intro-width))');
    expect(introStage['max-width']).toBe('100%');
    expect(introStage.display).toBe('flex');
    expect(introStage['justify-content']).toBe('flex-start');
    expect(introStage['min-height']).toBe('0');
    expect(introStage.gap).toBe('clamp(22px, 3.8vh, 38px)');
    expect(introStage['padding-top']).toBe('clamp(116px, 30vh, 340px)');
    expect(introTitle['font-size']).toBe('clamp(30px, 2.75rem, 48px)');
    expect(introTitle['font-weight']).toBe('800');
    expect(introTitle['white-space']).toBe('normal');
    expect(introTitle['overflow-wrap']).toBe('anywhere');
    expect(scopedIntroTitle.margin).toBe('0 auto');
    expect(scopedIntroTitle.width).toBe('min(100%, max-content)');
    expect(scopedIntroTitle['max-width']).toBe('100%');
    expect(scopedIntroTitle.transform).toBe('translateX(clamp(-28px, -2.2vw, 0px))');
    expect(introCopy.display).toBe('none');
    expect(floatingComposer.width).toBe('var(--conversation-composer-width)');
    expect(floatingComposer['max-width']).toBe('100%');
    expect(floatingComposer.padding).toBe('0');
    expect(floatingComposer.background).toBe('transparent');
    expect(stitchScopedFloatingComposer.position).toBeUndefined();
    expect(stitchScopedFloatingComposer.width).toBeUndefined();
    expect(stitchScopedFloatingComposer['max-width']).toBeUndefined();
    expect(stitchScopedFloatingComposer['pointer-events']).toBeUndefined();
    expect(stitchScopedFloatingComposer['z-index']).toBe('var(--z-local-sticky)');
    expect(floatingCard.width).toBe('100%');
    expect(floatingCard.margin).toBe('0 auto');
  });

  it('keeps the light new-chat composer on the dark-mode geometry', () => {
    const floating = topLevelDeclarationsFor('.composer--floating');
    const sharedCard = topLevelDeclarationsFor('.composer.composer--floating[data-file-drop-target] .composer-card');
    const sharedTextarea = topLevelDeclarationsFor('.composer.composer--floating[data-file-drop-target] textarea');
    const sharedMeta = topLevelDeclarationsFor('.composer.composer--floating[data-file-drop-target] .composer-meta');
    const sharedAttach = topLevelDeclarationsFor('.composer.composer--floating[data-file-drop-target] .composer-attach');
    const sharedContext = topLevelDeclarationsFor('.composer.composer--floating[data-file-drop-target] .composer-context');
    const sharedModel = topLevelDeclarationsFor('.composer.composer--floating[data-file-drop-target] .composer-model');
    const lightCard = declarationsFor('.sa-window[data-theme="light"] .conversation--intro .composer--floating .composer-card');
    const lightAttach = declarationsFor('.sa-window[data-theme="light"] .conversation--intro .composer--floating .composer-attach');
    const lightTextarea = declarationsFor('.sa-window[data-theme="light"] .conversation--intro .composer.composer--floating[data-file-drop-target] textarea');
    const lightMeta = declarationsFor('.sa-window[data-theme="light"] .conversation--intro .composer.composer--floating[data-file-drop-target] .composer-meta');
    const lightContext = declarationsFor('.sa-window[data-theme="light"] .conversation--intro .composer.composer--floating[data-file-drop-target] .composer-context');
    const lightModel = declarationsFor('.sa-window[data-theme="light"] .conversation--intro .composer.composer--floating[data-file-drop-target] .composer-model');
    const disabledSend = declarationsFor('.sa-window[data-theme="light"] .conversation--intro .composer.composer--floating[data-file-drop-target] .send:disabled');
    const disclaimer = declarationsFor('.sa-window[data-theme="light"] .conversation--intro .composer-disclaimer');
    const track = declarationsFor('.sa-window[data-theme="light"] .conversation--intro .composer--floating .provider-track');
    const darkFloating = topLevelDeclarationsFor('.sa-window[data-theme="dark"] .composer.composer--floating[data-file-drop-target]');
    const darkCard = topLevelDeclarationsFor('.sa-window[data-theme="dark"] .composer.composer--floating[data-file-drop-target] .composer-card');
    const darkTextarea = topLevelDeclarationsFor('.sa-window[data-theme="dark"] .composer.composer--floating[data-file-drop-target] textarea');

    expect(floating['--composer-floating-max-width']).toBe('800px');
    expect(floating['--composer-floating-bottom-gap']).toBe('22px');
    expect(sharedCard.padding).toBe('0');
    expect(sharedCard['border-radius']).toBe('var(--suiyuan-radius-input)');
    expect(sharedTextarea.height).toBe('76px');
    expect(sharedTextarea['min-height']).toBe('76px');
    expect(sharedMeta['min-height']).toBe('48px');
    expect(sharedMeta['flex-wrap']).toBe('nowrap');
    expect(sharedAttach.flex).toBe('0 0 36px');
    expect(sharedAttach.width).toBe('36px');
    expect(sharedAttach['min-width']).toBe('36px');
    expect(sharedAttach.height).toBe('36px');
    expect(sharedAttach['min-height']).toBe('36px');
    expect(sharedAttach.padding).toBe('0');
    expect(sharedContext.display).toBeUndefined();
    expect(sharedModel.height).toBe('34px');
    expect(sharedModel['min-height']).toBe('34px');
    for (const property of ['height', 'min-height', 'padding', 'border-radius']) {
      expect(lightTextarea[property]).toBeUndefined();
    }
    for (const property of ['font-size', 'line-height']) {
      expect(lightTextarea[property]).toBeUndefined();
    }
    for (const property of ['height', 'min-height', 'margin-top', 'padding']) {
      expect(lightMeta[property]).toBeUndefined();
    }
    expect(lightContext.display).toBeUndefined();
    for (const property of ['width', 'height', 'min-height', 'padding']) {
      expect(lightAttach[property]).toBeUndefined();
    }
    for (const property of ['min-width', 'height', 'min-height']) {
      expect(lightModel[property]).toBeUndefined();
    }
    expect(darkFloating['--composer-floating-max-width']).toBeUndefined();
    expect(darkFloating['--composer-floating-bottom-gap']).toBeUndefined();
    expect(darkCard.padding).toBeUndefined();
    expect(darkCard['border-radius']).toBeUndefined();
    expect(darkTextarea.height).toBeUndefined();
    expect(darkTextarea['min-height']).toBeUndefined();
    expect(lightCard.background).toBe('var(--surface)');
    expect(lightCard['border-color']).toBe('var(--suiyuan-surface-variant)');
    expect(lightCard['box-shadow']).toBe('var(--suiyuan-input-shadow)');
    expect(lightAttach.background).toBe('var(--suiyuan-surface-low)');
    expect(lightAttach['border-color']).toBe('var(--suiyuan-surface-variant)');
    expect(lightModel.background).toBe('color-mix(in srgb, var(--suiyuan-primary-fixed) 10%, transparent)');
    expect(disabledSend.background).toBe('var(--suiyuan-primary)');
    expect(disabledSend.color).toBe('var(--on-accent)');
    expect(disclaimer.color).toBe('var(--suiyuan-on-surface-variant)');
    expect(disclaimer.margin).toBeUndefined();
    expect(disclaimer['font-weight']).toBeUndefined();
    expect(disclaimer['line-height']).toBeUndefined();
    expect(track.background).toBe('color-mix(in srgb, var(--surface-3) 72%, var(--border))');
  });

  it('keeps the Suiyuan intro and floating composer dark when the shell theme is dark', () => {
    const intro = declarationsFor('.sa-window[data-theme="dark"] .chat-page.chat-page--intro');
    const darkCard = declarationsFor('.sa-window[data-theme="dark"] .chat-intro-card');
    const composerBackdrop = declarationsFor('.sa-window[data-theme="dark"] .composer.composer--floating[data-file-drop-target]');
    const composerCard = declarationsFor('.sa-window[data-theme="dark"] .composer.composer--floating[data-file-drop-target] .composer-card');

    expect(intro.background).toContain('var(--suiyuan-surface-bright)');
    expect(darkCard.background).toContain('var(--suiyuan-surface-low)');
    expect(composerBackdrop.background).toContain('var(--bg)');
    expect(composerCard.background).toContain('var(--suiyuan-surface-low)');
    expect(composerCard['border-color']).toContain('var(--suiyuan-outline-variant)');
  });

  it('keeps light and dark intro geometry structurally isomorphic', () => {
    const spotlight = topLevelDeclarationsFor('.chat-intro-spotlight');
    const spotlightInner = topLevelDeclarationsFor('.chat-intro-spotlight__inner');
    const logoTile = topLevelDeclarationsFor('.chat-intro-logo-tile');
    const introTitle = topLevelDeclarationsFor('.chat-intro-title');
    const introSubtitle = topLevelDeclarationsFor('.chat-intro-subtitle');
    const suggestions = topLevelDeclarationsFor('.chat-intro-suggestions');
    const card = topLevelDeclarationsFor('.chat-intro-card');
    const cardIcon = topLevelDeclarationsFor('.chat-intro-card__icon');
    const cardIconSvg = topLevelDeclarationsFor('.chat-intro-card__icon svg');
    const cardTitle = topLevelDeclarationsFor('.chat-intro-card__title');
    const cardDescription = topLevelDeclarationsFor('.chat-intro-card__description');
    const darkSpotlight = topLevelDeclarationsFor('.sa-window[data-theme="dark"] .chat-intro-spotlight');
    const darkCard = topLevelDeclarationsFor('.sa-window[data-theme="dark"] .chat-intro-card');
    const darkTitle = topLevelDeclarationsFor('.sa-window[data-theme="dark"] .chat-intro-title');
    const darkSuggestions = topLevelDeclarationsFor('.sa-window[data-theme="dark"] .chat-intro-suggestions');
    const darkIcon = topLevelDeclarationsFor('.sa-window[data-theme="dark"] .chat-intro-card__icon');
    const darkCardTitle = topLevelDeclarationsFor('.sa-window[data-theme="dark"] .chat-intro-card__title');
    const darkCardDescription = topLevelDeclarationsFor('.sa-window[data-theme="dark"] .chat-intro-card__description');
    const mobileSpotlight = mediaDeclarationFor('(max-width: 640px)', '.chat-intro-spotlight', 'overflow-y');
    const mobileInner = mediaDeclarationFor('(max-width: 640px)', '.chat-intro-spotlight__inner', 'justify-content');
    const mobileCard = mediaDeclarationFor('(max-width: 640px)', '.chat-intro-card', 'min-height');
    const mobileIcon = mediaDeclarationFor('(max-width: 640px)', '.chat-intro-card__icon', 'width');
    const mobileTitle = mediaDeclarationFor('(max-width: 640px)', '.chat-intro-card__title', 'margin');
    const mobileDescription = mediaDeclarationFor('(max-width: 640px)', '.chat-intro-card__description', 'line-height');

    expect(spotlight.inset).toBe('64px 0 0');
    expect(spotlight.padding).toBe('0 32px');
    expect(spotlightInner.height).toBe('auto');
    expect(logoTile.display).toBe('none');
    expect(introTitle.margin).toBe('0');
    expect(introTitle['font-weight']).toBe('600');
    expect(introSubtitle.display).toBe('none');
    expect(suggestions.width).toBe('min(900px, 100%)');
    expect(suggestions['margin-top']).toBe('48px');
    expect(suggestions['margin-bottom']).toBe('128px');
    expect(card['min-height']).toBe('174px');
    expect(card.gap).toBe('8px');
    expect(card['border-color']).toBe('var(--suiyuan-outline-variant)');
    expect(card.padding).toBe('24px');
    expect(cardIcon.width).toBe('36px');
    expect(cardIcon.height).toBe('36px');
    expect(cardIcon['margin-bottom']).toBe('0');
    expect(cardIconSvg.width).toBe('17px');
    expect(cardIconSvg.height).toBe('17px');
    expect(cardTitle.margin).toBe('8px 0 0');
    expect(cardTitle['font-weight']).toBe('700');
    expect(cardDescription['min-height']).toBe('0');
    expect(cardDescription.color).toBe('var(--suiyuan-on-surface-variant)');
    expect(cardDescription['font-weight']).toBe('400');
    expect(cardDescription['line-height']).toBe('var(--suiyuan-text-body-line)');
    expect(darkSpotlight.inset).toBeUndefined();
    expect(darkSpotlight.padding).toBeUndefined();
    expect(darkTitle['font-weight']).toBeUndefined();
    expect(darkSuggestions.width).toBeUndefined();
    expect(darkSuggestions['margin-top']).toBeUndefined();
    expect(darkSuggestions['margin-bottom']).toBeUndefined();
    expect(darkCard['min-height']).toBeUndefined();
    expect(darkCard.gap).toBeUndefined();
    expect(darkIcon.width).toBeUndefined();
    expect(darkIcon.height).toBeUndefined();
    expect(darkIcon['margin-bottom']).toBeUndefined();
    expect(darkCardTitle.margin).toBeUndefined();
    expect(darkCardTitle['font-weight']).toBeUndefined();
    expect(darkCardDescription['min-height']).toBeUndefined();
    expect(darkCardDescription['font-weight']).toBeUndefined();
    expect(darkCardDescription['line-height']).toBeUndefined();
    expect(css).not.toContain('.composer-attach-label');
    expect(css).not.toContain('content: "附件"');
    expect(mobileSpotlight['overflow-y']).toBe('auto');
    expect(mobileSpotlight.padding).toBe('24px 16px 270px');
    expect(mobileInner['justify-content']).toBe('flex-start');
    expect(mobileCard['min-height']).toBe('92px');
    expect(mobileCard.gap).toBe('5px');
    expect(mobileIcon.width).toBe('34px');
    expect(mobileIcon.height).toBe('34px');
    expect(mobileTitle.margin).toBe('4px 0 0');
    expect(mobileDescription['line-height']).toBe('15px');
  });

  it('keeps the composer toolbar on one row at tablet and mobile widths', () => {
    const tabletMeta = mediaDeclarationFor('(max-width: 920px)', '.sa-window .composer-meta', 'flex-wrap');
    const tabletActions = mediaDeclarationFor('(max-width: 920px)', '.sa-window .composer-actions', 'width');
    const mobileMeta = mediaDeclarationFor('(max-width: 640px)', '.sa-window .composer-meta', 'display');
    const mobileAttach = mediaDeclarationFor('(max-width: 640px)', '.sa-window .composer-attach', 'width');
    const mobileActions = mediaDeclarationFor('(max-width: 640px)', '.sa-window .composer-actions', 'display');

    expect(tabletMeta['flex-wrap']).toBe('nowrap');
    expect(tabletActions.width).toBe('auto');
    expect(mobileMeta.display).toBe('flex');
    expect(mobileMeta['flex-wrap']).toBe('nowrap');
    expect(mobileAttach.width).toBe('36px');
    expect(mobileActions.display).toBe('inline-flex');
  });
});
