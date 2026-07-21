import React, { useCallback, useEffect, useState } from "react";
import { Dialog, Modal, ModalOverlay } from "react-aria-components";
import { requiredAppStoragePort } from "../../shared/api/browser/browserStorage.js";
import { PromptScopeChoice } from "./PromptPageWizardUi.jsx";

function optionalPromptDebugStorageEnabled() {
  if (typeof window === "undefined") return false;
  if (window.__SUPER_DOLPHIN_PROMPT_DEBUG__ === true) return true;
  try {
    return (
      requiredAppStoragePort("prompt debug storage").get(
        "super-dolphin.promptDebug",
      ) === "1"
    );
  } catch {
    return false;
  }
}

export function PromptAriaModal(props) {
  const {
    ariaLabel,
    children,
    className = "modal-box",
    closeDisabled = false,
    closeOnOverlayClick = false,
    onClose,
    overlayClassName = "modal-overlay",
  } = props;
  const [restoreFocusElement] = useState(() => {
    const activeElement = document.activeElement;
    return activeElement instanceof HTMLElement ? activeElement : null;
  });
  useEffect(
    () => () => {
      if (restoreFocusElement && document.contains(restoreFocusElement))
        restoreFocusElement.focus();
    },
    [restoreFocusElement],
  );
  const handleOpenChange = useCallback(
    (isOpen) => {
      if (!isOpen && !closeDisabled && typeof onClose === "function") onClose();
    },
    [closeDisabled, onClose],
  );
  return (
    <ModalOverlay
      className={overlayClassName}
      isOpen
      isDismissable={closeOnOverlayClick && !closeDisabled}
      isKeyboardDismissDisabled={closeDisabled}
      onOpenChange={handleOpenChange}
    >
      {" "}
      <Modal className={className}>
        {" "}
        <Dialog aria-label={ariaLabel}>{children}</Dialog>{" "}
      </Modal>{" "}
    </ModalOverlay>
  );
}
export function PromptEditorModal(props) {
  return <PromptEditorModalContent {...props} />;
}

function PromptEditorModalContent(props) {
  const { form, notice, saving, onChange, onClose, onSave } = props;
  const update = (key) => (event) => {
    const { type, checked, value } = event.target;
    onChange({
      ...form,
      [key]: type === "checkbox" ? checked : value,
      ...(key === "priority" ? { hasPriority: true } : {}),
    });
  };
  const scopeLabel = form.scope === "global" ? "全局可用" : "这个项目";
  const scopeHint =
    form.scope === "global"
      ? "说明：其他项目也可以使用；当前项目同名内容优先。"
      : "说明：只在当前项目的对话中使用。";
  const previewText =
    form.content ||
    form.whenToUse ||
    form.description ||
    "已保存，AI 会在相关场景中使用";
  const advancedDebugAvailable = optionalPromptDebugStorageEnabled();
  return (
    <PromptAriaModal
      ariaLabel="编辑提示词"
      className="modal-box prompt-editor-modal"
      overlayClassName="modal-overlay prompt-modal-overlay"
      closeDisabled={saving}
      closeOnOverlayClick
      onClose={onClose}
    >
      {" "}
      <header>
        {" "}
        <div>
          {" "}
          <h2>编辑提示词</h2> <p>{scopeLabel}</p>{" "}
        </div>{" "}
      </header>
      <div className="prompt-scope-copy">
        {" "}
        <div>可用范围：{scopeLabel}</div>{" "}
        <PromptScopeChoice
          ariaLabel="可用范围"
          autoFocusProject
          scope={form.scope}
          onChange={(value) => onChange({ ...form, scope: value })}
        />{" "}
        <div>{scopeHint}</div>{" "}
      </div>{" "}
      <div className="prompt-editor-grid">
        <label>
          名称
          <input
            value={form.name}
            onChange={update("name")}
            aria-label="名称"
          />
        </label>{" "}
        <label className="wide">
          一句话描述
          <input
            value={form.description}
            onChange={update("description")}
            aria-label="一句话描述"
          />
        </label>
        <label className="wide">
          AI 什么时候会使用它
          <textarea
            value={form.whenToUse}
            onChange={update("whenToUse")}
            aria-label="AI 什么时候会使用它"
          />
        </label>{" "}
        <label className="wide">
          AI 使用时怎么做
          <textarea
            value={form.content}
            onChange={update("content")}
            aria-label="AI 使用时怎么做"
          />
        </label>
        <label className="wide">
          保存后 AI 会看到什么
          <textarea
            className="prompt-preview-readonly"
            value={previewText}
            aria-label="保存后 AI 会看到什么"
            readOnly
          />
        </label>{" "}
        <label className="prompt-check">
          <input
            type="checkbox"
            checked={form.enabled}
            onChange={update("enabled")}
          />{" "}
          启用状态
        </label>
      </div>{" "}
      <PromptEditorAdvancedFields
        advancedDebugAvailable={advancedDebugAvailable}
        form={form}
        update={update}
      />
      {notice ? <div className="prompt-notice">{notice}</div> : null}{" "}
      <footer>
        {" "}
        <button
          type="button"
          className="ghost"
          onClick={onClose}
          disabled={saving}
        >
          取消
        </button>{" "}
        <button type="button" onClick={onSave} disabled={saving}>
          {saving ? "保存中..." : "保存"}
        </button>
      </footer>{" "}
    </PromptAriaModal>
  );
}

function PromptEditorAdvancedFields({ advancedDebugAvailable, form, update }) {
  if (!advancedDebugAvailable) return null;
  return (
    <details className="prompt-advanced-debug">
      {" "}
      <summary>高级调试</summary>{" "}
      <div className="prompt-editor-grid prompt-advanced-grid">
        {" "}
        <label>
          Agent Key
          <input
            value={form.agentType}
            onChange={update("agentType")}
            aria-label="Agent Key"
          />
        </label>
        <label>
          场景标签
          <input
            value={form.tagsText}
            onChange={update("tagsText")}
            aria-label="场景标签"
          />
        </label>{" "}
        <label>
          排序权重
          <input
            type="number"
            value={form.priority}
            onChange={update("priority")}
            aria-label="排序权重"
          />
        </label>
        <label className="wide">
          match_when JSON
          <textarea
            value={form.matchWhenText}
            onChange={update("matchWhenText")}
            aria-label="match_when JSON"
          />
        </label>{" "}
      </div>{" "}
    </details>
  );
}
