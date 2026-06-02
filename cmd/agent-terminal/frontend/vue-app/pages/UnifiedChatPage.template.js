export const template = `
    <section class="page active unified-chat-page" :class="isCmd ? 'mode-cmd' : 'mode-chat'" data-testid="chat-page">
      <ChatToolbar
        :is-cmd="isCmd"
        :active-status="activeStatus"
        :display-status-text="displayStatusText"
        :active-status-meta="activeStatusMeta"
        :use-claude-provider="useClaudeProvider"
        :provider-preference-ready="providerPreferenceReady"
        :provider-preference-error="providerPreferenceError"
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
                :empty-text="chatEmptyText"
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
                <ContextUsageBanner
                  v-if="!isCmd && selectedThreadId"
                  :level="activeTokenLevel"
                  :used-percent="(activeTokenUsage && activeTokenUsage.usedPercent) || 0"
                  :used-tokens="(activeTokenUsage && activeTokenUsage.usedTokens) || 0"
                  :context-window="(activeTokenUsage && activeTokenUsage.contextWindowTokens) || 0"
                  :can-compact="canCompact"
                  :compacting="compacting"
                  @compact="compactCurrent"
                  @fork="openForkDraftFromUI('context-banner')"
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
                  :interruptible="canInterrupt"
                  :compacting="compacting"
                  :can-compact="canCompact"
                  :compact-result-text="compactResultText"
                  :compact-result-tone="compactResultTone"
                  :compact-success-count="compactSuccessCount"
                  :token-inline="activeTokenInline"
                  :token-tooltip="activeTokenTooltip"
                  :token-level="activeTokenLevel"
                  :send-disabled="activeThreadSendBlocked"
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
                   @update-thread-config-model="updateThreadConfigModel"
                  @update-thread-config-effort="updateThreadConfigEffort"
                  @save-thread-config="saveThreadConfigDraft"
                  @restore-thread-config-inherit="restoreThreadConfigInherit"
                  @send="send"
                  @interrupt="interruptCurrent"
                  @compact="compactCurrent"
                  @open-fork-draft="openForkDraftFromUI('composer-bar')"
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
