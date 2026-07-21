import { describe, expect, it } from 'vitest';
import {
  declarationsFor,
  firstDeclarationsFor,
  topLevelDeclarationsFor,
  containerDeclarationsFor,
} from './styles.test.fixture.js';
describe('composer layout styles', () => {
  it('draws the screenshot separator line between the composer textarea and controls', () => {
    const textarea = declarationsFor('.composer textarea');

    expect(textarea['border-bottom']).toBe('1px solid var(--border)');
  });

  it('keeps the composer textarea in the screenshot-height input area', () => {
    const textarea = declarationsFor('.composer textarea');
    const floatingTextarea = declarationsFor('.composer--floating textarea');
    const activeTimelineShell = declarationsFor('.conversation:not(.conversation--intro) .timeline-shell');
    const activeTimeline = declarationsFor('.conversation:not(.conversation--intro) .timeline');
    const activeComposer = declarationsFor('.conversation:not(.conversation--intro) .composer--docked');

    expect(textarea['line-height']).toBe('1.5');
    expect(textarea.height).toBe('76px');
    expect(textarea['min-height']).toBe('76px');
    expect(floatingTextarea.height).toBe('76px');
    expect(floatingTextarea['min-height']).toBe('76px');
    expect(textarea['max-height']).toBe('calc(1.5em * 8 + 34px)');
    expect(textarea['overflow-y']).toBe('auto');
    expect(activeTimelineShell['grid-row']).toBe('2');
    expect(activeTimeline.padding).toBe('24px 0 clamp(112px, 16vh, 172px)');
    expect(activeComposer['grid-row']).toBe('3');
  });

  it('renders the Suiyuan floating composer as a raised white input object', () => {
    const floatingCard = declarationsFor('.composer--floating .composer-card');
    const textarea = declarationsFor('.composer--floating textarea');
    const meta = declarationsFor('.composer--floating .composer-meta');
    const send = declarationsFor('.composer .send');
    const disabledSend = declarationsFor('.composer .send:disabled');

    expect(floatingCard.background).toBe('var(--surface)');
    expect(floatingCard['border-radius']).toBe('var(--suiyuan-radius-input)');
    expect(floatingCard['box-shadow']).toContain('var(--suiyuan-input-shadow)');
    expect(textarea.padding).toBe('18px 20px 12px');
    expect(meta['min-height']).toBe('48px');
    expect(send.background).toBe('var(--primary-action-bg)');
    expect(disabledSend.background).toBe('var(--surface-2)');
  });

  it('keeps the fixed floating composer aligned when the product nav collapses', () => {
    const expandedFloatingComposer = topLevelDeclarationsFor('.sa-window .composer.composer--floating[data-file-drop-target]');
    const collapsedFloatingComposer = topLevelDeclarationsFor('.sa-window.sidebar-collapsed .composer.composer--floating[data-file-drop-target]');

    expect(expandedFloatingComposer['--composer-fixed-left']).toBe('var(--suiyuan-sidebar-width, 280px)');
    expect(collapsedFloatingComposer['--composer-fixed-left']).toBe('0px');
  });

  it('keeps composer send controls aligned with the shell theme', () => {
    const sendIcon = declarationsFor('.composer .send svg');

    expect(sendIcon.transform).toBe('none');
    expect(sendIcon['transform-origin']).toBe('50% 50%');
  });

  it('keeps composer interrupt controls visually distinct from sending', () => {
    const interruptButton = declarationsFor('.composer .send--interrupt');
    const interruptIcon = declarationsFor('.composer .send--interrupt svg');

    expect(interruptButton.background).toContain('var(--error)');
    expect(interruptIcon.transform).toBe('none');
  });

  it('keeps app workbench navigation and agent list icons on consistent fixed sizes', () => {
    const nav = declarationsFor('.app-sidebar-nav');
    const navButton = declarationsFor('.app-sidebar-nav button');
    const newChat = declarationsFor('.sidebar-new-chat');
    const navIcon = declarationsFor('.app-sidebar-nav button svg');
    const navActive = declarationsFor('.app-sidebar-nav button.active');
    const threadToolIcon = declarationsFor('.thread-tools svg');
    const threadCardIcon = declarationsFor('.thread-card svg');
    const threadPin = declarationsFor('.thread-pin');
    const threadPinHover = declarationsFor('.thread-pin:hover');
    const providerBadge = declarationsFor('.thread-card b');
    const statusLine = declarationsFor('.thread-status-row');
    const statusDot = declarationsFor('.thread-status-dot');
    const hiddenProjectSelector = declarationsFor('.sidebar-project-selector');

    expect(nav.gap).toBe('10px');
    expect(nav['padding-top']).toBe('0');
    expect(navButton.width).toBe('100%');
    expect(navButton['border-left']).toBeUndefined();
    expect(navActive.background).toBe('var(--sidebar-active)');
    expect(newChat.height).toBe('auto');
    expect(newChat['min-height']).toBe('38px');
    expect(newChat.padding).toBe('8px 10px');
    expect(navIcon.width).toBe('20px');
    expect(navIcon.height).toBe('20px');
    expect(navIcon['flex-shrink']).toBe('0');
    expect(threadToolIcon.width).toBe('16px');
    expect(threadToolIcon.height).toBe('16px');
    expect(threadCardIcon.width).toBe('16px');
    expect(threadCardIcon.height).toBe('16px');
    expect(threadPin.background).toBe('var(--surface-2)');
    expect(threadPin.color).toBe('var(--text-muted)');
    expect(threadPin['border-color']).toBe('var(--border)');
    expect(threadPinHover.color).toBe('var(--text-pri)');
    expect(providerBadge.display).toBe('inline-flex');
    expect(providerBadge['min-height']).toBe('22px');
    expect(providerBadge['min-width']).toBe('52px');
    expect(providerBadge['font-size']).toBe('12px');
    expect(providerBadge['line-height']).toBe('1');
    expect(statusLine.display).toBe('inline-flex');
    expect(statusLine['font-size']).toBe('12px');
    expect(statusDot.width).toBe('8px');
    expect(hiddenProjectSelector.display).toBe('none');
    expect(statusDot.height).toBe('8px');
    expect(statusDot['flex']).toBe('0 0 auto');
  });

  it('renders thread card icon actions without outer rings', () => {
    const threadArchive = declarationsFor('.thread-archive');
    const threadArchiveActive = declarationsFor('.thread-archive.active');
    const threadArchiveFocus = declarationsFor('.thread-archive:focus-visible');
    const threadDelete = declarationsFor('.thread-delete-trigger');
    const threadRunning = declarationsFor('.thread-running-spinner');
    const threadPinActive = declarationsFor('.thread-pin.active');
    const threadPinFocus = declarationsFor('.thread-pin:focus-visible');

    expect(threadArchive.border).toBe('0');
    expect(threadArchive.background).toBe('transparent');
    expect(threadArchive['box-shadow']).toBe('none');
    expect(threadDelete.border).toBe('0');
    expect(threadDelete.background).toBe('transparent');
    expect(threadDelete.outline).toBe('0');
    expect(threadDelete['box-shadow']).toBe('none');
    expect(threadRunning.display).toBe('inline-grid');
    expect(threadArchiveActive.color).toBe(threadPinActive.color);
    expect(threadArchiveActive.border).toBe('none');
    expect(threadArchiveActive.background).toBe(threadPinActive.background);
    expect(threadArchiveActive['border-color']).toBe(threadPinActive['border-color']);
    expect(threadArchiveActive['border-style']).toBe(threadPinActive['border-style']);
    expect(threadArchiveActive.outline).toBe(threadPinFocus.outline);
    expect(threadArchiveActive['box-shadow']).toBe(threadPinActive['box-shadow']);
    expect(threadArchiveFocus.outline).toBe(threadPinFocus.outline);
    expect(threadArchiveFocus['box-shadow']).toBe('none');
  });

  it('lets thread card actions adapt inside the agent list width', () => {
    const card = declarationsFor('.thread-card');
    const main = declarationsFor('.thread-main');
    const editingMain = declarationsFor('.thread-main--editing');
    const actions = firstDeclarationsFor('.thread-card-actions');
    const archive = declarationsFor('.thread-archive');
    const pin = declarationsFor('.thread-pin');
    const deleteAction = declarationsFor('.thread-delete-trigger');
    const compactActions = containerDeclarationsFor('(max-width: 260px)', '.thread-card-actions');

    expect(card['grid-template-columns']).toBe('minmax(0, 1fr) minmax(0, max-content)');
    expect(card['container-type']).toBe('inline-size');
    expect(card.padding).toBe('8px');
    expect(main['grid-column']).toBe('1');
    expect(main.padding).toBe('0');
    expect(editingMain['padding-left']).toBe('0');
    expect(editingMain['padding-right']).toBe('0');
    expect(actions.display).toBe('flex');
    expect(actions['flex-wrap']).toBe('nowrap');
    expect(actions['justify-content']).toBe('flex-end');
    expect(actions['max-width']).toBe('max-content');
    expect(archive.position).toBe('relative');
    expect(pin.position).toBe('relative');
    expect(deleteAction.position).toBe('relative');
    expect(archive.transform).toBe('none');
    expect(pin.transform).toBe('none');
    expect(deleteAction.transform).toBe('none');
    expect(compactActions).toHaveLength(1);
    expect(compactActions[0]['grid-column']).toBe('1 / -1');
    expect(compactActions[0]['grid-row']).toBe('2');
    expect(compactActions[0].width).toBe('100%');
    expect(compactActions[0]['max-width']).toBe('100%');
    expect(compactActions[0].gap).toBe('3px');
    expect(compactActions[0]['flex-direction']).toBeUndefined();
  });

  it('keeps workflow run history rows aligned as scannable data columns', () => {
    const row = declarationsFor('.run-row');
    const label = declarationsFor('.run-row span');
    const status = declarationsFor('.run-row em');
    const time = declarationsFor('.run-row time');

    expect(row.display).toBe('grid');
    expect(row['grid-template-columns']).toBe('minmax(128px, 1fr) minmax(56px, max-content) max-content');
    expect(row['text-align']).toBe('left');
    expect(label['justify-self']).toBe('start');
    expect(status['justify-self']).toBe('end');
    expect(status['white-space']).toBe('nowrap');
    expect(time['justify-self']).toBe('end');
    expect(time['font-variant-numeric']).toBe('tabular-nums');
    expect(time['white-space']).toBe('nowrap');
  });

  it('keeps runtime panel details shrink-safe inside the right rail', () => {
    const panel = declarationsFor('.runtime-panel');
    const toolbar = declarationsFor('.runtime-toolbar');
    const activityPanel = declarationsFor('.runtime-activity-panel');
    const diffGroup = declarationsFor('.diff-file-group');
    const icons = declarationsFor('.runtime-icons');
    const logs = declarationsFor('.log-lines');
    const tooltipRow = declarationsFor('.runtime-stat-tooltip-row');
    const tooltipName = declarationsFor('.runtime-stat-tooltip-name');
    const logLine = declarationsFor('.warning-log-line');

    expect(panel.position).toBe('relative');
    expect(panel['z-index']).toBe('var(--z-local-sticky)');
    expect(panel.overflow).toBe('hidden');
    expect(panel.background).toBe('var(--surface-2)');
    expect(toolbar.background).toBe('var(--surface-2)');
    expect(diffGroup.background).toBe('var(--surface)');
    expect(activityPanel['border-top']).toBe('1px solid var(--line)');
    expect(activityPanel.background).toBe('var(--surface-2)');
    expect(activityPanel['min-width']).toBe('0');
    expect(activityPanel['max-width']).toBe('100%');
    expect(activityPanel.overflow).toBe('hidden');
    expect(icons['min-width']).toBe('0');
    expect(icons.overflow).toBe('visible');
    expect(logs['min-width']).toBe('0');
    expect(logs['max-width']).toBe('100%');
    expect(logs['overflow-x']).toBe('hidden');
    expect(tooltipRow['min-width']).toBe('0');
    expect(tooltipName['min-width']).toBe('0');
    expect(tooltipName.overflow).toBe('visible');
    expect(tooltipName['white-space']).toBe('normal');
    expect(logLine['min-width']).toBe('0');
    expect(logLine.display).toBe('block');
    expect(logLine.width).toBe('100%');
    expect(logLine['max-width']).toBe('100%');
    expect(logLine.overflow).toBe('hidden');
    expect(logLine['text-overflow']).toBe('ellipsis');
  });
});
