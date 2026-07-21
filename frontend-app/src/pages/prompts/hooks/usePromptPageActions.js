import { useEffect, useRef } from "react";
import {
  copyTextToClipboard,
  deletePrompt,
  discardPromptIntent,
  getPrompt,
  setPreference,
  writePrompt,
} from "../services/promptPageService.js";
import { runUIAction } from "../../../shared/ui/runUIAction.js";
import { ACTIVE_PROMPT_PREF_KEY } from "../model/promptPageViewSchemas.js";
import { canForceLaunchPrompt } from "../model/promptPageAssetListUtils.js";
import { pendingDraftFromItem } from "../model/promptPageDraftUtils.js";
import { promptFormFromItem } from "../model/promptPageAssetFormUtils.js";
import { noticeText } from "../model/promptPageErrorUtils.js";
import {
  firstText,
  parseJsonObjectForEditor,
  rawTextValue,
  textValue,
  wordListFromText,
} from "../model/promptPageTextUtils.js";
import { activePromptQueryKey } from "./usePromptPageQueries.js";

export function promptWritePayload(cwd, form, name, agentType, matchWhen) {
  const payload = {
    cwd,
    id: form.id,
    name,
    description: form.description,
    agentType,
    when_to_use: form.whenToUse,
    content: form.content,
    tags: wordListFromText(form.tagsText),
    enabled: form.enabled,
    scope: form.scope === "global" ? "global" : "project",
    match_when:
      form.hasMatchWhen || textValue(form.matchWhenText)
        ? matchWhen
        : undefined,
  };
  if (form.hasPriority)
    payload.priority = Number.isFinite(Number(form.priority))
      ? Number(form.priority)
      : 0;
  return payload;
}
export async function savePromptForm(options) {
  const {
    cwd,
    form,
    isCurrentRequest,
    refreshPromptSurface,
    setEditorOpen,
    setNotice,
    setSaving,
  } = options;
  const name = textValue(form.name);
  if (!name) {
    setNotice("请填写提示词名称");
    return;
  }
  const agentType = textValue(form.agentType);
  if (!agentType) {
    setNotice("请填写 Agent Key");
    return;
  }
  const parsedMatchWhen = parseJsonObjectForEditor(
    form.matchWhenText,
    "自动匹配条件",
  );
  if (parsedMatchWhen.error) {
    setNotice(parsedMatchWhen.error);
    return;
  }
  setSaving(true);
  try {
    await writePrompt(
      promptWritePayload(cwd, form, name, agentType, parsedMatchWhen.value),
    );
    if (!isCurrentRequest()) return;
    await refreshPromptSurface({ force: true });
    if (!isCurrentRequest()) return;
    setEditorOpen(false);
    setNotice(`提示词已保存：${name}`);
  } catch (err) {
    if (!isCurrentRequest()) return;
    setNotice(noticeText(err, "保存失败"));
    throw err;
  } finally {
    if (isCurrentRequest()) setSaving(false);
  }
}
export async function removePromptItem({
  cwd,
  item,
  refreshPromptSurface,
  setActioning,
  setNotice,
}) {
  setActioning(`delete:${item.id}`);
  try {
    await deletePrompt({
      cwd,
      id: item.id,
      scope: item.scope === "global" ? "global" : "project",
    });
    await refreshPromptSurface({ force: true });
    setNotice(`已删除：${item.name}`);
  } catch (err) {
    setNotice(noticeText(err, "删除失败"));
    throw err;
  } finally {
    setActioning("");
  }
}
export async function copyPromptItem({
  cwd,
  item,
  fallbackMode,
  setActioning,
  setNotice,
}) {
  if (item.isPendingDraft) {
    setNotice("这条草稿还在待确认，确认保存后才能复制内容");
    return;
  }
  setActioning(`copy:${item.id}`);
  try {
    let content = rawTextValue(item.content);
    if (!fallbackMode && item.id) {
      const response = await getPrompt({ cwd, id: item.id });
      content = firstText(
        response?.prompt?.content,
        response?.prompt?.prompt_text,
        response?.promptText,
        content,
      );
    }
    if (!textValue(content)) {
      setNotice("暂无可复制内容");
      return;
    }
    await copyTextToClipboard(content);
    setNotice("已复制提示词内容");
  } catch (err) {
    setNotice(noticeText(err, "复制失败"));
    throw err;
  } finally {
    setActioning("");
  }
}
export async function setLaunchPreference({
  cwd,
  item,
  queryClient,
  setActioning,
  setNotice,
}) {
  setActioning(`launch:${item.id}`);
  try {
    await setPreference({ cwd, key: ACTIVE_PROMPT_PREF_KEY, value: item.id });
    queryClient.setQueryData(activePromptQueryKey(cwd), item.id);
    setNotice(`已设为强制使用：${item.name}`);
  } catch (err) {
    setNotice(noticeText(err, "设置强制使用失败"));
    throw err;
  } finally {
    setActioning("");
  }
}
export async function clearLaunchPreference({
  cwd,
  queryClient,
  setActioning,
  setNotice,
}) {
  setActioning("launch:clear");
  try {
    await setPreference({ cwd, key: ACTIVE_PROMPT_PREF_KEY, value: "" });
    queryClient.setQueryData(activePromptQueryKey(cwd), "");
    setNotice("已取消强制使用，新对话将使用默认路由");
  } catch (err) {
    setNotice(noticeText(err, "取消强制使用失败"));
    throw err;
  } finally {
    setActioning("");
  }
}
export async function discardPromptDraftItem({
  cwd,
  item,
  refreshPromptSurface,
  setActioning,
  setNotice,
}) {
  const draftKey = item.draftKey || item.id;
  setActioning(`discard:${draftKey}`);
  try {
    await discardPromptIntent({ cwd, draftKey });
    await refreshPromptSurface({ force: true });
    setNotice(`已丢弃：${item.name}`);
  } catch (err) {
    setNotice(noticeText(err, "丢弃失败"));
    throw err;
  } finally {
    setActioning("");
  }
}
export function usePromptEditorActions(params) {
  const {
    cwd,
    fallbackMode,
    actioning,
    form,
    queryClient,
    refreshPromptSurface,
    setters,
  } = params;
  const { setActioning, setEditorOpen, setForm, setNotice, setSaving } =
    setters;
  const currentCwdRef = useRef(cwd);
  const saveGenerationRef = useRef(0);
  useEffect(() => {
    currentCwdRef.current = cwd;
    saveGenerationRef.current += 1;
  }, [cwd]);
  const savePrompt = () => {
    const generation = saveGenerationRef.current + 1;
    saveGenerationRef.current = generation;
    return savePromptForm({
      cwd,
      form,
      refreshPromptSurface,
      setEditorOpen,
      setNotice,
      setSaving,
      isCurrentRequest: () =>
        currentCwdRef.current === cwd &&
        saveGenerationRef.current === generation,
    });
  };
  return {
    retryPromptSync: () =>
      runUIAction(
        "prompt.surface.retry",
        () => refreshPromptSurface({ force: true }),
        { retryable: true },
      ),
    openEdit: (item) => {
      saveGenerationRef.current += 1;
      setForm(promptFormFromItem(item));
      setEditorOpen(true);
      setNotice("");
    },
    savePrompt: () => runUIAction("prompt.save", savePrompt),
    removePrompt: (item) => {
      if (fallbackMode) {
        setNotice("当前为只读降级，暂不支持删除");
        return;
      }
      if (!item.id || actioning) return;
      runUIAction("prompt.delete", () =>
        removePromptItem({
          cwd,
          item,
          refreshPromptSurface,
          setActioning,
          setNotice,
        }),
      );
    },
    copyPrompt: (item) => {
      if (!item.id || actioning) return;
      runUIAction(
        "prompt.copy",
        () =>
          copyPromptItem({ cwd, item, fallbackMode, setActioning, setNotice }),
        { retryable: true },
      );
    },
    setLaunchPrompt: (item) => {
      if (!canForceLaunchPrompt(item) || actioning) return;
      runUIAction("prompt.launch.set", () =>
        setLaunchPreference({
          cwd,
          item,
          queryClient,
          setActioning,
          setNotice,
        }),
      );
    },
    clearLaunchPrompt: () => {
      if (actioning) return;
      runUIAction("prompt.launch.clear", () =>
        clearLaunchPreference({ cwd, queryClient, setActioning, setNotice }),
      );
    },
  };
}
export function usePromptDraftActions({
  cwd,
  actioning,
  refreshPromptSurface,
  setters,
}) {
  const { setActioning, setNotice, setWizardDraft, setWizardOpen } = setters;
  return {
    continuePendingDraft: (item) => {
      setWizardDraft(pendingDraftFromItem(item));
      setWizardOpen(true);
      setNotice("");
    },
    discardDraft: (item) => {
      const draftKey = item.draftKey || item.id;
      if (!draftKey || actioning) return;
      runUIAction("prompt.draft.discard", () =>
        discardPromptDraftItem({
          cwd,
          item,
          refreshPromptSurface,
          setActioning,
          setNotice,
        }),
      );
    },
    handleWizardSaved: async () => {
      setWizardOpen(false);
      setWizardDraft(null);
      await refreshPromptSurface({ force: true });
      setNotice("已保存，可在新对话中被 AI 发现和使用");
    },
  };
}
