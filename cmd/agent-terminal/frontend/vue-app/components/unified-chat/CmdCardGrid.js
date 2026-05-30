// @ts-nocheck

export const CmdCardGrid = {
  name: 'CmdCardGrid',
  props: {
    cmdCards: { type: Array, default: () => [] },
    layoutMode: { type: String, default: '' },
    cmdCardCols: { type: Number, default: 3 },
  },
  emits: ['select-thread', 'load-card-history', 'rename-card', 'stop-card'],
  setup(_, { emit }) {
    return { emit };
  },
  template: `
    <div class="cmd-card-grid" :class="'cols-' + cmdCardCols">
      <article
        v-for="card in cmdCards"
        :key="card.id"
        class="cmd-thread-card"
        :class="['view-' + layoutMode, { active: card.selected }]"
        @click="emit('select-thread', card.id)"
      >
        <header class="cmd-thread-card-head">
          <div>
            <strong>{{ card.name }}</strong>
            <small>{{ card.id }}</small>
          </div>
          <span class="badge" :class="'badge-' + card.status">
            {{ card.statusHeader }}
          </span>
          <span v-if="card.provider" class="thread-cli-badge" :class="'cli-' + card.provider">{{ card.provider === 'claude' ? 'Claude' : 'Codex' }}</span>

          <span v-else-if="card.agentTitle || card.agentKey || card.promptKey" class="thread-agent-badge" :title="'路由 agent：' + (card.agentKey || '-') + (card.promptKey ? (' / prompt：' + card.promptKey) : '')">{{ card.agentTitle || card.agentKey || card.promptKey }}</span>

          <span v-if="card.cwdMismatch" class="thread-cwd-mismatch-badge" :title="card.cwdMismatchReason || 'CWD 不匹配'">⚠ CWD</span>
        </header>

        <div v-if="layoutMode !== 'overview'" class="cmd-thread-preview">
          <p v-if="!card.selected" class="muted">点击卡片查看预览</p>
          <template v-else>
            <p v-for="line in card.preview" :key="line.key">{{ line.text }}</p>
            <p v-if="card.preview.length === 0" class="muted">暂无消息</p>
          </template>
        </div>

        <pre v-if="layoutMode === 'mix' && card.selected && card.diff" class="cmd-thread-diff">{{ card.diff }}</pre>

        <div class="cmd-thread-actions">
          <button class="btn btn-ghost btn-xs" @click.stop="emit('select-thread', card.id)">打开</button>
          <button class="btn btn-ghost btn-xs" @click.stop="emit('load-card-history', card.id)">历史</button>
          <button class="btn btn-ghost btn-xs" @click.stop="emit('rename-card', card.id)">改名</button>
          <button
            class="btn btn-ghost btn-xs"
            :disabled="!card.interruptible"
            :title="card.interruptible ? '中断该 Agent 当前执行' : '当前没有可中断任务'"
            @click.stop="emit('stop-card', card.id)"
          >停止</button>
        </div>
      </article>
    </div>
  `,
};
