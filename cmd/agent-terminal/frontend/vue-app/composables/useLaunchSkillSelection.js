import { computed, onBeforeUnmount, ref, watch } from '../../lib/vue.esm-browser.prod.js';
import { logDebug, logWarn } from '../services/log.js';
import { listSkills, previewSkillMatches } from '../services/skills-api.js';
import {
  buildSkillPreviewSignature,
  collectForceMatchedSkillNames,
  mergeSkillNameLists,
  normalizeSkillPreviewMatches,
  skillNameKey,
} from '../utils/skill-match-utils.js';

const EMPTY_LAUNCH_SELECTION = Object.freeze({ enabled: false, selectedSkills: [], manualSkillSelection: false });

function normalizeLaunchFeature(rawFeature) {
  if (typeof rawFeature === 'boolean') return rawFeature;
  if (!rawFeature || typeof rawFeature !== 'object') return false;
  if (Object.prototype.hasOwnProperty.call(rawFeature, 'enabled')) return rawFeature.enabled !== false;
  return true;
}

export function resolveLaunchSkillSelectionFeature(threadFeatures, projectFeatures) {
  const threadFeature = threadFeatures && typeof threadFeatures === 'object'
    ? threadFeatures.launchSkillSelection
    : undefined;
  const projectFeature = projectFeatures && typeof projectFeatures === 'object'
    ? projectFeatures.launchSkillSelection
    : undefined;
  return normalizeLaunchFeature(threadFeature ?? projectFeature ?? false);
}

function normalizeSkills(rawSkills) {
  if (!Array.isArray(rawSkills)) return [];
  const next = new Array();
  const seen = new Set();
  rawSkills.forEach((rawSkill) => {
    const name = (rawSkill?.name || '').toString().trim();
    const key = skillNameKey(name);
    if (!key || seen.has(key)) return;
    seen.add(key);
    next.push({
      name,
      summary: (rawSkill?.summary || '').toString().trim(),
      description: (rawSkill?.description || '').toString().trim(),
    });
  });
  return next;
}

