import { callAPI } from '../services/api.js';
import { logWarn } from '../services/log.js';
import { requireSkillsCwd } from './useSkillEditorHelpers.js';
import { resolveRenderedMarkdownAction } from '../utils/assistant-markdown-click.js';
import {
  inferSkillNameFromPath,
  fileNameFromPath,
  isSkillMainFilePath,
  normalizePathKey,
  skillDirFromFilePath,
} from '../utils/skill-parser.js';

function withSkillsCwd(deps, payload = {}) {
  return { ...payload, cwd: requireSkillsCwd({ cwd: deps?.activeCwdSource?.value }) };
}

/**
 * 管理 SkillsPage 的子文件打开与 Markdown 预览跳转。
 *
 * @param {object} deps
 */
export function useSkillFileNavigation(deps) {
  async function onOpenSkillSubfile(file) {
    const path = (file?.path || '').toString().trim();
    if (!path) return;
    try {
      if (isSkillMainFilePath(path)) {
        await deps.readSkillFile(path, deps.form.name || deps.selectedSkillName.value || '');
        deps.setNotice('info', '已切换到主文件 SKILL.md');
        return;
      }
      const raw = await callAPI('skills/local/read', withSkillsCwd(deps, { path }));
      deps.form.body = (raw?.skill?.content || '').toString();
      deps.sourcePath.value = path;
      deps.activeSkillFilePath.value = path;
      deps.setNotice('info', `已加载子文件：${file.name || fileNameFromPath(path)}`);
    } catch (error) {
      logWarn('skills', 'load.subfile.failed', { error, path });
      deps.setNotice('error', `读取子文件失败：${error?.message || error}`);
    }
  }

  function resolveSkillPreviewFile(rawPath) {
    const input = (rawPath || '').toString().trim();
    if (!input) return null;
    const candidates = new Set([normalizePathKey(input), normalizePathKey(input.replace(/^\.\//, ''))]);
    const activePath = (deps.activeSkillFilePath.value || deps.sourcePath.value || '').toString().trim();
    const activeDir = skillDirFromFilePath(activePath);
    if (activeDir && !/^(?:[A-Za-z]:[\\/]|[\\/])/.test(input)) {
      const joined = `${activeDir.replace(/[\\/]+$/g, '')}/${input.replace(/^\.\//, '')}`;
      candidates.add(normalizePathKey(joined));
    }
    const files = Array.isArray(deps.skillFiles.value) ? deps.skillFiles.value : [];
    const exact = files.find((file) => candidates.has(normalizePathKey(file?.path)));
    if (exact) return exact;
    const basename = fileNameFromPath(input).toLowerCase();
    if (!basename) return null;
    const byName = files.filter((file) => fileNameFromPath(file?.path).toLowerCase() === basename);
    return byName.length === 1 ? byName[0] : null;
  }

  function resolveSkillCardForCitation(payload) {
    const rawName = (payload?.skillName || '').toString().trim().toLowerCase();
    const rawPath = (payload?.path || '').toString().trim();
    const inferredName = rawPath && /(^|[\\/])SKILL\.md$/i.test(rawPath)
      ? inferSkillNameFromPath(skillDirFromFilePath(rawPath)).toLowerCase()
      : '';
    const pathKey = normalizePathKey(rawPath);
    const cards = Array.isArray(deps.skillCards.value) ? deps.skillCards.value : [];
    if (pathKey) {
      const byPath = cards.find((item) => {
        const dirKey = normalizePathKey(item?.dir || '');
        return dirKey && (pathKey === `${dirKey}/skill.md` || pathKey.startsWith(`${dirKey}/`));
      });
      if (byPath) return byPath;
    }
    const byName = cards.filter((item) => {
      const itemNameKey = (item?.name || '').toString().trim().toLowerCase();
      if (rawName && itemNameKey === rawName) return true;
      if (inferredName && itemNameKey === inferredName) return true;
      return false;
    });
    return byName.length === 1 ? byName[0] : null;
  }

  async function onSkillPreviewClick(event) {
    const action = resolveRenderedMarkdownAction(event);
    if (!action) return;
    if (typeof event?.preventDefault === 'function') event.preventDefault();
    if (typeof event?.stopPropagation === 'function') event.stopPropagation();
    const payload = action.payload || {};
    const kind = (payload.kind || '').toString().trim();
    const targetPath = (action.type === 'file-ref' ? payload.path : (payload.path || '')).toString().trim();
    const targetFile = resolveSkillPreviewFile(targetPath);
    if (targetFile) {
      await onOpenSkillSubfile(targetFile);
      return;
    }
    if (kind === 'skill') {
      const targetSkill = resolveSkillCardForCitation(payload);
      if (targetSkill) {
        await deps.onEditSkill(targetSkill);
        return;
      }
      deps.setNotice('info', '未找到引用的技能，无法跳转。');
      return;
    }
    if (kind === 'conversation') {
      deps.setNotice('info', '技能预览暂不支持会话跳转，请前往聊天页查看。');
      return;
    }
    if (kind) {
      deps.setNotice('info', '该引用已识别，但当前页面仅支持打开技能文件或技能定义。');
      return;
    }
    deps.setNotice('info', '当前预览仅支持打开本技能目录内的引用文件。');
  }

  return {
    onOpenSkillSubfile,
    onSkillPreviewClick,
  };
}
