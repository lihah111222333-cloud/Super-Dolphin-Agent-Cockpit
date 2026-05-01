import { computed, nextTick, reactive, ref, watch } from '../../lib/vue.esm-browser.prod.js';
import { callAPI, selectProjectDirs } from '../services/api.js';
import { logInfo, logWarn } from '../services/log.js';
import { importSkills, writeSkill } from '../services/skills-api.js';
import { renderAssistantMarkdown } from '../utils/assistant-markdown.js';
import {
  listToText, inferSkillNameFromPath, summarizeItems,
  normalizePathKey, fileNameFromPath, isSkillMainFilePath,
  parseSkillMarkdown, buildSkillMarkdown,
} from '../utils/skill-parser.js';

function updateNotice(notice, level, message) {
  notice.level = level || 'info';
  notice.message = (message || '').toString();
}

function resolveSkillsCwd(props) {
  const activeProject = (props?.projectStore?.state?.active || '').toString().trim();
  return activeProject && activeProject !== '.' ? activeProject : '';
}

function withSkillsCwd(props, payload = {}) {
  const cwd = resolveSkillsCwd(props);
  return cwd ? { ...payload, cwd } : payload;
}

function normalizeSkillScope(scope) {
  return (scope || '').toString().trim().toLowerCase() === 'system' ? 'system' : 'project';
}

function scopeFromTrust(trust) {
  const normalized = (trust || '').toString().trim().toLowerCase();
  return normalized === 'user' || normalized === 'signed' ? 'system' : 'project';
}

function applyParsedSkillState(state, parsed, rawContent, path = '', fallbackSummary = '', fallbackSource = '') {
  state.form.name = parsed.name || state.form.name || '';
  state.form.description = parsed.description || '';
  state.form.summary = parsed.summary || fallbackSummary || parsed.description || '';
  if (parsed.summary) {
    state.summarySource.value = 'frontmatter';
  } else if (fallbackSource) {
    state.summarySource.value = fallbackSource;
  } else if (fallbackSummary) {
    state.summarySource.value = 'generated';
  } else if (parsed.description) {
    state.summarySource.value = 'description';
  } else {
    state.summarySource.value = '';
  }
  state.form.triggerWordsText = listToText(parsed.triggerWords);
  state.form.forceWordsText = listToText(parsed.forceWords);
  state.form.body = (parsed.body || '').trim();
  state.sourcePath.value = path;
  state.activeSkillFilePath.value = path;
  state.selectedSkillName.value = state.form.name || state.selectedSkillName.value;
  logInfo('skills', 'editor.skill.loaded', {
    name: state.form.name,
    source_path: path,
    body_len: rawContent.length,
  });
}

function createSkillFileReaders(props, state) {
  async function loadSkillFiles(dir, preferredPath = '') {
    const raw = await callAPI('skills/local/listFiles', withSkillsCwd(props, { dir }));
    const files = Array.isArray(raw?.files) ? raw.files : [];
    const normalized = files
      .map((item) => ({
        name: (item?.name || '').toString().trim(),
        path: (item?.path || '').toString().trim(),
        isMain: Boolean(item?.is_main),
      }))
      .filter((item) => item.name && item.path);
    state.skillFiles.value = normalized;
    if (normalized.length === 0) {
      state.activeSkillFilePath.value = '';
      return;
    }
    const preferredKey = normalizePathKey(preferredPath);
    const nextActive = normalized.find((item) => normalizePathKey(item.path) === preferredKey)
      || normalized.find((item) => item.isMain)
      || normalized[0];
    if (nextActive?.path) {
      state.activeSkillFilePath.value = nextActive.path;
    }
  }

  async function readSkillFile(path, fallbackName = '', fallbackSummary = '', fallbackSource = '') {
    const raw = await callAPI('skills/local/read', withSkillsCwd(props, { path }));
    const content = (raw?.skill?.content || '').toString();
    if (!content.trim()) {
      throw new Error('读取的技能文件为空');
    }
    const serverSummary = (raw?.skill?.summary || '').toString().trim();
    const serverSummarySource = (raw?.skill?.summary_source || '').toString().trim();
    const parsed = parseSkillMarkdown(content, fallbackName);
    const finalFallbackSummary = serverSummary || fallbackSummary;
    const finalFallbackSource = serverSummarySource || fallbackSource;
    applyParsedSkillState(state, parsed, content, path, finalFallbackSummary, finalFallbackSource);
    if (!parsed.summary && finalFallbackSummary) {
      state.setNotice('info', '系统已生成摘要，你可以在编辑后保存为自定义摘要。');
    }
  }

  return { loadSkillFiles, readSkillFile };
}

