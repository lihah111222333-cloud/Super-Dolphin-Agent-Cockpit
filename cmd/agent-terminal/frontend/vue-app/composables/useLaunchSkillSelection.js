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
import {
  catalogRefForName as uniqueCatalogRefForName,
  mergeSkillRefs,
  normalizeSkillCatalog,
  skillPersonalType,
  skillRefFor,
  skillScopeFromSkill,
  skillScopeFromTrust,
} from '../utils/skill-ref-utils.js';

const EMPTY_LAUNCH_SELECTION = Object.freeze({ enabled: false, selectedSkills: [], selectedSkillRefs: [], manualSkillSelection: false });
const EMPTY_LAUNCH_SCOPE = '';

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

const normalizeSkills = normalizeSkillCatalog;
const launchSkillScopeFromTrust = skillScopeFromTrust;
const launchSkillScopeFromSkill = skillScopeFromSkill;
const launchSkillPersonalType = skillPersonalType;
const launchSkillRefFor = skillRefFor;
const mergeLaunchSkillRefs = mergeSkillRefs;

function syncLaunchSkillScope(scopeRef, scopeTabsEnabled, projectSkills, systemSkills) {
  if (!scopeTabsEnabled.value) {
    scopeRef.value = EMPTY_LAUNCH_SCOPE;
    return;
  }
  if (scopeRef.value === 'project' && projectSkills.value.length > 0) return;
  if (scopeRef.value === 'personal' && systemSkills.value.length > 0) return;
  scopeRef.value = projectSkills.value.length > 0 ? 'project' : 'personal';
}

function updateLaunchSkillScope(scopeRef, scopeTabsEnabled, projectSkills, systemSkills, nextScope) {
  const normalized = (nextScope || '').toString().trim().toLowerCase();
  if (!scopeTabsEnabled.value) {
    scopeRef.value = EMPTY_LAUNCH_SCOPE;
    return;
  }
  if (normalized === 'personal' && systemSkills.value.length > 0) {
    scopeRef.value = 'personal';
    return;
  }
  if (projectSkills.value.length > 0) {
    scopeRef.value = 'project';
    return;
  }
  scopeRef.value = systemSkills.value.length > 0 ? 'personal' : EMPTY_LAUNCH_SCOPE;
}

function namesNotCoveredByRefs(names, refs) {
  const covered = new Set((Array.isArray(refs) ? refs : [])
    .map((item) => skillNameKey(item?.name))
    .filter(Boolean));
  return mergeSkillNameLists(names).filter((name) => !covered.has(skillNameKey(name)));
}

function resolveActiveCwd(activeCwdSource) {
  return (activeCwdSource?.value || '').toString().trim();
}

async function resolveLaunchSkillRefsForStart(ctx, text) {
  const normalizedText = (text || '').toString().trim();
  let forceMatchedSkillNames = [...ctx.forceMatchedSkillNames.value];
  if (!normalizedText) {
    ctx.resetPreview();
    return forceMatchedSkillNames;
  }
  try {
    const raw = await previewSkillMatches({ threadId: '', text: normalizedText, cwd: ctx.cwd() });
    const latestMatches = normalizeSkillPreviewMatches(raw?.matches);
    ctx.matches.value = latestMatches;
    ctx.previewSignature.set(buildSkillPreviewSignature(latestMatches));
    forceMatchedSkillNames = collectForceMatchedSkillNames(latestMatches);
  } catch (error) {
    logWarn('ui', 'launchSkillSelection.resolve.failed', { text_len: normalizedText.length, error, when: 'start' });
  }
  return forceMatchedSkillNames;
}

function installLaunchSkillSelectionWatchers(ctx) {
  watch(ctx.launchSkillSelectionEnabled, (enabled) => {
    if (!enabled) {
      ctx.launchAvailableSkills.value = [];
      ctx.resetLaunchSkillSelection();
      return;
    }
    ctx.refreshLaunchSkillCatalog().catch(() => {});
    ctx.scheduleLaunchSkillPreview();
  }, { immediate: true });

  watch([() => ctx.selectedThreadId.value, () => ctx.composer?.state?.text], () => {
    if ((ctx.selectedThreadId.value || '').toString().trim()) {
      ctx.resetLaunchSkillSelection();
      return;
    }
    ctx.scheduleLaunchSkillPreview();
  }, { immediate: true });

  watch(() => Number(ctx.skillRevision?.value || 0), () => {
    if (!ctx.launchSkillSelectionEnabled.value) return;
    ctx.refreshLaunchSkillCatalog().catch(() => {});
    ctx.scheduleLaunchSkillPreview();
  });

  watch(() => resolveActiveCwd(ctx.activeCwdSource), (next, prev) => {
    if (next === prev || !ctx.launchSkillSelectionEnabled.value) return;
    ctx.refreshLaunchSkillCatalog().catch(() => {});
    ctx.scheduleLaunchSkillPreview();
  });

  watch(() => ctx.launchSkillMatches.value, () => {
    ctx.launchManualSkillRefs.value = mergeLaunchSkillRefs(ctx.launchManualSkillRefs.value);
  });

  watch([ctx.launchProjectSkills, ctx.launchSystemSkills], () => {
    syncLaunchSkillScope(ctx.launchSkillScope, ctx.launchScopeTabsEnabled, ctx.launchProjectSkills, ctx.launchSystemSkills);
  }, { immediate: true });

  onBeforeUnmount(() => {
    ctx.clearLaunchSkillPreviewTimer();
    ctx.cancelLaunchSkillPreview();
  });
}

