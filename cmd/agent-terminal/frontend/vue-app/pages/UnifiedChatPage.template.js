export const template = `
    <section class="page active unified-chat-page" :class="isCmd ? 'mode-cmd' : 'mode-chat'" data-testid="chat-page">
      <ChatToolbar
        :is-cmd="isCmd"
        :active-status="activeStatus"
        :display-status-text="displayStatusText"
        :active-status-meta="activeStatusMeta"
        :use-claude-provider="useClaudeProvider"
        :selected-thread-id="selectedThreadId"
        :can-interrupt="canInterrupt"
        :recovering-selected="recoveringSelected"
        :copy-button-label="copyButtonLabel"
        :project-options="projectStore.projectOptions.value"
        :active-project="projectStore.state.active"
        :layout-mode="layoutMode" :cmd-card-cols="cmdCardCols"
        :window-cwd="windowCwd"
        :cwd-display="cwdDisplay"
        @update-project="projectStore.setActive($event)" @add-project="projectStore.quickAdd()" @remove-project="projectStore.removeProject($event)"
        @set-cmd-layout="setCmdLayout" @set-cmd-card-cols="setCmdCardCols"
        @copy-thread-info="copySelectedThreadId" @stop-selected="stopSelected"
        @toggle-provider-mode="toggleProviderMode" @launch-one="launchOne" @recover-selected="recoverSelected"
      />
      <div class="unified-main">
        <ThreadRailSidePanel
          v-if="!isCmd"
          :show-archived-thread-list="showArchivedThreadList"
          :active-chat-thread-count="activeChatThreadCount"
          :archived-chat-thread-count="archivedChatThreadCount"
          :visible-chat-thread-cards="visibleChatThreadCards"
          :thread-rail-dragging="threadRailDragging"
          :thread-rail-style="threadRailStyle"
          :editing-thread-id="editingThreadId"
          :editing-alias="editingAlias"
          :renaming-thread-id="renamingThreadId"
          :set-rename-input-ref="setRenameInputRef"
          :token-level-by-thread-id="tokenLevelByThreadId"
          :failed-auto-continue-by-thread-id="failedAutoContinueByThreadId"
          @open-new-window="openNewWindow"
          @toggle-archived-thread-list="toggleArchivedThreadList"
          @select-thread="selectThread"
          @toggle-thread-pin="toggleThreadPin"
          @toggle-thread-archive="toggleThreadArchive"
          @delete-stale-threads="deleteStaleThreads"
          @begin-inline-rename="beginInlineRename"
          @submit-inline-rename="submitInlineRename"
          @handle-inline-rename-enter="handleInlineRenameEnter"
          @cancel-inline-rename="cancelInlineRename"
          @handle-inline-rename-blur="handleInlineRenameBlur"
          @update-editing-alias="editingAlias = $event"
        />
        <div
          v-if="!isCmd"
          class="thread-rail-resizer"
          :class="{ dragging: threadRailDragging }"
          role="separator"
          aria-orientation="vertical"
          aria-label="调整会话列表宽度"
          @mousedown="onThreadRailResizeStart"
        ></div>
        <div class="unified-center">
          <section v-if="isCmd" class="cmd-card-panel">
            <div class="overview-metrics">
              <div class="metric"><strong>{{ stats.total }}</strong><span>子Agent</span></div>
              <div class="metric"><strong>{{ stats.running }}</strong><span>执行中</span></div>
              <div class="metric"><strong>{{ stats.thinking }}</strong><span>思考/回复</span></div>
              <div class="metric"><strong>{{ stats.editing }}</strong><span>改文件</span></div>
              <div class="metric"><strong>{{ stats.error }}</strong><span>异常</span></div>
            </div>

            <CmdCardGrid
              :cmd-cards="cmdCards"
              :layout-mode="layoutMode"
              :cmd-card-cols="cmdCardCols"
              @select-thread="selectThread"
              @load-card-history="loadCardHistory"
              @rename-card="renameCard"
              @stop-card="stopCard"
            />
          </section>

          <CmdOverviewPanel
            v-if="showOverview"
            :stats="stats"
            :recent-threads="recentThreads"
            :selected-thread-id="selectedThreadId"
            :get-display-name="getDisplayName"
            @select-thread="selectThread"
          />

          <section
            v-if="!isCmd && taskHandoffVisible"
            class="task-handoff-strip data-card-vue"
            :class="{ 'task-handoff-strip-collapsed': !taskStripExpanded }"
            data-testid="task-handoff-strip"
          >
            <button
              type="button"
              class="task-handoff-strip-chip"
              data-testid="task-handoff-strip-chip"
              :aria-expanded="taskStripExpanded ? 'true' : 'false'"
              @click="toggleTaskStrip"
            >
              <span class="task-handoff-strip-chip-icon" aria-hidden="true">⚡</span>
              <span class="task-handoff-strip-chip-title">任务模式 · {{ activeTask?.title || '当前任务' }}</span>
              <span v-if="taskHandoffUpdatedAt" class="task-handoff-strip-chip-meta">· 更新于 {{ taskHandoffUpdatedAt }}</span>
              <span v-if="taskHandoffError" class="task-handoff-strip-chip-badge task-handoff-strip-chip-badge-error">!</span>
              <span class="task-handoff-strip-chip-chevron" aria-hidden="true">{{ taskStripExpanded ? '▾' : '▸' }}</span>
            </button>
            <div v-if="taskStripExpanded" class="task-handoff-strip-body" data-testid="task-handoff-strip-body">
              <div class="task-handoff-strip-head">
                <div>
                  <div class="task-handoff-strip-meta">
                    <span v-if="taskHandoffUpdatedAt">更新于 {{ taskHandoffUpdatedAt }}</span>
                    <span v-if="taskHandoffUpdatedBy">· 来源 {{ taskHandoffUpdatedBy }}</span>
                  </div>
                </div>
                <div class="task-handoff-strip-actions">
                  <button
                    class="btn btn-primary btn-xs"
                    data-testid="task-handoff-new-task"
                    :disabled="continueTaskBusy || taskHandoffLoading || !taskHandoffPreview"
                    @click="startNewTaskFromHandoff"
                  >
                    以此新建任务
                  </button>
                </div>
              </div>
              <div v-if="taskHandoffError" class="task-handoff-strip-error">{{ taskHandoffError }}</div>
              <div v-else-if="taskHandoffLoading" class="task-handoff-strip-loading">正在加载任务接力摘要…</div>
              <pre v-else class="task-handoff-strip-preview">{{ taskHandoffPreview || '当前任务已建立，但还没有可读的接力摘要。完成一轮工作后系统会自动生成。' }}</pre>
            </div>
          </section>

          <div v-if="showWorkspace" class="workspace-area">

            <div ref="workspaceRef" id="agent-workspace" class="chat-workspace with-diff">
              <WorkspaceChatPanel
                :selected-thread-id="selectedThreadId"
                :split-ratio="splitRatio"
                :active-pinned-plan="activePinnedPlan"
                :no-active-thread="noActiveThread"
                :active-timeline="activeTimeline"
                :active-status="activeStatus"
                :display-status-text="displayStatusText"
                :active-status-meta="activeStatusMeta"
                :resolve-thread-display-name="resolveThreadDisplayName"
                :presence-target="presenceAnchorRef"
                :pinned-plan-card-spec="pinnedPlanCardSpec"
                :is-at-bottom="isAtBottom"
                @dismiss-pinned-plan="dismissPinnedPlan"
                @file-ref-click="onTimelineFileRefClick"
                @citation-click="onTimelineCitationClick"
                @scroll-to-bottom="scheduleScrollToBottom(true)"
                @scroll-to-top="scrollToTop"
              />
              <div class="panel-resizer" :class="{dragging}" @mousedown="onResizeStart"></div>
              <div class="workspace-right-col" :style="{ flex: '0 0 ' + (100 - splitRatio) + '%' }">
                <DiffPanel
                  :diff-text="activeDiffText"
                  :media-preview="activeMediaPreview"
                  :markdown-preview="activeMarkdownPreview"
                  :focus-file="activeDiffFocusFile"
                  :focus-line="activeDiffFocusLine"
                  :project="projectStore.state.active"
                  :projects="projectStore.state.projects"
                  @file-ref-click="onTimelineFileRefClick"
                  @citation-click="onTimelineCitationClick"
                  @preview-dirty-change="onPreviewDirtyChange"
                />

              </div>
            </div>

            <div class="workspace-bottom-row" :class="{ 'is-cmd': isCmd }" :style="activityPanelRowStyle">
              <div class="chat-composer-shell" :class="{ 'for-chat': !isCmd }" :style="chatComposerShellStyle">
                <div v-if="!isCmd" ref="presenceAnchorRef" class="chat-status-presence-anchor"></div>
                <LaunchSkillPicker
                  v-if="launchSkillSelectionEnabled && !selectedThreadId"
                  :enabled="launchSkillSelectionEnabled"
                  :skills="launchSkillPickerSkills"
                  :project-skills="launchProjectSkills"
                  :system-skills="launchSystemSkills"
                  :scope="launchSkillScope"
                  :scope-tabs-enabled="launchSkillScopeTabsEnabled"
                  :matches="launchSkillMatches"
                  :selected-skill-names="launchSelectedSkillNames"
                  :loading="launchSkillSelectionLoading"
                  @toggle-skill="toggleLaunchSelectedSkill"
                  @update:scope="setLaunchSkillScope"
                  @select-all="selectAllLaunchSuggestedSkills"
                  @clear="clearLaunchSelectedSkills"
                  @refresh="refreshLaunchSkillSelection"
                />
                <ContextUsageBanner
                  v-if="!isCmd && selectedThreadId"
                  :level="activeTokenLevel"
                  :used-percent="(activeTokenUsage && activeTokenUsage.usedPercent) || 0"
                  :used-tokens="(activeTokenUsage && activeTokenUsage.usedTokens) || 0"
                  :context-window="(activeTokenUsage && activeTokenUsage.contextWindowTokens) || 0"
                  :can-compact="canCompact"
                  :compacting="compacting"
                  :failed-info="activeAutoContinueFailed"
                  :retrying="autoContinueRetrying"
                  :retry-error="autoContinueRetryError"
                  :stuck-info="activeStuckInfo"
                  :poking-stuck="pokingStuckThread"
                  :thread-is-task="threadIsTask"
                  :promoting-task="promotingTask"
                  :promote-task-error="promoteTaskLastError"
                  @retry-stuck-thread="onRetryStuckThread"
                  @force-stuck-thread="onForceStuckThread"
                  @mark-stuck-done="onMarkStuckDone"
                  @compact="compactCurrent"
                  @fork="openForkDraftFromUI('context-banner')"
                  @retry-auto-continue="onRetryAutoContinue"
                  @promote-task="onPromoteTaskRequested"
                />
                <div
                  v-if="sendFailureNotice"
                  class="chat-send-failure-notice"
                  data-testid="chat-send-failure-notice"
                  role="alert"
                  aria-live="assertive"
                >{{ sendFailureNotice }}</div>
                <ComposerForkDraftCard
                  v-if="!isCmd"
                  :fork-draft="composer.forkDraft"
                  :submitting="forkSubmitting"
                  :error="forkError"
                  :source-thread-name="forkSourceThreadName"
                  :context-used-percent="(activeTokenUsage && activeTokenUsage.usedPercent) || 0"
                  :available-shared-files="forkAvailableSharedFiles"
                  @close="composer.closeForkDraft()"
                  @submit="submitForkThread"
                  @add-shared-file="composer.addForkSharedFile($event)"
                  @remove-shared-file="composer.removeForkSharedFile($event)"
                />
                <ComposerBar
                  ref="composerBarRef"
                  :is-cmd="isCmd"
                  :composer="composer"
                  :thread-id="selectedThreadId"
                  :launch-skill-selection-enabled="launchSkillSelectionEnabled"
                  :interruptible="canInterrupt"
                  :compacting="compacting"
                  :can-compact="canCompact"
                  :compact-result-text="compactResultText"
                  :compact-result-tone="compactResultTone"
                  :compact-success-count="compactSuccessCount"
                  :token-inline="activeTokenInline"
                  :token-tooltip="activeTokenTooltip"
                  :token-level="activeTokenLevel"
                  :disabled="false"
                  :skill-matches="composerSkillMatches"
                  :skill-matches-loading="composerSkillPreviewLoading"
                  :selected-skill-names="composerEffectiveSelectedSkillNames"
                  :thread-config-provider="threadConfigUi.meta.provider"
                  :thread-config-supports-override="threadConfigUi.meta.supportsThreadOverride"
                  :thread-config-draft-model="threadConfigUi.draft.model"
                   :thread-config-draft-effort="threadConfigUi.draft.effort"
                   :thread-config-loading="threadConfigUi.loading"
                   :thread-config-saving="threadConfigUi.saving"
                   :thread-config-notice="threadConfigUi.notice"
                   :thread-config-notice-level="threadConfigUi.noticeLevel"
                   :thread-config-meta="threadConfigUi.meta"
                   :router-preview="routerPreview"
                   :thread-is-task="threadIsTask"
                   :promoting-task="promotingTask"
                   :thread-task-id="threadTaskId"
                   :promote-task-error="promoteTaskLastError"
                   @update-thread-config-model="updateThreadConfigModel"
                  @update-thread-config-effort="updateThreadConfigEffort"
                  @save-thread-config="saveThreadConfigDraft"
                  @restore-thread-config-inherit="restoreThreadConfigInherit"
                  @toggle-skill="toggleComposerSelectedSkill"
                  @select-all-skills="selectAllComposerSuggestedSkills"
                  @clear-skills="clearComposerSelectedSkills"
                  @send="send"
                  @interrupt="interruptCurrent"
                  @compact="compactCurrent"
                  @open-fork-draft="openForkDraftFromUI('composer-bar')"
                  @promote-task="onPromoteTaskRequested"
                />
              </div>
              <div v-if="!isCmd" class="workspace-bottom-side">
                <div class="workspace-bottom-side-layer" :class="{ dragging: activityPanelDragging }">
                  <div class="activity-panel-resizer" :class="{ dragging: activityPanelDragging }" @mousedown="onActivityResizeStart"></div>
                  <ActivityPanel
                    :stats="activeActivityStats"
                    :alerts="activeAlerts"
                    :process-events="activeProcessActivity"
                  />
                </div>
              </div>
            </div>
          </div>
        </div>
      </div>
      <PathChoiceModal
        :show="showPathChoiceModal"
        :options="pathChoiceOptions"
        :title="pathChoiceTitle"
        :truncated="pathChoiceTruncated"
        :on-confirm="confirmPathChoice"
        :on-cancel="cancelPathChoice"
      />
    </section>
`;
