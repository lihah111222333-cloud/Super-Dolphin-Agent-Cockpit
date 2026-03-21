import { logDebug } from '../services/log.js';

export const CommandsPage = {
    name: 'CommandsPage',
    props: {
        commandCards: { type: Array, default: () => [] },
        prompts: { type: Array, default: () => [] },
        commandFields: { type: Array, default: () => [] },
        promptFields: { type: Array, default: () => [] },
    },
    emits: ['run-command', 'run-prompt'],
    setup(_props, { emit }) {
        void _props;
        function onRunCommand(item) {
            logDebug('page', 'commands.runCommand.click', {});
            emit('run-command', item);
        }

        function onRunPrompt(item) {
            logDebug('page', 'commands.runPrompt.click', {});
            emit('run-prompt', item);
        }

        return {
            onRunCommand,
            onRunPrompt,
        };
    },
    template: `
    <section id="page-commands" class="page active" data-testid="commands-page">
      <div class="panel-header">
        <div class="ph-bar"></div>
        <div class="ph-text"><h2>命令卡 / 提示词</h2></div>
      </div>
      <div class="split-duo" data-testid="commands-split">
        <div class="split-left" data-testid="commands-left">
          <div class="section-header">COMMANDS</div>
          <div class="panel-body" data-testid="commands-panel">
            <div v-if="commandCards.length === 0" class="empty-state" data-testid="commands-empty-state">
              <div class="es-icon">C</div>
              <h3>暂无命令卡</h3>
            </div>
            <div v-else class="data-list-vue" data-testid="commands-list">
              <article
                v-for="(item, idx) in commandCards"
                :key="item.card_key || ('cmd-' + idx)"
                class="data-card-vue"
                :data-testid="'command-card-' + idx"
              >
                <div v-for="field in commandFields" :key="field.key" class="data-row-vue">
                  <strong>{{ field.label }}</strong>
                  <span>{{ item[field.key] ?? '-' }}</span>
                </div>
                <div class="data-actions-vue">
                  <button class="btn btn-ghost btn-xs" :data-testid="'command-run-button-' + idx" @click="onRunCommand(item)">发送到当前会话</button>
                </div>
              </article>
            </div>
          </div>
        </div>
        <div class="split-divider"></div>
        <div class="split-right" data-testid="prompts-right">
          <div class="section-header">PROMPTS</div>
          <div class="panel-body" data-testid="prompts-panel">
            <div v-if="prompts.length === 0" class="empty-state" data-testid="prompts-empty-state">
              <div class="es-icon">P</div>
              <h3>暂无提示词</h3>
            </div>
            <div v-else class="data-list-vue" data-testid="prompts-list">
              <article
                v-for="(item, idx) in prompts"
                :key="item.prompt_key || ('prompt-' + idx)"
                class="data-card-vue"
                :data-testid="'prompt-card-' + idx"
              >
                <div v-for="field in promptFields" :key="field.key" class="data-row-vue">
                  <strong>{{ field.label }}</strong>
                  <span>{{ item[field.key] ?? '-' }}</span>
                </div>
                <div class="data-actions-vue">
                  <button class="btn btn-ghost btn-xs" :data-testid="'prompt-run-button-' + idx" @click="onRunPrompt(item)">发送到当前会话</button>
                </div>
              </article>
            </div>
          </div>
        </div>
      </div>
    </section>
  `,
};