export function useLaunchSkillSelection(opts) {
  const {
    composer,
    selectedThreadId,
    skillRevision,
    featureSource,
    activeCwdSource,
  } = opts;
  const launchAvailableSkills = ref([]);
  const launchSkillMatches = ref([]);
  const launchManualSkillRefs = ref([]);
  const launchSkillCatalogLoading = ref(false);
  const launchSkillPreviewLoading = ref(false);
  const launchSkillScope = ref(EMPTY_LAUNCH_SCOPE);
  const launchSkillSelectionEnabled = computed(() => resolveLaunchSkillSelectionFeature(
    featureSource?.value?.threadFeatures,
    featureSource?.value?.projectFeatures,
  ));
  const launchSkillSelectionLoading = computed(() => launchSkillCatalogLoading.value || launchSkillPreviewLoading.value);
  const launchProjectSkills = computed(() => launchAvailableSkills.value.filter((skill) => launchSkillScopeFromSkill(skill) === 'project'));
  const launchSystemSkills = computed(() => launchAvailableSkills.value.filter((skill) => launchSkillScopeFromSkill(skill) === 'personal'));
  const launchScopeTabsEnabled = computed(() => launchProjectSkills.value.length > 0 && launchSystemSkills.value.length > 0);
  const launchVisibleSkills = computed(() => {
    if (!launchScopeTabsEnabled.value) return launchAvailableSkills.value;
    return launchSkillScope.value === 'personal' ? launchSystemSkills.value : launchProjectSkills.value;
  });
  const launchForceMatchedSkillNames = computed(() => collectForceMatchedSkillNames(launchSkillMatches.value));
  const launchManualSkillNames = computed(() => launchManualSkillRefs.value.map((refItem) => refItem.name).filter(Boolean));
  const launchSelectedSkillNames = computed(() => mergeSkillNameLists(launchManualSkillNames.value, launchForceMatchedSkillNames.value));
  const launchSelectedSkillRefs = computed(() => mergeLaunchSkillRefs(launchManualSkillRefs.value, forceMatchedLaunchSkillRefs()));
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
    launchManualSkillRefs.value = [];
    launchSkillScope.value = EMPTY_LAUNCH_SCOPE;
    resetLaunchSkillPreview();
  }
  const setLaunchSkillScope = (nextScope) => updateLaunchSkillScope(
    launchSkillScope,
    launchScopeTabsEnabled,
    launchProjectSkills,
    launchSystemSkills,
    nextScope,
  );

  function isLaunchSkillAutoApplied(rawName) {
    const nameKey = skillNameKey(rawName);
    if (!nameKey) return false;
    return launchForceMatchedSkillNames.value.some((name) => skillNameKey(name) === nameKey);
  }

  function catalogRefForName(rawName) {
    const nameKey = skillNameKey(rawName);
    if (!nameKey) return launchSkillRefFor('');
    return uniqueCatalogRefForName(launchAvailableSkills.value, rawName);
  }

  function launchRefFromSelection(rawSkill) {
    const hasCatalogIdentity = rawSkill && typeof rawSkill === 'object' && (
      rawSkill.key || rawSkill.dir || rawSkill.skill_file || rawSkill.path || rawSkill.scope || rawSkill.trust
    );
    if (hasCatalogIdentity) return launchSkillRefFor(rawSkill);
    return catalogRefForName(rawSkill?.name || rawSkill);
  }

  function forceMatchedLaunchSkillRefs(names = launchForceMatchedSkillNames.value) {
    return mergeLaunchSkillRefs(names.map((name) => catalogRefForName(name)));
  }

  function toggleLaunchSelectedSkill(rawSkill) {
    const refItem = launchRefFromSelection(rawSkill);
    if (isLaunchSkillAutoApplied(refItem.name)) return;
    if (!refItem.key) return;
    const alreadySelected = launchSelectedSkillRefs.value.some((item) => item.key === refItem.key);
    const next = launchManualSkillRefs.value.filter((item) => item.key !== refItem.key);
    if (!alreadySelected) next.push(refItem);
    launchManualSkillRefs.value = next;
  }

  function clearLaunchSelectedSkills() {
    if (launchManualSkillRefs.value.length === 0) return;
    launchManualSkillRefs.value = [];
  }

  function visibleCatalogRefForName(rawName) {
    if (!launchScopeTabsEnabled.value) return catalogRefForName(rawName);
    return uniqueCatalogRefForName(launchVisibleSkills.value, rawName);
  }

  function matchInLaunchVisibleScope(match) {
    if (!launchScopeTabsEnabled.value) return true;
    const matchScope = launchSkillScopeFromSkill(match);
    if (matchScope === launchSkillScope.value) return true;
    return launchVisibleSkills.value.some((skill) => skillNameKey(skill?.name) === skillNameKey(match?.name));
  }

  function selectAllLaunchSuggestedSkills(activeMatches = null) {
    const sourceMatches = Array.isArray(activeMatches) ? activeMatches : launchSkillMatches.value;
    if (!Array.isArray(sourceMatches) || sourceMatches.length === 0) return;
    const selectableNames = sourceMatches
      .filter(matchInLaunchVisibleScope)
      .filter((match) => !isLaunchSkillAutoApplied(match?.name))
      .map((match) => (match?.name || '').toString().trim());
    const refs = selectableNames.map((name) => visibleCatalogRefForName(name)).filter((refItem) => refItem.key);
    launchManualSkillRefs.value = mergeLaunchSkillRefs(launchManualSkillRefs.value, refs);
  }

  async function refreshLaunchSkillCatalog() {
    if (!launchSkillSelectionEnabled.value) {
      launchAvailableSkills.value = [];
      return;
    }
    launchSkillCatalogLoading.value = true;
    try {
      launchAvailableSkills.value = normalizeSkills(await listSkills(resolveActiveCwd(activeCwdSource)));
    } catch (error) {
      launchAvailableSkills.value = [];
      launchSkillScope.value = EMPTY_LAUNCH_SCOPE;
      logWarn('ui', 'launchSkillSelection.list.failed', { error });
    } finally {
      launchSkillCatalogLoading.value = false;
      syncLaunchSkillScope(launchSkillScope, launchScopeTabsEnabled, launchProjectSkills, launchSystemSkills);
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
      const raw = await previewSkillMatches({ threadId: '', text: normalizedText, cwd: resolveActiveCwd(activeCwdSource) });
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
    const manualSelectedSkillRefs = mergeLaunchSkillRefs(launchManualSkillRefs.value);
    const manualSelectedSkillNames = mergeSkillNameLists(manualSelectedSkillRefs.map((item) => item.name));
    const forceMatchedSkillNames = await resolveLaunchSkillRefsForStart({
      forceMatchedSkillNames: launchForceMatchedSkillNames,
      matches: launchSkillMatches,
      resetPreview: resetLaunchSkillPreview,
      cwd: () => resolveActiveCwd(activeCwdSource),
      previewSignature: { set: (value) => { launchSkillPreviewSignature = value; } },
    }, text);
    const forceMatchedRefs = forceMatchedLaunchSkillRefs(forceMatchedSkillNames);
    const resolvedForceMatchedSkillNames = mergeSkillNameLists(forceMatchedRefs.map((item) => item.name));
    const selectedSkillRefs = mergeLaunchSkillRefs(manualSelectedSkillRefs, forceMatchedRefs);
    return {
      enabled: true,
      selectedSkills: namesNotCoveredByRefs(mergeSkillNameLists(manualSelectedSkillNames, resolvedForceMatchedSkillNames), selectedSkillRefs),
      selectedSkillRefs,
      manualSkillSelection: manualSelectedSkillNames.length > 0,
    };
  }

  installLaunchSkillSelectionWatchers({
    activeCwdSource,
    clearLaunchSkillPreviewTimer,
    composer,
    launchAvailableSkills,
    launchManualSkillRefs,
    launchProjectSkills,
    launchScopeTabsEnabled,
    launchSkillMatches,
    launchSkillScope,
    launchSkillSelectionEnabled,
    launchSystemSkills,
    refreshLaunchSkillCatalog,
    resetLaunchSkillSelection,
    scheduleLaunchSkillPreview,
    selectedThreadId,
    skillRevision,
    cancelLaunchSkillPreview: () => {
      launchSkillPreviewSeq += 1;
      launchSkillPreviewLoading.value = false;
    },
  });

  return {
    launchSkillSelectionEnabled,
    launchAvailableSkills: launchVisibleSkills,
    launchVisibleSkills,
    launchProjectSkills,
    launchSystemSkills,
    launchScopeTabsEnabled,
    launchSkillScope,
    launchSkillMatches,
    launchSelectedSkillNames,
    launchSelectedSkillRefs,
    launchSkillSelectionLoading,
    toggleLaunchSelectedSkill,
    clearLaunchSelectedSkills,
    setLaunchSkillScope,
    selectAllLaunchSuggestedSkills,
    refreshLaunchSkillSelection,
    resolveLaunchSkillSelectionForStart,
    resetLaunchSkillSelection,
  };
}
