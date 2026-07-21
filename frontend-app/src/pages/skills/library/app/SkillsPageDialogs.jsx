import React from "react";
import { FocusTrapDialog } from "../../../../shared/ui/FocusTrapDialog.jsx";

export function ConfirmSkillDeleteModal({
  skill,
  deleting,
  onClose,
  onConfirm,
}) {
  return (
    <FocusTrapDialog
      ariaLabel="删除技能"
      closeDisabled={deleting}
      onClose={onClose}
    >
      <header>
        <h2>删除技能</h2>
        <button
          type="button"
          className="ghost"
          onClick={onClose}
          disabled={deleting}
        >
          关闭
        </button>
      </header>
      <p>
        确定删除技能 “{skill.name}
        ”吗？该操作会删除技能目录及其资源文件，无法恢复。
      </p>
      <p className="path">{skill.dir || "-"}</p>
      <footer>
        <button
          type="button"
          className="ghost"
          onClick={onClose}
          disabled={deleting}
        >
          取消
        </button>
        <button
          type="button"
          className="text-danger"
          onClick={() => {
            void onConfirm();
          }}
          disabled={deleting}
        >
          {deleting ? "删除中..." : "确认删除"}
        </button>
      </footer>
    </FocusTrapDialog>
  );
}

export function ImportScopeModal({ importing, onClose, onConfirm }) {
  return (
    <FocusTrapDialog
      ariaLabel="导入技能"
      closeDisabled={importing}
      onClose={onClose}
    >
      <header>
        <h2>导入技能</h2>
        <button
          type="button"
          className="ghost"
          onClick={onClose}
          disabled={importing}
        >
          关闭
        </button>
      </header>
      <p>这些技能导入后给谁使用？</p>
      <footer>
        <button
          type="button"
          className="ghost"
          onClick={onClose}
          disabled={importing}
        >
          取消
        </button>
        <button
          type="button"
          onClick={() => {
            void onConfirm("personal");
          }}
          disabled={importing}
        >
          私人使用
        </button>
        <button
          type="button"
          onClick={() => {
            void onConfirm("project");
          }}
          disabled={importing}
        >
          项目共享
        </button>
      </footer>
    </FocusTrapDialog>
  );
}
