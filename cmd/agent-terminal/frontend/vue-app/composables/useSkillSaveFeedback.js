import { ref } from '../../lib/vue.esm-browser.prod.js';
import { normalizePathKey } from '../utils/skill-parser.js';

function normalizeSavedScope(scope) {
  const value = (scope || '').toString().trim().toLowerCase();
  return value === 'personal' || value === 'user' || value === 'signed' || value === 'system' ? 'personal' : 'project';
}

function normalizeSavedPersonalType(scope, personalType) {
  if (normalizeSavedScope(scope) !== 'personal') return '';
  return (personalType || '').toString().trim().toLowerCase() || 'user';
}

function dirFromSkillFilePath(path) {
  const value = (path || '').toString().trim();
  const index = Math.max(value.lastIndexOf('/'), value.lastIndexOf('\\'));
  return index > 0 ? value.slice(0, index) : '';
}

function globalNoticeAfterCardSave(message) {
  const text = (message || '').toString().trim();
  if (text === '已保存' || text.startsWith('文件已保存')) return '';
  if (text.startsWith('已保存。')) {
    return text.replace(/^已保存。\s*/, '').trim();
  }
  if (text.startsWith('已保存；') || text.startsWith('已保存;')) {
    return text.replace(/^已保存[；;]\s*/, '').trim();
  }
  return text;
}

export function useSkillSaveFeedback({ editor, sourcePath, activeSkillFilePath }) {
  const recentlySavedSkill = ref(null);
  let recentlySavedTimer = null;

  function currentSavedSkillSnapshot() {
    const scope = normalizeSavedScope(editor.form.scope);
    const path = (sourcePath.value || activeSkillFilePath.value || '').toString().trim();
    return {
      name: (editor.form.name || editor.selectedSkillName.value || '').toString().trim(),
      scope,
      personal_type: normalizeSavedPersonalType(scope, editor.form.personal_type),
      dir: dirFromSkillFilePath(path),
    };
  }

  function markRecentlySavedSkill(snapshot) {
    const name = (snapshot?.name || '').toString().trim();
    if (!name) return;
    recentlySavedSkill.value = { ...snapshot, name };
    if (recentlySavedTimer) clearTimeout(recentlySavedTimer);
    recentlySavedTimer = setTimeout(() => {
      recentlySavedSkill.value = null;
      recentlySavedTimer = null;
    }, 2400);
  }

  function isSkillCardRecentlySaved(item) {
    const saved = recentlySavedSkill.value;
    if (!saved) return false;
    const savedName = (saved.name || '').toString().trim().toLowerCase();
    const itemName = (item?.name || '').toString().trim().toLowerCase();
    if (!savedName || savedName !== itemName) return false;
    const savedScope = normalizeSavedScope(saved.scope);
    const itemScope = normalizeSavedScope(item?.scope);
    if (savedScope !== itemScope) return false;
    const savedPersonalType = normalizeSavedPersonalType(savedScope, saved.personal_type);
    const itemPersonalType = normalizeSavedPersonalType(itemScope, item?.personal_type || item?.personalType);
    if (savedPersonalType !== itemPersonalType) return false;
    const savedDir = normalizePathKey(saved.dir || '');
    const itemDir = normalizePathKey(item?.dir || '');
    return !savedDir || !itemDir || savedDir === itemDir;
  }

  async function onSaveSkill() {
    const beforeSave = currentSavedSkillSnapshot();
    await editor.onSaveSkill();
    if (editor.notice.level !== 'success') return;
    const afterSave = currentSavedSkillSnapshot();
    markRecentlySavedSkill(afterSave.name ? afterSave : beforeSave);
    const nextNoticeMessage = globalNoticeAfterCardSave(editor.notice.message);
    if (nextNoticeMessage !== editor.notice.message) {
      editor.setNotice('info', nextNoticeMessage);
    }
  }

  return {
    isSkillCardRecentlySaved,
    onSaveSkill,
  };
}