function createImportActions(props, emit, deps, state, readers) {
  async function onUploadSkill() {
    if (state.uploading.value) return;
    state.uploading.value = true;
    state.importFailures.value = [];
    try {
      const folderPaths = await selectProjectDirs();
      if (!Array.isArray(folderPaths) || folderPaths.length === 0) {
        state.setNotice('info', '未选择目录');
        return;
      }

      const selectedNames = folderPaths
        .map((entryPath) => inferSkillNameFromPath(entryPath))
        .filter(Boolean);
      const selectedNameSeen = new Set();
      const duplicatedNameSet = new Set();
      for (const name of selectedNames) {
        const key = name.toLowerCase();
        if (selectedNameSeen.has(key)) {
          duplicatedNameSet.add(name);
          continue;
        }
        selectedNameSeen.add(key);
      }
      const duplicatedNames = Array.from(duplicatedNameSet);
      if (duplicatedNames.length > 0) {
        state.setNotice('error', `选择目录中存在重复技能名：${summarizeItems(duplicatedNames)}`);
        return;
      }

      const existingNameSet = new Set(
        deps.skillCards.value.map((item) => (item?.name || '').toString().toLowerCase()).filter(Boolean),
      );
      const overwriteNames = selectedNames.filter((name) => existingNameSet.has(name.toLowerCase()));
      if (overwriteNames.length > 0) {
        state.setNotice('info', `将覆盖已有技能：${summarizeItems(overwriteNames)}，继续导入中...`);
      }

      const imported = await importSkills(resolveSkillsCwd(props), folderPaths, state.importScope.value);
      const importedSkills = Array.isArray(imported?.imported)
        ? imported.imported
        : (Array.isArray(imported?.skills) ? imported.skills : []);
      const failures = Array.isArray(imported?.failures) ? imported.failures : [];
      state.importFailures.value = failures.map((item) => {
        const source = (item?.source || '').toString().trim();
        const message = (item?.error || '未知错误').toString().trim();
        return `${source || '-'}：${message || '未知错误'}`;
      });
      const firstSkill = importedSkills[0] || null;

      emit('refresh-skills');
      if (firstSkill?.skill_file) {
        await readers.readSkillFile(firstSkill.skill_file, firstSkill.name || '');
        state.form.scope = normalizeSkillScope(state.importScope.value);
      }
      if (failures.length > 0) {
        state.setNotice('error', `导入完成：成功 ${importedSkills.length}，失败 ${failures.length}`);
        return;
      }
      if (importedSkills.length === 0) {
        state.setNotice('info', '未导入任何技能目录');
        return;
      }
      state.setNotice('success', `已导入 ${importedSkills.length} 个技能目录（含资源文件）`);
    } catch (error) {
      logWarn('skills', 'upload.failed', { error });
      state.setNotice('error', `导入目录失败：${error?.message || error}`);
    } finally {
      state.uploading.value = false;
    }
  }

  return { onUploadSkill };
}