export function useLaunchSkillSelection(opts) {
  const { composer, selectedThreadId, skillRevision, featureSource } = opts;
  const launchAvailableSkills = ref([]);
  const launchSkillMatches = ref([]);
  const launchManualSkillNames = ref([]);
  const launchSkillCatalogLoading = ref(false);
  const launchSkillPreviewLoading = ref(false);
  const launchSkillSelectionEnabled = computed(() => resolveLaunchSkillSelectionFeature(
    featureSource?.value?.threadFeatures,
    featureSource?.value?.projectFeatures,
  ));
  const launchSkillSelectionLoading = computed(() => launchSkillCatalogLoading.value || launchSkillPreviewLoading.value);
  const launchForceMatchedSkillNames = computed(() => collectForceMatchedSkillNames(launchSkillMatches.value));
  const launchSelectedSkillNames = computed(() => mergeSkillNameLists(launchManualSkillNames.value, launchForceMatchedSkillNames.value));
  let launchSkillPreviewTimer = 0;
  let launchSkillPreviewSeq = 0;
  let launchSkillPreviewSignature = '';

  function clearLaunchSkillPreviewTimer() {
    if (!launchSkillPreviewTimer) return;
    if (typeof window !== 'undefined' && typeof window.clearTimeout === 'function') window.clearTimeout(launchSkillPreviewTimer);
    else if (typeof globalThis.clearTimeout === 'function') globalThis.clearTimeout(launchSkillPreviewTimer);
    launchSkillPreviewTimer = 0;
  }

  function resetLaunchSkillPreview() {
    clearLaunchSkillPreviewTimer();
    launchSkillPreviewSeq += 1;
    launchSkillPreviewLoading.value = false;
    launchSkillPreviewSignature = '';
    launchSkillMatches.value = [];
  }

  function resetLaunchSkillSelection() {
    launchManualSkillNames.value = [];
    resetLaunchSkillPreview();
  }

  function isLaunchSkillAutoApplied(rawName) {
    const nameKey = skillNameKey(rawName);
    if (!nameKey) return false;
    return launchForceMatchedSkillNames.value.some((name) => skillNameKey(name) === nameKey);
  }

  function toggleLaunchSelectedSkill(rawName) {
    if (isLaunchSkillAutoApplied(rawName)) return;
    const normalized = (rawName || '').toString().trim();
    const nameKey = skillNameKey(normalized);
    if (!nameKey) return;
    const next = launchManualSkillNames.value.filter((name) => skillNameKey(name) !== nameKey);
    if (!launchSelectedSkillNames.value.some((name) => skillNameKey(name) === nameKey)) next.push(normalized);
    launchManualSkillNames.value = next;
  }

  function clearLaunchSelectedSkills() {
    if (launchManualSkillNames.value.length === 0) return;
    launchManualSkillNames.value = [];
  }

  function selectAllLaunchSuggestedSkills() {
    if (!Array.isArray(launchSkillMatches.value) || launchSkillMatches.value.length === 0) return;
    const selectableNames = launchSkillMatches.value
      .filter((match) => !isLaunchSkillAutoApplied(match?.name))
      .map((match) => (match?.name || '').toString().trim());
    launchManualSkillNames.value = mergeSkillNameLists(launchManualSkillNames.value, selectableNames);
  }

  async function refreshLaunchSkillCatalog() {
    if (!launchSkillSelectionEnabled.value) {
      launchAvailableSkills.value = [];
      return;
    }
    launchSkillCatalogLoading.value = true;
    try {
      launchAvailableSkills.value = normalizeSkills(await listSkills());
    } catch (error) {
      launchAvailableSkills.value = [];
      logWarn('ui', 'launchSkillSelection.list.failed', { error });
    } finally {
      launchSkillCatalogLoading.value = false;
    }
  }

  async function runLaunchSkillPreview(text) {
    const normalizedText = (text || '').toString().trim();
    const threadId = (selectedThreadId.value || '').toString().trim();
    const requestSeq = ++launchSkillPreviewSeq;
    if (!launchSkillSelectionEnabled.value || threadId || !normalizedText) {
      resetLaunchSkillPreview();
      return [];
    }
    const startedAt = Date.now();
    launchSkillPreviewLoading.value = true;
    try {
      const raw = await previewSkillMatches({ threadId: '', text: normalizedText });
      if (requestSeq !== launchSkillPreviewSeq) return launchSkillMatches.value;
      const matches = normalizeSkillPreviewMatches(raw?.matches);
      launchSkillMatches.value = matches;
      const nextSignature = buildSkillPreviewSignature(matches);
      if (nextSignature !== launchSkillPreviewSignature) {
        launchSkillPreviewSignature = nextSignature;
        logDebug('ui', 'launchSkillSelection.preview.done', { text_len: normalizedText.length, matches: matches.length, duration_ms: Date.now() - startedAt });
      }
      return matches;
    } catch (error) {
      if (requestSeq !== launchSkillPreviewSeq) return [];
      launchSkillMatches.value = [];
      launchSkillPreviewSignature = '';
      logWarn('ui', 'launchSkillSelection.preview.failed', { text_len: normalizedText.length, error, duration_ms: Date.now() - startedAt });
      return [];
    } finally {
      if (requestSeq === launchSkillPreviewSeq) launchSkillPreviewLoading.value = false;
    }
  }

  function scheduleLaunchSkillPreview() {
    clearLaunchSkillPreviewTimer();
    const threadId = (selectedThreadId.value || '').toString().trim();
    const text = (composer?.state?.text || '').toString().trim();
    if (!launchSkillSelectionEnabled.value || threadId || !text) {
      resetLaunchSkillPreview();
      return;
    }
    if (typeof window === 'undefined' || typeof window.setTimeout !== 'function') {
      runLaunchSkillPreview(text).catch(() => {});
      return;
    }
    launchSkillPreviewTimer = window.setTimeout(() => {
      launchSkillPreviewTimer = 0;
      runLaunchSkillPreview(text).catch(() => {});
    }, 240);
  }

  async function refreshLaunchSkillSelection() {
    await refreshLaunchSkillCatalog();
    const threadId = (selectedThreadId.value || '').toString().trim();
    const text = (composer?.state?.text || '').toString().trim();
    if (threadId || !text) {
      resetLaunchSkillPreview();
      return;
    }
    await runLaunchSkillPreview(text);
  }

  async function resolveLaunchSkillSelectionForStart(text) {
    if (!launchSkillSelectionEnabled.value) return EMPTY_LAUNCH_SELECTION;
    const manualSelectedSkillNames = mergeSkillNameLists(launchManualSkillNames.value);
    const normalizedText = (text || '').toString().trim();
    let forceMatchedSkillNames = [...launchForceMatchedSkillNames.value];
    if (normalizedText) {
      try {
        const raw = await previewSkillMatches({ threadId: '', text: normalizedText });
        const latestMatches = normalizeSkillPreviewMatches(raw?.matches);
        launchSkillMatches.value = latestMatches;
        launchSkillPreviewSignature = buildSkillPreviewSignature(latestMatches);
        forceMatchedSkillNames = collectForceMatchedSkillNames(latestMatches);
      } catch (error) {
        logWarn('ui', 'launchSkillSelection.resolve.failed', { text_len: normalizedText.length, error, when: 'start' });
      }
    } else {
      resetLaunchSkillPreview();
    }
    return {
      enabled: true,
      selectedSkills: mergeSkillNameLists(manualSelectedSkillNames, forceMatchedSkillNames),
      manualSkillSelection: manualSelectedSkillNames.length > 0,
    };
  }

  watch(launchSkillSelectionEnabled, (enabled) => {
    if (!enabled) {
      launchAvailableSkills.value = [];
      resetLaunchSkillSelection();
      return;
    }
    refreshLaunchSkillCatalog().catch(() => {});
    scheduleLaunchSkillPreview();
  }, { immediate: true });

  watch([() => selectedThreadId.value, () => composer?.state?.text], () => {
    if ((selectedThreadId.value || '').toString().trim()) {
      resetLaunchSkillSelection();
      return;
    }
    scheduleLaunchSkillPreview();
  }, { immediate: true });

  watch(() => Number(skillRevision?.value || 0), () => {
    if (!launchSkillSelectionEnabled.value) return;
    refreshLaunchSkillCatalog().catch(() => {});
    scheduleLaunchSkillPreview();
  });

  watch(() => launchSkillMatches.value, () => {
    launchManualSkillNames.value = mergeSkillNameLists(launchManualSkillNames.value);
  });

  onBeforeUnmount(() => {
    clearLaunchSkillPreviewTimer();
    launchSkillPreviewSeq += 1;
    launchSkillPreviewLoading.value = false;
  });

  return {
    launchSkillSelectionEnabled,
    launchAvailableSkills,
    launchSkillMatches,
    launchSelectedSkillNames,
    launchSkillSelectionLoading,
    toggleLaunchSelectedSkill,
    clearLaunchSelectedSkills,
    selectAllLaunchSuggestedSkills,
    refreshLaunchSkillSelection,
    resolveLaunchSkillSelectionForStart,
    resetLaunchSkillSelection,
  };
}
