import { computed } from '../../lib/vue.esm-browser.prod.js';
import {
  composerSkillMatchClass,
  composerSkillMatchReason,
  skillNameKey,
} from '../utils/skill-match-utils.js';

const EMPTY_SKILL = { name: '', summary: '', description: '', matchedBy: '' };
const EMPTY_ENTRY = { key: '', name: '', match: null, selected: false, autoApplied: false, summary: '', skill: null };
const EMPTY_PROPS = {
  selectedSkillNames: [],
  selectedSkillRefs: [],
  matches: [],
  skills: [],
  projectSkills: [],
  systemSkills: [],
  scope: '',
  scopeTabsEnabled: false,
};

function sortEntries(left = EMPTY_ENTRY, right = EMPTY_ENTRY) {
  const leftSelected = left.selected ? 1 : 0;
  const rightSelected = right.selected ? 1 : 0;
  if (leftSelected !== rightSelected) return rightSelected - leftSelected;
  const leftMatched = left.match ? 1 : 0;
  const rightMatched = right.match ? 1 : 0;
  if (leftMatched !== rightMatched) return rightMatched - leftMatched;
  return left.name.localeCompare(right.name, 'zh-Hans-CN');
}

function resolveSelectedSkillKeys(props) {
  const keys = props.selectedSkillRefs
    .map((ref = {}) => (ref?.key || '').toString().trim())
    .filter(Boolean);
  if (keys.length > 0) return keys;
  return props.selectedSkillNames
    .map((name = '') => skillNameKey(name))
    .filter(Boolean);
}

function normalizedScope(value) {
  const scope = (value || '').toString().trim().toLowerCase();
  return scope === 'project' || scope === 'personal' ? scope : '';
}

function scopeForSkill(skill) {
  const scope = normalizedScope(skill?.scope);
  if (scope) return scope;
  const trust = (skill?.trust || '').toString().trim().toLowerCase();
  return trust === 'project' ? 'project' : 'personal';
}

