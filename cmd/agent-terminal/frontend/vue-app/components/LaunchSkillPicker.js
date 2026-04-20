import { computed } from '../../lib/vue.esm-browser.prod.js';
import {
  composerSkillMatchClass,
  composerSkillMatchReason,
  skillNameKey,
} from '../utils/skill-match-utils.js';

const EMPTY_SKILL = { name: '', summary: '', description: '', matchedBy: '' };
const EMPTY_ENTRY = { key: '', name: '', match: null, selected: false, autoApplied: false, summary: '' };
const EMPTY_PROPS = {
  selectedSkillNames: [],
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
    loading: { type: Boolean, default: false },
  },
  emits: ['toggle-skill', 'select-all', 'clear', 'refresh', 'update:scope'],
  setup(props = EMPTY_PROPS, { emit }) {
    const selectedSkillKeys = computed(() => new Set(
      props.selectedSkillNames
        .map((name = '') => skillNameKey(name))
        .filter(Boolean),
    ));

    const showScopeTabs = computed(() => props.scopeTabsEnabled && Boolean(props.scope));
    const scopedSkills = computed(() => {
      if (!showScopeTabs.value) return props.skills;
      return props.scope === 'system' ? props.systemSkills : props.projectSkills;
    });

    const skillEntries = computed(() => {
      const matchByKey = new Map();
      const entries = [{ key: '', name: '', match: null, selected: false, autoApplied: false, summary: '' }];
      entries.pop();
      const seen = new Set();
      props.matches.forEach((match = EMPTY_SKILL) => {
        const key = skillNameKey(match?.name);
        if (key && !matchByKey.has(key)) matchByKey.set(key, match);
      });
      const pushEntry = (rawSkill = EMPTY_SKILL) => {
        const name = (rawSkill?.name || rawSkill || '').toString().trim();
        const key = skillNameKey(name);
        if (!key || seen.has(key)) return;
        seen.add(key);
        const match = matchByKey.get(key) || null;
        entries.push({
          key,
          name,
          match,
          selected: selectedSkillKeys.value.has(key),
          autoApplied: match?.matchedBy === 'force',
          summary: (rawSkill?.summary || rawSkill?.description || '').toString().trim(),
        });
      };
      props.matches.forEach(pushEntry);
      scopedSkills.value.forEach(pushEntry);
      return entries.sort(sortEntries);
    });

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
      emit('toggle-skill', entry.name);
    }

    function updateScope(nextScope) {
      emit('update:scope', nextScope);
    }

    return {
      showScopeTabs,
      scopedSkills,
      skillEntries,
      entryReason,
      entryClass,
      toggleEntry,
      updateScope,
      onSelectAll: () => emit('select-all'),
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
          {{ loading ? '首发技能准备中…' : ('Launch 技能 ' + selectedSkillNames.length + '/' + skillEntries.length) }}
        </span>
        <div v-if="showScopeTabs" class="composer-skill-selector-tabs" data-testid="launch-skill-scope-tabs">
          <button
            class="composer-skill-selector-btn"
            :class="{ selected: scope === 'project' }"
            data-testid="launch-skill-scope-tab-project"
            type="button"
            @click="updateScope('project')"
          >project {{ projectSkills.length }}</button>
          <button
            class="composer-skill-selector-btn"
            :class="{ selected: scope === 'system' }"
            data-testid="launch-skill-scope-tab-system"
            type="button"
            @click="updateScope('system')"
          >system {{ systemSkills.length }}</button>
        </div>
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
          :disabled="selectedSkillNames.length === 0"
          @click="onClear"
        >清空</button>
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
          <span class="composer-skill-selector-item-name">{{ entry.name }}</span>
          <span class="composer-skill-selector-item-reason">{{ entryReason(entry) }}</span>
        </button>
        <span v-if="!loading && skillEntries.length === 0" class="composer-skill-selector-empty">暂无可选技能</span>
      </div>
    </div>
  `,
};
