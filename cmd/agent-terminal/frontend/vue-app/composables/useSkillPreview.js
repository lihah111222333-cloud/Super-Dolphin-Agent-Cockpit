import {
  ref,
  computed,
  watch,
  onBeforeUnmount,
} from '../../lib/vue.esm-browser.prod.js';
import { callAPI } from '../services/api.js';
import { listSkills } from '../services/skills-api.js';
import { logDebug, logWarn } from '../services/log.js';
import {
  normalizeSkillPreviewMatches,
  skillNameKey,
  collectForceMatchedSkillNames,
  mergeSkillNameLists,
  composerSkillMatchClass,
  composerSkillMatchReason,
  buildSkillPreviewSignature,
} from '../utils/skill-match-utils.js';
import {
  catalogRefForName,
  mergeSkillRefs,
  normalizeSkillCatalog,
  skillRefFor,
} from '../utils/skill-ref-utils.js';

export function useSkillPreview(opts) {
  const {
    composer,
    selectedThreadId,
    skillRevision,
    activeCwdSource,
  } = opts;

  const composerSkillMatches = /** @type {{ value: any[] }} */ (ref([]));
  const composerSelectedSkillNames = /** @type {{ value: string[] }} */ (ref([]));
  const composerSelectedSkillRefs = /** @type {{ value: any[] }} */ (ref([]));
  const composerSkillPreviewLoading = ref(false);

  let composerSkillPreviewTimer = 0;
  let composerSkillPreviewSeq = 0;
  let composerSkillPreviewQueued = { requestSeq: 0, threadId: '', text: '' };
  let hasComposerSkillPreviewQueued = false;
  let composerSkillPreviewLastSignature = '';
  let composerSkillPreviewLastWarnAt = 0;

  function resolveActiveCwd() {
    return (activeCwdSource?.value || '').toString().trim();
  }

  function clearComposerSkillPreviewTimer() {
    if (!composerSkillPreviewTimer) return;
    window.clearTimeout(composerSkillPreviewTimer);
    composerSkillPreviewTimer = 0;
  }

  // pure match helpers are shared via utils/skill-match-utils.js

  const composerAutoAppliedSkillNames = computed(() => collectForceMatchedSkillNames(composerSkillMatches.value));

  const composerEffectiveSelectedSkillNames = computed(() => mergeSkillNameLists(
    composerSelectedSkillNames.value,
    composerAutoAppliedSkillNames.value,
  ));
  const composerEffectiveSelectedSkillRefs = computed(() => mergeSkillRefs(composerSelectedSkillRefs.value));

  function isComposerSkillAutoApplied(rawName) {
    const nameKey = skillNameKey(rawName);
    if (!nameKey) return false;
    return composerAutoAppliedSkillNames.value.some((name) => skillNameKey(name) === nameKey);
  }

  function isComposerSkillSelected(rawName) {
    const nameKey = skillNameKey(rawName);
    if (!nameKey) return false;
    return composerEffectiveSelectedSkillNames.value.some((name) => skillNameKey(name) === nameKey);
  }

  function setComposerSelectedSkill(rawSkill, selected) {
    const refItem = skillRefFor(rawSkill);
    const normalized = refItem.name;
    const nameKey = skillNameKey(normalized);
    if (!nameKey) return;
    const next = composerSelectedSkillNames.value.filter((name) => skillNameKey(name) !== nameKey);
    const nextRefs = composerSelectedSkillRefs.value.filter((refItem) => skillNameKey(refItem?.name) !== nameKey);
    if (selected) {
      next.push(normalized);
      if (refItem.key && refItem.scope && refItem.path) nextRefs.push(refItem);
    }
    composerSelectedSkillNames.value = next;
    composerSelectedSkillRefs.value = nextRefs;
  }

  function toggleComposerSelectedSkill(rawSkill) {
    const name = rawSkill && typeof rawSkill === 'object' ? rawSkill.name : rawSkill;
    if (isComposerSkillAutoApplied(name)) return;
    const selected = isComposerSkillSelected(name);
    setComposerSelectedSkill(rawSkill, !selected);
  }

  function clearComposerSelectedSkills() {
    if (composerSelectedSkillNames.value.length === 0) return;
    composerSelectedSkillNames.value = [];
    composerSelectedSkillRefs.value = [];
  }

  function resetSelectedComposerSkills() {
    composerSelectedSkillNames.value = [];
    composerSelectedSkillRefs.value = [];
  }

  function selectAllComposerSuggestedSkills() {
    if (!Array.isArray(composerSkillMatches.value) || composerSkillMatches.value.length === 0) return;
    composerSelectedSkillNames.value = mergeSkillNameLists(
      composerSkillMatches.value.map((match) => (match?.name || '').toString().trim()),
    );
    composerSelectedSkillRefs.value = [];
  }

  // presentation/signature helpers are shared via utils/skill-match-utils.js

  function maybeWarnSkillPreviewFailure(meta) {
    const now = Date.now();
    if (now - composerSkillPreviewLastWarnAt < 2000) return;
    composerSkillPreviewLastWarnAt = now;
    logWarn('ui', 'chat.skillPreview.failed', meta);
  }

  function runQueuedComposerSkillPreviewIfNeeded() {
    if (!hasComposerSkillPreviewQueued || composerSkillPreviewLoading.value) return;
    const queued = composerSkillPreviewQueued;
    hasComposerSkillPreviewQueued = false;
    if (queued.requestSeq !== composerSkillPreviewSeq) return;
    runComposerSkillPreview(queued.requestSeq, queued.threadId, queued.text).catch(() => { });
  }

  async function runComposerSkillPreview(requestSeq, threadId, text) {
    const startedAt = Date.now();
    composerSkillPreviewLoading.value = true;
    try {
      const raw = await callAPI('skills/match/preview', {
        threadId,
        text,
        cwd: resolveActiveCwd(),
      });
      if (requestSeq !== composerSkillPreviewSeq) return;
      const matches = normalizeSkillPreviewMatches(raw?.matches);
      composerSkillMatches.value = matches;
      const signature = buildSkillPreviewSignature(matches);
      if (signature !== composerSkillPreviewLastSignature) {
        composerSkillPreviewLastSignature = signature;
        logDebug('ui', 'chat.skillPreview.done', {
          thread_id: threadId,
          text_len: text.length,
          matches: matches.length,
          duration_ms: Date.now() - startedAt,
        });
      }
    } catch (error) {
      if (requestSeq !== composerSkillPreviewSeq) return;
      composerSkillMatches.value = [];
      composerSkillPreviewLastSignature = '';
      maybeWarnSkillPreviewFailure({
        thread_id: threadId,
        text_len: text.length,
        error,
        duration_ms: Date.now() - startedAt,
      });
    } finally {
      if (requestSeq === composerSkillPreviewSeq) {
        composerSkillPreviewLoading.value = false;
      }
      runQueuedComposerSkillPreviewIfNeeded();
    }
  }

  function requestComposerSkillPreview(threadId, text) {
    const requestSeq = ++composerSkillPreviewSeq;
    if (composerSkillPreviewLoading.value) {
      composerSkillPreviewQueued = { requestSeq, threadId, text };
      hasComposerSkillPreviewQueued = true;
      return;
    }
    runComposerSkillPreview(requestSeq, threadId, text).catch(() => { });
  }

  function scheduleComposerSkillPreview() {
    clearComposerSkillPreviewTimer();
    const threadId = (selectedThreadId.value || '').toString().trim();
    const text = (composer.state.text || '').toString().trim();
    if (!threadId || !text) {
      composerSkillPreviewSeq += 1;
      hasComposerSkillPreviewQueued = false;
      composerSkillPreviewLastSignature = '';
      composerSkillMatches.value = [];
      composerSkillPreviewLoading.value = false;
      return;
    }
    composerSkillPreviewTimer = window.setTimeout(() => {
      composerSkillPreviewTimer = 0;
      requestComposerSkillPreview(threadId, text);
    }, 240);
  }

  async function resolveComposerSkillSelectionForSend(threadId, text) {
    const manualSelectedSkillNames = mergeSkillNameLists(composerSelectedSkillNames.value);
    const manualSelectedSkillRefs = mergeSkillRefs(composerSelectedSkillRefs.value.map((refItem) => ({ ...refItem, source: 'manual' })));
    let forceMatchedSkillNames = [...composerAutoAppliedSkillNames.value];
    try {
      const raw = await callAPI('skills/match/preview', {
        threadId,
        text,
        cwd: resolveActiveCwd(),
      });
      const latestMatches = normalizeSkillPreviewMatches(raw?.matches);
      composerSkillMatches.value = latestMatches;
      composerSkillPreviewLastSignature = buildSkillPreviewSignature(latestMatches);
      forceMatchedSkillNames = collectForceMatchedSkillNames(latestMatches);
    } catch (error) {
      maybeWarnSkillPreviewFailure({
        thread_id: threadId,
        text_len: text.length,
        error,
        when: 'send',
      });
    }
    let skillCatalog = [];
    if (manualSelectedSkillNames.length > 0 || forceMatchedSkillNames.length > 0) {
      try {
        skillCatalog = normalizeSkillCatalog(await listSkills(resolveActiveCwd()));
      } catch (error) {
        logWarn('ui', 'chat.skillPreview.list.failed', { error, when: 'send' });
      }
    }

    return {
      selectedSkills: mergeSkillNameLists(manualSelectedSkillNames, forceMatchedSkillNames),
      selectedSkillRefs: mergeSkillRefs(
        manualSelectedSkillRefs,
        manualSelectedSkillNames
          .filter((name) => !manualSelectedSkillRefs.some((refItem) => skillNameKey(refItem?.name) === skillNameKey(name)))
          .map((name) => catalogRefForName(skillCatalog, name, 'manual')),
        forceMatchedSkillNames.map((name) => catalogRefForName(skillCatalog, name, 'force')),
      ),
      manualSkillSelection: manualSelectedSkillNames.length > 0,
    };
  }

  watch(
    [() => selectedThreadId.value, () => composer.state.text],
    () => {
      scheduleComposerSkillPreview();
    },
    { immediate: true },
  );

  watch(
    () => Number(skillRevision?.value || 0),
    () => {
      scheduleComposerSkillPreview();
    },
  );

  watch(
    () => composerSkillMatches.value,
    () => {
      composerSelectedSkillNames.value = mergeSkillNameLists(composerSelectedSkillNames.value);
      composerSelectedSkillRefs.value = mergeSkillRefs(composerSelectedSkillRefs.value);
    },
  );

  onBeforeUnmount(() => {
    clearComposerSkillPreviewTimer();
    composerSkillPreviewSeq += 1;
    hasComposerSkillPreviewQueued = false;
    composerSkillPreviewLoading.value = false;
  });

  return {
    composerSkillMatches,
    composerEffectiveSelectedSkillNames,
    composerEffectiveSelectedSkillRefs,
    composerSkillPreviewLoading,
    isComposerSkillSelected,
    toggleComposerSelectedSkill,
    clearComposerSelectedSkills,
    resetSelectedComposerSkills,
    selectAllComposerSuggestedSkills,
    composerSkillMatchClass,
    composerSkillMatchReason,
    resolveComposerSkillSelectionForSend,
  };
}
