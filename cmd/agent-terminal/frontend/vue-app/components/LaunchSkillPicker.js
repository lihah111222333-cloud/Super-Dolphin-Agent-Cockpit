// @ts-nocheck
import { computed } from '../../lib/vue.esm-browser.prod.js';
import {
  composerSkillMatchClass,
  composerSkillMatchReason,
  skillNameKey,
} from '../utils/skill-match-utils.js';

export const LaunchSkillPicker = {
  name: 'LaunchSkillPicker',
  props: {
    skills: { type: Array, default: () => [] },
    matches: { type: Array, default: () => [] },
    selectedSkillNames: { type: Array, default: () => [] },
    loading: { type: Boolean, default: false },
    enabled: { type: Boolean, default: false },
  },
  emits: ['toggle-skill', 'select-all', 'clear', 'refresh'],
  setup(props, { emit }) {
    const catalogItems = computed(() => {
      const next = [];
      const seen = new Set();
      const matchByName = new Map();
      props.matches.forEach((match) => {
        const key = skillNameKey(match?.name);
        if (key) matchByName.set(key, match);
      });
      const pushItem = (skill, match = null) => {
        const name = (skill?.name || match?.name || '').toString().trim();
        const key = skillNameKey(name);
        if (!key || seen.has(key)) return;
        seen.add(key);
        const effectiveMatch = match || matchByName.get(key) || null;
        const matchClass = effectiveMatch ? composerSkillMatchClass(effectiveMatch) : '';
        const subtitle = effectiveMatch
          ? composerSkillMatchReason(effectiveMatch)
          : ((skill?.summary || skill?.description || '').toString().trim() || '手动选择');
        next.push({
          key,
          name,
          autoApplied: matchClass === 'force',
          matchClass,
          subtitle,
        });
      };
      props.matches.forEach((match) => pushItem(null, match));
      props.skills.forEach((skill) => pushItem(skill));
      return next;
    });

    function isSelected(rawName) {
      const key = skillNameKey(rawName);
      return key ? props.selectedSkillNames.some((name) => skillNameKey(name) === key) : false;
    }

    return {
      catalogItems,
      isSelected,
      onToggleSkill: (name) => emit('toggle-skill', name),
      onSelectAll: () => emit('select-all'),
      onClear: () => emit('clear'),
      onRefresh: () => emit('refresh'),
    };
  },
  template: `
    <section
      v-if="enabled"
      class="launch-skill-picker composer-skill-selector"
      :class="{ 'is-expanded': catalogItems.length > 8 }"
      data-testid="launch-skill-picker"
    >
      <div class="composer-skill-selector-head">
        <span class="composer-skill-selector-title" :class="{ 'loading-shimmer': loading }">
          {{ loading ? 'Launch 技能加载中…' : ('Launch 技能选择 ' + selectedSkillNames.length + '/' + catalogItems.length) }}
        </span>
        <button class="composer-skill-selector-btn" type="button" @click="onRefresh">刷新</button>
        <button class="composer-skill-selector-btn" type="button" :disabled="catalogItems.length === 0" @click="onSelectAll">全选</button>
        <button class="composer-skill-selector-btn" type="button" :disabled="selectedSkillNames.length === 0" @click="onClear">清空</button>
      </div>
      <div v-if="matches.length > 0" class="composer-skill-selector-empty">
        已匹配 {{ matches.length }} 个推荐技能；强制命中的技能会自动选中。
      </div>
      <div class="composer-skill-selector-list">
        <button
          v-for="item in catalogItems"
          :key="item.key"
          class="composer-skill-selector-item"
          :class="[item.matchClass, { selected: isSelected(item.name) }]"
          type="button"
          :disabled="item.autoApplied"
          :title="item.subtitle"
          @click="onToggleSkill(item.name)"
        >
          <span class="composer-skill-selector-item-name">{{ item.name }}</span>
          <span class="composer-skill-selector-item-reason">{{ item.subtitle }}</span>
        </button>
        <span v-if="!loading && catalogItems.length === 0" class="composer-skill-selector-empty">暂无可选技能</span>
      </div>
    </section>
  `,
};