function createEditorActions(props, emit, state, readers) {
  function onCreateSkill() {
    state.selectedSkillName.value = '';
    state.summarySource.value = '';
    state.sourcePath.value = '';
     state.skillFiles.value = [];
     state.activeSkillFilePath.value = '';
     state.form.name = '';
     state.form.description = '';
     state.form.summary = '';
     state.form.triggerWordsText = '';
     state.form.forceWordsText = '';
     state.form.body = '';
     state.form.scope = 'project';
     state.isBodyEditing.value = true;
    state.bodyEditorFocused.value = false;
    state.isEditorOpen.value = true;
    state.setNotice('info', '已打开新建表单，填写后点击保存。');
    nextTick(() => {
      const node = state.bodyInputRef.value;
      if (node && typeof node.focus === 'function') node.focus();
    });
  }

  async function onEditSkill(item) {
    if (!item?.dir) return;
    const skillPath = `${item.dir}/SKILL.md`;
    try {
      await readers.readSkillFile(skillPath, item.name || '', item.summary || '', item.summary ? 'generated' : '');
      let filesLoadErrorMessage = '';
      try {
        await readers.loadSkillFiles(item.dir, skillPath);
      } catch (error) {
        filesLoadErrorMessage = (error?.message || error || '').toString();
        state.skillFiles.value = [];
        state.activeSkillFilePath.value = skillPath;
        logWarn('skills', 'load.subfiles.failed', { error, dir: item.dir, path: skillPath });
      }
       state.selectedSkillName.value = item.name || '';
       state.form.scope = normalizeSkillScope(scopeFromTrust(item?.trust));
       state.isBodyEditing.value = false;
      state.bodyEditorFocused.value = false;
      state.isEditorOpen.value = true;
      if (filesLoadErrorMessage) {
        state.setNotice('error', `主文件已加载，但子文件列表读取失败：${filesLoadErrorMessage}`);
      } else {
        state.setNotice('info', `已加载技能：${item.name || ''}`);
      }
    } catch (error) {
      logWarn('skills', 'load.savedSkill.failed', { error, path: skillPath });
      state.setNotice('error', `读取技能失败：${error?.message || error}`);
    }
  }

  function isDeletingSkill(name) {
    return (state.deletingSkillName.value || '').toLowerCase() === (name || '').toString().toLowerCase();
  }

  function onDeleteSkill(item) {
    const skillName = (item?.name || '').toString().trim();
    if (!skillName || state.deletingSkillName.value) return;
    state.confirmDeleteTarget.value = item;
  }

  function cancelSkillDelete() {
    state.confirmDeleteTarget.value = null;
  }

  async function confirmSkillDelete() {
    const item = state.confirmDeleteTarget.value;
    const skillName = (item?.name || '').toString().trim();
    if (!skillName) return;
    state.confirmDeleteTarget.value = null;
    state.deletingSkillName.value = skillName;
    try {
      await callAPI('skills/local/delete', withSkillsCwd(props, { name: skillName }));
      const skillKey = skillName.toLowerCase();
      if ((state.selectedSkillName.value || '').toLowerCase() === skillKey) {
        state.selectedSkillName.value = '';
      }
      if ((state.form.name || '').toLowerCase() === skillKey) {
        state.form.name = '';
        state.form.description = '';
        state.form.summary = '';
        state.form.triggerWordsText = '';
        state.form.forceWordsText = '';
        state.form.body = '';
        state.form.scope = 'project';
        state.summarySource.value = '';
        state.sourcePath.value = '';
        state.skillFiles.value = [];
        state.activeSkillFilePath.value = '';
      }
      if (!state.selectedSkillName.value) {
        state.isEditorOpen.value = false;
      }
      emit('refresh-skills');
      state.setNotice('success', `技能已删除：${skillName}`);
    } catch (error) {
      logWarn('skills', 'delete.failed', { error, skill: skillName });
      state.setNotice('error', `删除技能失败：${error?.message || error}`);
    } finally {
      state.deletingSkillName.value = '';
    }
  }

  async function onSaveSkill() {
    const targetPath = (state.activeSkillFilePath.value || state.sourcePath.value || '').toString().trim();
    state.saving.value = true;
    try {
      if (!state.resolvedIsEditingMainSkillFile.value) {
        if (!targetPath) {
          throw new Error('缺少子文件路径，无法保存');
        }
        await writeSkill(resolveSkillsCwd(props), targetPath, (state.form.body || '').toString(), normalizeSkillScope(state.form.scope));
        state.setNotice('success', `子文件已保存：${fileNameFromPath(targetPath) || targetPath}`);
        return;
      }

      const name = (state.form.name || '').trim();
      if (!name) {
        state.setNotice('error', '请先填写技能名称');
        return;
      }
      const content = buildSkillMarkdown(state.form);
      state.form.scope = normalizeSkillScope(state.form.scope);
      const saved = await writeSkill(resolveSkillsCwd(props), name, content, state.form.scope);
      state.selectedSkillName.value = name;
      state.summarySource.value = 'frontmatter';
      if (saved?.path) {
        state.sourcePath.value = saved.path;
        state.activeSkillFilePath.value = saved.path;
      }
      emit('refresh-skills');
      state.setNotice('success', `技能已保存：${name}（${state.form.scope === 'system' ? 'system' : 'project'}）`);
    } catch (error) {
      logWarn('skills', 'save.failed', {
        error,
        name: (state.form.name || '').trim(),
        path: targetPath,
        main_file: state.resolvedIsEditingMainSkillFile.value,
      });
      state.setNotice('error', `保存失败：${error?.message || error}`);
    } finally {
      state.saving.value = false;
    }
  }

  function closeEditor() {
    state.isEditorOpen.value = false;
    state.isBodyEditing.value = false;
    state.bodyEditorFocused.value = false;
  }

  function startBodyEdit() {
    state.isBodyEditing.value = true;
    nextTick(() => {
      const node = state.bodyInputRef.value;
      if (node && typeof node.focus === 'function') node.focus();
    });
  }

  function finishBodyEdit() {
    state.isBodyEditing.value = false;
    state.bodyEditorFocused.value = false;
  }

  function onBodyFocus() {
    state.bodyEditorFocused.value = true;
  }

  function onBodyBlur() {
    state.bodyEditorFocused.value = false;
  }

  return {
    onCreateSkill,
    onEditSkill,
    isDeletingSkill,
    onDeleteSkill,
    confirmSkillDelete,
    cancelSkillDelete,
    onSaveSkill,
    closeEditor,
    startBodyEdit,
    finishBodyEdit,
    onBodyFocus,
    onBodyBlur,
  };
}

