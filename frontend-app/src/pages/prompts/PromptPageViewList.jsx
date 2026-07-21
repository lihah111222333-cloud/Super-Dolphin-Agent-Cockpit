import React from "react";
import { APP_COPY } from "../../shared/i18n/appI18n.js";

function promptBucket(item) {
  if (item.isPendingDraft) return "pending";
  return item.assetType === "recall" || item.assetType === "default_rule"
    ? item.assetType
    : "expert";
}
function canForceLaunchPrompt(item) {
  return (
    promptBucket(item) === "expert" &&
    item.enabled !== false &&
    !item.isPendingDraft
  );
}
function promptLifecycleStatus(item, active) {
  if (item.isPendingDraft) return "pending";
  if (item.enabled === false) return "disabled";
  return active ? "started" : "created";
}
function promptLifecycleStatusLabel(status) {
  if (status === "created") return "已创建";
  if (status === "started") return "已启动";
  if (status === "disabled") return "已停用";
  if (status === "pending") return "";
  throw new TypeError(`unsupported prompt lifecycle status: ${String(status)}`);
}
function promptBucketLabel(bucket) {
  return (
    {
      expert: "专家能力",
      recall: "参考资料",
      default_rule: "默认规则",
    }[bucket] || "待确认"
  );
}
function trunc(value, max = 120) {
  const text =
    value === null || value === undefined ? "" : String(value).trim();
  if (!text) return "暂无内容";
  return text.length > max ? `${text.slice(0, max)}...` : text;
}

export function PromptStatusMessages(props) {
  const {
    copy,
    isProjectPending,
    fallbackMode,
    syncError,
    error,
    loading,
    onRetry,
  } = props;
  return (
    <>
      {" "}
      {isProjectPending ? (
        <div className="prompt-notice">{copy.connecting}</div>
      ) : null}{" "}
      {fallbackMode ? (
        <div className="prompt-notice warn">{copy.fallbackNotice}</div>
      ) : null}{" "}
      {syncError ? (
        <PromptRetryNotice copy={copy} message={syncError} onRetry={onRetry} />
      ) : null}
      {error ? (
        <PromptRetryNotice copy={copy} message={error} onRetry={onRetry} />
      ) : null}{" "}
      {loading ? (
        <output className="prompt-loading" aria-live="polite">
          {copy.loading}
        </output>
      ) : null}{" "}
    </>
  );
}
function PromptRetryNotice({ copy = APP_COPY.zh.prompts, message, onRetry }) {
  return (
    <div className="prompt-notice error" role="alert">
      {" "}
      <span>{message}</span>{" "}
      <button type="button" className="ghost" onClick={onRetry}>
        {copy.retrySync}
      </button>{" "}
    </div>
  );
}
export function PromptCardsGrid(props) {
  const {
    visibleItems,
    activePromptId,
    actioning,
    fallbackMode,
    editorActions,
    draftActions,
  } = props;
  return (
    <div className="prompt-card-grid">
      {" "}
      {visibleItems.map((item, index) => (
        <PromptCard
          key={item.id || index}
          item={item}
          active={activePromptId === item.id && canForceLaunchPrompt(item)}
          actioning={actioning}
          fallbackMode={fallbackMode}
          onEdit={editorActions.openEdit}
          onCopy={editorActions.copyPrompt}
          onDelete={editorActions.removePrompt}
          onSetLaunch={editorActions.setLaunchPrompt}
          onClearLaunch={editorActions.clearLaunchPrompt}
          onContinueDraft={draftActions.continuePendingDraft}
          onDiscardDraft={draftActions.discardDraft}
        />
      ))}{" "}
    </div>
  );
}

function PromptBadges({ item, active }) {
  const bucket = promptBucket(item);
  const lifecycleStatus = promptLifecycleStatus(item, active);
  const lifecycleLabel = promptLifecycleStatusLabel(lifecycleStatus);
  return (
    <div className="prompt-badges">
      {" "}
      <span>{promptBucketLabel(bucket)}</span>{" "}
      {item.scope === "global" ? <span>全局可用</span> : null}{" "}
      {item.isPendingDraft ? <span>待确认</span> : null}{" "}
      {!item.isPendingDraft && lifecycleLabel ? (
        <span>{lifecycleLabel}</span>
      ) : null}
      {active ? <span className="active">强制使用</span> : null}{" "}
    </div>
  );
}
function PromptTagRow({ tags }) {
  if (!tags.length) return null;
  return (
    <div className="prompt-tag-row">
      {" "}
      {tags.map((tag) => (
        <span key={tag}>{tag}</span>
      ))}{" "}
    </div>
  );
}
function PromptForceAction({
  item,
  active,
  actioning,
  onSetLaunch,
  onClearLaunch,
}) {
  if (active) {
    return (
      <button
        type="button"
        className="ghost"
        disabled={actioning === "launch:clear"}
        onClick={onClearLaunch}
      >
        取消强制
      </button>
    );
  }
  if (!canForceLaunchPrompt(item)) return null;
  return (
    <button
      type="button"
      className="ghost"
      disabled={Boolean(actioning)}
      onClick={() => onSetLaunch(item)}
    >
      强制使用
    </button>
  );
}
function PromptPendingActions({
  item,
  actioning,
  onContinueDraft,
  onDiscardDraft,
}) {
  const discardKey = item.draftKey || item.id;
  return (
    <>
      {" "}
      <button type="button" onClick={() => onContinueDraft(item)}>
        继续确认
      </button>{" "}
      <button
        type="button"
        className="ghost danger"
        disabled={actioning === `discard:${discardKey}`}
        onClick={() => onDiscardDraft(item)}
      >
        {" "}
        {actioning === `discard:${discardKey}` ? "丢弃中..." : "丢弃"}
      </button>{" "}
    </>
  );
}
function PromptSavedActions(props) {
  const {
    item,
    active,
    actioning,
    fallbackMode,
    onEdit,
    onCopy,
    onSetLaunch,
    onClearLaunch,
  } = props;
  return (
    <>
      {" "}
      <button type="button" onClick={() => onEdit(item)}>
        {fallbackMode ? "查看" : "编辑"}
      </button>{" "}
      <button
        type="button"
        className="ghost"
        disabled={actioning === `copy:${item.id}`}
        onClick={() => onCopy(item)}
      >
        {" "}
        {actioning === `copy:${item.id}` ? "复制中..." : "复制"}{" "}
      </button>
      <PromptForceAction
        item={item}
        active={active}
        actioning={actioning}
        onSetLaunch={onSetLaunch}
        onClearLaunch={onClearLaunch}
      />{" "}
    </>
  );
}
function PromptCardActions(props) {
  if (props.item.isPendingDraft) {
    return <PromptPendingActions {...props} />;
  }
  return <PromptSavedActions {...props} />;
}
function PromptCard(props) {
  const { item, active } = props;
  return (
    <article
      className={`prompt-card ${item.enabled === false && !item.isPendingDraft ? "disabled" : ""} ${item.isPendingDraft ? "pending" : ""}`}
    >
      {" "}
      <div className="prompt-card-head">
        {" "}
        <h3>{item.name || "未命名"}</h3>{" "}
        <PromptBadges item={item} active={active} />{" "}
      </div>
      {item.description ? (
        <p className="prompt-card-desc">{item.description}</p>
      ) : null}{" "}
      <PromptTagRow tags={item.tags} />{" "}
      <p className="prompt-card-preview">{trunc(item.preview)}</p>{" "}
      <div className="prompt-card-actions">
        {" "}
        <PromptCardActions {...props} />{" "}
      </div>{" "}
    </article>
  );
}
