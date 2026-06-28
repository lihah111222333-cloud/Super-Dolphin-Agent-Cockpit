export const DIFF_PANEL_TEMPLATE = `
    <div id="diff-panel" ref="panelRef">
      <div class="diff-header">
        <div class="diff-header-main" :class="{ 'diff-header-main--icon': hasDiffPreview }">
          <template v-if="hasDiffPreview">
            <span class="diff-header-chip diff-header-chip--title">
              <span class="diff-header-icon" :title="headerIconTooltip('change')" role="img" :aria-label="headerTitle">
                <svg viewBox="0 0 24 24" focusable="false" aria-hidden="true">
                  <path :d="headerIconPath('change')"></path>
                </svg>
              </span>
            </span>
            <span class="diff-header-chip diff-header-chip--files">
              <span class="diff-header-icon" :title="headerIconTooltip('files')" role="img" :aria-label="headerSubText">
                <svg viewBox="0 0 24 24" focusable="false" aria-hidden="true">
                  <path :d="headerIconPath('files')"></path>
                </svg>
              </span>
              <strong class="diff-header-chip-count">{{ fileCountValue }}</strong>
            </span>
          </template>
          <template v-else>
            <strong>{{ headerTitle }}</strong>
            <small>{{ headerSubText }}</small>
          </template>
        </div>
        <div v-if="hasDiffPreview" class="diff-header-metrics">
          <span class="diff-metric add">+{{ totals.add }}</span>
          <span class="diff-metric del">-{{ totals.del }}</span>
        </div>
      </div>

      <div id="diff-content">
        <div v-if="hasMediaPreview" class="diff-media-card">
          <button class="diff-media-thumb-btn" type="button" @click="openLightbox" :title="mediaPath || '点击放大预览'" aria-label="放大图片预览">
            <img class="diff-media-thumb" :src="mediaThumbSrc" :alt="mediaPath || 'image preview'" />
          </button>
          <div class="diff-media-caption">
            <div class="diff-media-path" :title="mediaPath">{{ mediaPath || 'image' }}</div>
            <div v-if="mediaMeta" class="diff-media-meta">{{ mediaMeta }}</div>
          </div>
        </div>

        <div
          v-else-if="hasMarkdownPreview"
          class="diff-media-card"
          style="font-family: -apple-system, 'SF Pro Text', sans-serif; font-size: 13px; line-height: 1.62;"
        >
          <div class="diff-media-caption" style="display: flex; justify-content: space-between; align-items: flex-start; gap: 12px;">
            <div style="min-width: 0; flex: 1 1 auto;">
              <div class="diff-media-path" :title="previewPath">{{ previewPath || 'preview' }}</div>
              <div v-if="previewMeta" class="diff-media-meta">{{ previewMeta }}</div>
              <div v-if="saveError" class="diff-media-meta" style="color: #b42318;">{{ saveError }}</div>
            </div>
            <div v-if="previewEditable" style="display: flex; gap: 8px; align-items: center; flex: 0 0 auto;">
              <button v-if="!isEditing" class="btn btn-ghost btn-xs" type="button" @click="startEditing">Edit</button>
              <template v-else>
                <button class="btn btn-ghost btn-xs" type="button" @click="savePreviewChanges" :disabled="saving || !isDirty">{{ saving ? 'Saving...' : 'Save' }}</button>
                <button class="btn btn-ghost btn-xs" type="button" @click="cancelEditing" :disabled="saving">Cancel</button>
              </template>
            </div>
          </div>
          <div v-if="isEditing" style="padding: 12px 14px 14px;">
            <textarea
              ref="editorTextarea"
              v-model="draftText"
              class="diff-preview-editor"
              style="width: 100%; min-height: 200px; max-height: calc(100vh - 240px); resize: vertical; border: 1px solid var(--color-border, #d0d5dd); border-radius: 10px; padding: 12px; font: 13px/1.6 ui-monospace, 'SFMono-Regular', Menlo, Consolas, monospace; background: var(--color-panel, #fff); color: inherit; overflow-y: auto;"
              :disabled="saving"
              spellcheck="false"
              aria-label="编辑文档预览"
              @input="autoResizeEditor"
            ></textarea>
            <div class="diff-media-meta" style="padding-top: 8px;">保存后将统一写回 LF 换行符。</div>
          </div>
          <div
            v-else-if="isMarkdownPreview"
            class="chat-item-markdown agent-markdown-root"
            style="padding: 12px 14px 14px;"
            @click="onMarkdownPreviewClick"
            v-html="markdownHtml"
          ></div>
          <div
            v-else-if="isCodeTextPreview"
            class="chat-item-markdown agent-markdown-root"
            style="padding: 12px 14px 14px;"
            v-html="textPreviewHtml"
          ></div>
          <pre
            v-else
            class="diff-preview-text"
            style="margin: 0; padding: 12px 14px 14px; white-space: pre-wrap; word-break: break-word; font: 13px/1.6 ui-monospace, 'SFMono-Regular', Menlo, Consolas, monospace;"
          >{{ textPreviewPlainText }}</pre>
        </div>


        <div v-if="showLargeDiffPreview && hasDiffPreview" class="diff-empty" style="display: flex; justify-content: space-between; align-items: center; gap: 12px;">
          <span>{{ largeDiffPreviewText }}</span>
          <button class="btn btn-ghost btn-xs" type="button" @click="loadFullDiff">加载完整 Diff</button>
        </div>

        <div v-if="files.length === 0 && hasDiffPreview" class="diff-empty">暂无代码变更</div>

        <div
          v-if="hasDiffPreview"
          v-for="(file, fileIndex) in files"
          :key="fileKey(file, fileIndex)"
          class="diff-file-group"
          :class="{ 'is-focused': isFocusedFile(file), 'is-collapsed': isFileCollapsed(file, fileIndex) }"
        >
          <div class="diff-file-header">
            <button class="diff-file-toggle" type="button" @click="toggleFileCollapsed(file, fileIndex)" :aria-expanded="!isFileCollapsed(file, fileIndex)" :aria-label="fileToggleLabel(file, fileIndex)">
              <div class="diff-file-title">
                <span class="diff-file-caret" :class="{ 'is-collapsed': isFileCollapsed(file, fileIndex) }">{{ fileCaretSymbol(file, fileIndex) }}</span>
                <span class="diff-file-name" :title="displayFilePath(file)">
                  <span v-if="displayFilePathPrefix(file)" class="diff-file-name-prefix">{{ displayFilePathPrefix(file) }}</span>
                  <span class="diff-file-name-suffix">{{ displayFilePathSuffix(file) }}</span>
                </span>
              </div>
              <div class="diff-file-stats">
                <span class="diff-metric add">+{{ diffStats(file).add }}</span>
                <span class="diff-metric del">-{{ diffStats(file).del }}</span>
              </div>
            </button>
            <button class="diff-file-copy-btn" type="button" @click.stop="copyFilePath(file)" :title="isCopiedFile(file) ? '已复制路径' : '复制路径'" :aria-label="isCopiedFile(file) ? '已复制路径' : '复制路径'">
              <svg viewBox="0 0 24 24" focusable="false" aria-hidden="true">
                <path v-if="isCopiedFile(file)" d="M5 12l4 4 10-10"></path>
                <path v-else d="M9 9h10v12H9zM5 3h10v12"></path>
              </svg>
            </button>
          </div>
          <div v-show="!isFileCollapsed(file, fileIndex)" class="diff-file-lines">
            <div
              v-for="(line, idx) in file.lines"
              :key="line.type + '-' + (line.oldNo || line.newNo || idx)"
              class="diff-line"
              :class="[line.type, { 'is-focused-line': isFocusedLine(file, line) }]"
            >
              <span class="diff-line-num old">{{ line.oldNo }}</span>
              <span class="diff-line-num new">{{ line.newNo }}</span>
              <span class="diff-line-prefix">{{ linePrefix(line.type) }}</span>
              <span class="diff-line-content">{{ line.text }}</span>
            </div>
          </div>
        </div>

        <div v-if="hasMediaPreview && lightboxOpen" class="diff-media-lightbox" @click.self="closeLightbox">
          <div class="diff-media-lightbox-inner">
            <button class="diff-media-lightbox-close" type="button" @click="closeLightbox" aria-label="关闭预览">×</button>
            <img class="diff-media-full" :src="mediaFullSrc" :alt="mediaPath || 'image preview'" />
            <div class="diff-media-lightbox-path" :title="mediaPath">{{ mediaPath || 'image' }}</div>
          </div>
        </div>
      </div>
    </div>
`;