/**
 * 管理 SkillsPage 的编辑器状态、CRUD 与目录导入。
 *
 * @param {object} props
 * @param {(event: string, ...args: any[]) => void} emit
 * @param {{ skillCards: any, isEditingMainSkillFile?: any, sourcePath?: any, activeSkillFilePath?: any }} deps
 */
export function useSkillEditor(props, emit, deps) {
  const selectedSkillName = ref('');
  const summarySource = ref('');
  const sourcePath = deps.sourcePath || ref('');
  const skillFiles = ref([]);
  const activeSkillFilePath = deps.activeSkillFilePath || ref('');
  const importFailures = ref([]);
  const notice = reactive({ level: 'info', message: '' });
  const saving = ref(false);
  const uploading = ref(false);
  const deletingSkillName = ref('');
  const confirmDeleteTarget = ref(null);
  const isEditorOpen = ref(false);
  const isBodyEditing = ref(false);
  const bodyEditorFocused = ref(false);
  const bodyInputRef = ref(null);
  const importScope = ref('project');
  const form = reactive({
    name: '',
    description: '',
    summary: '',
    triggerWordsText: '',
    forceWordsText: '',
    body: '',
    scope: 'project',
  });

  const summarySourceLabel = computed(() => {
    const source = (summarySource.value || '').toLowerCase();
    if (source === 'frontmatter') return '用户摘要';
    if (source === 'description') return '系统生成（基于描述）';
    if (source === 'generated') return '系统生成（基于正文）';
    return '系统生成';
  });

  const skillBodyMarkdownHtml = computed(() => {
    const text = (form.body || '').toString().trim();
    if (!text) return '<p>暂无内容，点击“编辑正文”开始编写。</p>';
    return renderAssistantMarkdown(text);
  });

  const isEditingMainSkillFile = computed(() => {
    const candidate = (activeSkillFilePath.value || sourcePath.value || '').toString().trim();
    if (!candidate) return true;
    return isSkillMainFilePath(candidate);
  });
  const resolvedIsEditingMainSkillFile = deps.isEditingMainSkillFile || isEditingMainSkillFile;

  const state = {
    selectedSkillName,
    summarySource,
    sourcePath,
    skillFiles,
    activeSkillFilePath,
    importFailures,
    notice,
    saving,
    uploading,
    deletingSkillName,
    confirmDeleteTarget,
    isEditorOpen,
    isBodyEditing,
    bodyEditorFocused,
    bodyInputRef,
    importScope,
    form,
    resolvedIsEditingMainSkillFile,
    setNotice: (level, message) => updateNotice(notice, level, message),
  };

  const readers = createSkillFileReaders(props, state);
  const importActions = createImportActions(props, emit, deps, state, readers);
  const editorActions = createEditorActions(props, emit, state, readers);

  watch(deps.skillCards, (nextCards) => {
    const current = (selectedSkillName.value || '').toString().trim().toLowerCase();
    if (!current) return;
    const exists = nextCards.some((item) => (item?.name || '').toString().trim().toLowerCase() === current);
    if (!exists) {
      selectedSkillName.value = '';
    }
  });

  watch(deps.skillCards, (nextCards) => {
    const currentPathKey = normalizePathKey(sourcePath.value || '');
    if (!currentPathKey) return;
    const exists = nextCards.some((item) => {
      const dirKey = normalizePathKey(item?.dir || '');
      if (!dirKey) return false;
      return currentPathKey === `${dirKey}/skill.md` || currentPathKey.startsWith(`${dirKey}/`);
    });
    if (!exists) {
      selectedSkillName.value = '';
      isEditorOpen.value = false;
      skillFiles.value = [];
      activeSkillFilePath.value = '';
    }
  });

  watch(() => form.scope, (next) => {
    const normalized = normalizeSkillScope(next);
    if (normalized !== next) form.scope = normalized;
  }, { immediate: true });

  watch(importScope, (next) => {
    const normalized = normalizeSkillScope(next);
    if (normalized !== next) importScope.value = normalized;
  }, { immediate: true });

  return {
    selectedSkillName,
    summarySource,
    summarySourceLabel,
    sourcePath,
    skillFiles,
    activeSkillFilePath,
    isEditingMainSkillFile: resolvedIsEditingMainSkillFile,
    importFailures,
    notice,
    saving,
    uploading,
    deletingSkillName,
    confirmDeleteTarget,
    isEditorOpen,
    isBodyEditing,
    bodyEditorFocused,
    bodyInputRef,
    importScope,
    form,
    skillBodyMarkdownHtml,
    setNotice: state.setNotice,
    ...readers,
    ...importActions,
    ...editorActions,
  };
}
