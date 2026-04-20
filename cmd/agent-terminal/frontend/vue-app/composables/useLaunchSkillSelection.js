// @ts-nocheck
import { computed, ref, watch } from '../../lib/vue.esm-browser.prod.js';
import { logWarn } from '../services/log.js';
import { listSkills, previewSkillMatches } from '../services/skills-api.js';
import {
  collectForceMatchedSkillNames,
  mergeSkillNameLists,
  normalizeSkillPreviewMatches,
  skillNameKey,
} from '../utils/skill-match-utils.js';

function readFeatureFlag(rawValue) {
  if (rawValue === true) return true;
  if (rawValue === false || rawValue == null) return false;
  if (typeof rawValue === 'number') return rawValue === 1;
  const normalized = rawValue.toString().trim().toLowerCase();
  return normalized === '1' || normalized === 'true' || normalized === 'on' || normalized === 'yes';
}

export function useLaunchSkillSelection(opts = {}) {
  const { composer, selectedThreadId, skillRevision, featureSource } = opts;
  const skills = ref([]);
  const matches = ref([]);
  const manualSelectedSkillNames = ref([]);
  const skillsLoading = ref(false);
  const previewLoading = ref(false);

  let previewTimer = 0;
  let previewSeq = 0;
  let skillsSeq = 0;

  const features = computed(() => {
    const raw = featureSource?.value ?? featureSource ?? {};
    return { launchSkillSelection: readFeatureFlag(raw?.launchSkillSelection) };
  });
  const enabled = computed(() => features.value.launchSkillSelection);
  const loading = computed(() => skillsLoading.value || previewLoading.value);
  const autoAppliedSkillNames = computed(() => collectForceMatchedSkillNames(matches.value));
  const selectedSkillNames = computed(() => mergeSkillNameLists(
    manualSelectedSkillNames.value,
    autoAppliedSkillNames.value,
  ));

  function clearPreviewTimer() {
    if (!previewTimer) return;
    window.clearTimeout(previewTimer);
    previewTimer = 0;
  }

  function resetPreview() {
    clearPreviewTimer();
    previewSeq += 1;
    matches.value = [];
    previewLoading.value = false;
  }

  function isAutoApplied(rawName) {
    const key = skillNameKey(rawName);
    return key ? autoAppliedSkillNames.value.some((name) => skillNameKey(name) === key) : false;
  }

  async function loadSkills() {
    if (!enabled.value) {
      skills.value = [];
      skillsLoading.value = false;
      return [];
    }
    const requestId = ++skillsSeq;
    skillsLoading.value = true;
    try {
      const nextSkills = await listSkills();
      if (requestId === skillsSeq) skills.value = Array.isArray(nextSkills) ? nextSkills : [];
      return Array.isArray(nextSkills) ? nextSkills : [];
    } catch (error) {
      if (requestId === skillsSeq) skills.value = [];
      logWarn('ui', 'launch.skillPicker.list.failed', { error });
      return [];
    } finally {
      if (requestId === skillsSeq) skillsLoading.value = false;
    }
  }

  async function runPreview(textOverride) {
    if (!enabled.value || (selectedThreadId?.value || '').toString().trim()) {
      resetPreview();
      return [];
    }
    const text = (typeof textOverride === 'string' ? textOverride : composer?.state?.text || '').toString().trim();
    if (!text) {
      matches.value = [];
      previewLoading.value = false;
      return [];
    }
    const requestId = ++previewSeq;
    previewLoading.value = true;
    try {
      const rawMatches = await previewSkillMatches({ threadId: '', text });
      const normalized = normalizeSkillPreviewMatches(rawMatches);
      if (requestId === previewSeq) matches.value = normalized;
      return normalized;
    } catch (error) {
      if (requestId === previewSeq) matches.value = [];
      logWarn('ui', 'launch.skillPicker.preview.failed', { text_len: text.length, error });
      return [];
    } finally {
      if (requestId === previewSeq) previewLoading.value = false;
    }
  }

  function schedulePreview() {
    clearPreviewTimer();
    if (!enabled.value || (selectedThreadId?.value || '').toString().trim()) {
      resetPreview();
      return;
    }
    const text = (composer?.state?.text || '').toString().trim();
    if (!text) {
      matches.value = [];
      previewLoading.value = false;
      return;
    }
    previewTimer = window.setTimeout(() => {
      previewTimer = 0;
      runPreview(text).catch(() => {});
    }, 240);
  }

  function toggleSkill(rawName) {
    if (!enabled.value || isAutoApplied(rawName)) return;
    const normalized = (rawName || '').toString().trim();
    const key = skillNameKey(normalized);
    if (!key) return;
    const next = manualSelectedSkillNames.value.filter((name) => skillNameKey(name) !== key);
    if (next.length === manualSelectedSkillNames.value.length) next.push(normalized);
    manualSelectedSkillNames.value = mergeSkillNameLists(next);
  }

  function selectAll() {
    const source = matches.value.length > 0
      ? matches.value.map((match) => match?.name)
      : skills.value.map((skill) => skill?.name);
    manualSelectedSkillNames.value = mergeSkillNameLists(source);
  }

  function clear() {
    manualSelectedSkillNames.value = [];
  }

  async function refresh() {
    if (!enabled.value) return;
    await loadSkills();
    await runPreview();
  }

  async function resolveLaunchSkillSelectionForSend(textOverride) {
    const latestMatches = await runPreview(textOverride);
    const manual = mergeSkillNameLists(manualSelectedSkillNames.value);
    return {
      selectedSkills: mergeSkillNameLists(manual, collectForceMatchedSkillNames(latestMatches)),
      manualSkillSelection: manual.length > 0,
    };
  }

  function resetLaunchSkillSelection() {
    manualSelectedSkillNames.value = [];
    resetPreview();
  }

  watch(() => features.value.launchSkillSelection, (nextEnabled) => {
    if (!nextEnabled) {
      skills.value = [];
      resetLaunchSkillSelection();
      return;
    }
    loadSkills().catch(() => {});
    schedulePreview();
  }, { immediate: true });

  watch(() => composer?.state?.text, () => {
    schedulePreview();
  });

  watch(() => (selectedThreadId?.value || '').toString().trim(), () => {
    schedulePreview();
  }, { immediate: true });

  watch(() => Number(skillRevision?.value || 0), () => {
    if (!enabled.value) return;
    refresh().catch(() => {});
  });

  return {
    features,
    enabled,
    skills,
    matches,
    selectedSkillNames,
    loading,
    toggleSkill,
    selectAll,
    clear,
    refresh,
    resolveLaunchSkillSelectionForSend,
    resetLaunchSkillSelection,
  };
}