export const LaunchSkillPicker = {
  name: 'LaunchSkillPicker',
  props: {
    enabled: { type: Boolean, default: false },
    skills: { type: Array, default: () => [] },
    projectSkills: { type: Array, default: () => [] },
    systemSkills: { type: Array, default: () => [] },
    scope: { type: String, default: '' },
    scopeTabsEnabled: { type: Boolean, default: false },
    matches: { type: Array, default: () => [] },
    selectedSkillNames: { type: Array, default: () => [] },
    selectedSkillRefs: { type: Array, default: () => [] },
    loading: { type: Boolean, default: false },
  },
  emits: ['toggle-skill', 'select-all', 'clear', 'refresh', 'update:scope'],
  setup(props = EMPTY_PROPS, { emit }) {
    const selectedSkillKeys = computed(() => new Set(resolveSelectedSkillKeys(props)));
    const selectedSkillCount = computed(() => selectedSkillKeys.value.size);

    const showScopeTabs = computed(() => props.scopeTabsEnabled && Boolean(props.scope));
    const scopedSkills = computed(() => {
      if (!showScopeTabs.value) return props.skills;
      return props.scope === 'personal' ? props.systemSkills : props.projectSkills;
    });
    const scopedSkillNameKeys = computed(() => new Set(scopedSkills.value.map((skill) => skillNameKey(skill?.name || skill)).filter(Boolean)));
    const activeMatches = computed(() => {
      if (!showScopeTabs.value) return props.matches;
      const activeScope = props.scope === 'personal' ? 'personal' : 'project';
      return props.matches.filter((match = EMPTY_SKILL) => {
        const matchScope = normalizedScope(match?.scope);
        if (matchScope) return matchScope === activeScope;
        return scopedSkillNameKeys.value.has(skillNameKey(match?.name));
      });
    });

    const skillEntries = computed(() => {
      const matchByKey = new Map();
      const entries = [{ key: '', name: '', match: null, selected: false, autoApplied: false, summary: '' }];
      entries.pop();
      const seen = new Set();
      activeMatches.value.forEach((match = EMPTY_SKILL) => {
        const key = skillNameKey(match?.name);
        if (key && !matchByKey.has(key)) matchByKey.set(key, match);
      });
      const catalogSkillForName = (rawName) => {
        const nameKey = skillNameKey(rawName);
        if (!nameKey) return null;
        const allSkills = [...scopedSkills.value, ...props.skills, ...props.projectSkills, ...props.systemSkills];
        return allSkills.find((skill) => skillNameKey(skill?.name || skill) === nameKey) || null;
      };
      const entrySkillFor = (rawSkill, name) => {
        const hasCatalogIdentity = rawSkill && typeof rawSkill === 'object' && (
          rawSkill.key || rawSkill.dir || rawSkill.skill_file || rawSkill.path || rawSkill.scope || rawSkill.trust
        );
        return hasCatalogIdentity ? rawSkill : (catalogSkillForName(name) || rawSkill);
      };
      const pushEntry = (rawSkill = EMPTY_SKILL) => {
        const name = (rawSkill?.name || rawSkill || '').toString().trim();
        const resolvedSkill = entrySkillFor(rawSkill, name);
        const key = (resolvedSkill?.key || rawSkill?.key || skillNameKey(name)).toString().trim();
        if (!key || seen.has(key)) return;
        seen.add(key);
        const match = matchByKey.get(skillNameKey(name)) || null;
        entries.push({
          key,
          name,
          match,
          selected: selectedSkillKeys.value.has(key) || selectedSkillKeys.value.has(skillNameKey(name)),
          autoApplied: match?.matchedBy === 'force',
          summary: (resolvedSkill?.summary || resolvedSkill?.description || rawSkill?.summary || rawSkill?.description || '').toString().trim(),
          skill: resolvedSkill,
        });
      };
      activeMatches.value
        .filter((match = EMPTY_SKILL) => !scopedSkillNameKeys.value.has(skillNameKey(match?.name)))
        .forEach(pushEntry);
      scopedSkills.value.forEach(pushEntry);
      return entries.sort(sortEntries);
    });
    const activeScope = computed(() => {
      if (!showScopeTabs.value) return '';
      return props.scope === 'personal' ? 'personal' : 'project';
    });
    const activeMatchedSkills = computed(() => skillEntries.value
      .filter((entry) => {
        if (!entry.match) return false;
        if (!activeScope.value) return true;
        return scopeForSkill(entry.skill || entry.match) === activeScope.value;
      })
      .map((entry) => entry.skill || entry.match || entry.name));

    function entryReason(entry = EMPTY_ENTRY) {
      if (entry.match) {
        const reason = composerSkillMatchReason(entry.match);
        return entry.autoApplied ? `${reason} · 已自动启用` : reason;
      }
      return entry.summary || '手动选择后将在 launch 时带入';
    }

    function entryClass(entry = EMPTY_ENTRY) {
      return [entry.match ? composerSkillMatchClass(entry.match) : '', { selected: entry.selected }];
    }

    function toggleEntry(entry = EMPTY_ENTRY) {
      if (entry.autoApplied) return;
      emit('toggle-skill', entry.skill || entry.name);
    }

    function updateScope(nextScope) {
      emit('update:scope', nextScope);
    }

    return {
      selectedSkillCount,
      showScopeTabs,
      scopedSkills,
      skillEntries,
      entryReason,
      entryClass,
      toggleEntry,
      updateScope,
      onSelectAll: () => emit('select-all', activeMatchedSkills.value),
      onClear: () => emit('clear'),
      onRefresh: () => emit('refresh'),
    };
  },
  template: `
    <div
      v-if="enabled"
      class="composer-skill-selector launch-skill-picker"
      :class="{ 'is-expanded': skillEntries.length > 8 }"
      role="status"
      aria-live="polite"
      data-testid="launch-skill-picker"
    >
      <div class="composer-skill-selector-head">
        <span class="composer-skill-selector-title" :class="{ 'loading-shimmer': loading }">
          {{ loading ? '首发技能准备中…' : ('首发技能 ' + selectedSkillCount + '/' + skillEntries.length) }}
        </span>
        <div class="composer-skill-selector-actions">
          <button class="composer-skill-selector-btn" type="button" @click="onRefresh">刷新</button>
          <button
            class="composer-skill-selector-btn"
            type="button"
            :disabled="matches.length === 0"
            @click="onSelectAll"
          >全选匹配</button>
          <button
            class="composer-skill-selector-btn"
            type="button"
            :disabled="selectedSkillCount === 0"
            @click="onClear"
          >清空</button>
        </div>
      </div>
      <div v-if="showScopeTabs" class="composer-skill-selector-tabs" data-testid="launch-skill-scope-tabs">
        <button
          class="composer-skill-selector-tab"
          :class="{ selected: scope === 'project' }"
          data-testid="launch-skill-scope-tab-project"
          type="button"
          @click="updateScope('project')"
        >
          <span class="composer-skill-scope-dot composer-skill-scope-dot-project" aria-hidden="true"></span>
          <span>项目共享</span>
          <span class="composer-skill-scope-count">{{ projectSkills.length }}</span>
        </button>
        <button
          class="composer-skill-selector-tab"
          :class="{ selected: scope === 'personal' }"
          data-testid="launch-skill-scope-tab-personal"
          type="button"
          @click="updateScope('personal')"
        >
          <span class="composer-skill-scope-dot composer-skill-scope-dot-personal" aria-hidden="true"></span>
          <span>私人使用</span>
          <span class="composer-skill-scope-count">{{ systemSkills.length }}</span>
        </button>
      </div>
      <div class="composer-skill-selector-list">
        <button
          v-for="entry in skillEntries"
          :key="entry.key"
          class="composer-skill-selector-item"
          :class="entryClass(entry)"
          type="button"
          :disabled="entry.autoApplied"
          :title="entryReason(entry)"
          @click="toggleEntry(entry)"
        >
          <span v-if="entry.selected" class="composer-skill-selector-item-check" aria-hidden="true">✓</span>
          <span class="composer-skill-selector-item-name">{{ entry.name }}</span>
          <span class="composer-skill-selector-item-reason">{{ entryReason(entry) }}</span>
        </button>
        <span v-if="!loading && skillEntries.length === 0" class="composer-skill-selector-empty">暂无可选技能</span>
      </div>
    </div>
  `,
};
