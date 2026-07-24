import { useCallback, useMemo, useState } from 'react';
import { errorMessage, optionalSettingsCwd } from '../shared/pageShared.js';
import { fetchSkillsDashboard, trimmedText } from './skillsDashboardModel.js';

function skillToolsQueryKey(cwd) {
  // 与 SkillsPage.jsx 中 SkillToolsView 使用的查询键保持一致，确保失效能命中同一 query。
  return ['skillTools', cwd];
}

function normalizeRegisteredNames(response) {
  const tools = Array.isArray(response?.tools) ? response.tools : [];
  return tools.map((tool) => trimmedText(tool?.methodName)).filter(Boolean);
}

function firstPresentText(...values) {
  for (const value of values) {
    const text = trimmedText(value);
    if (text) return text;
  }
  return '';
}

function uniqueNamedSkills(skills) {
  const counts = new Map();
  skills.forEach((skill) => counts.set(skill.name, (counts.get(skill.name) ?? 0) + 1));
  return skills.filter((skill) => counts.get(skill.name) === 1);
}

// “注册技能工具”的状态机：只能把当前项目真实存在的 Skill 注册为动态工具。
// 打开对话框时经真实 API 加载技能列表与已注册工具；保存前再次拉取确认，
// 不依赖过期 UI 状态，杜绝注册出没有对应 SKILL.md 的不可调用工具。
export function useSkillToolRegistration({ createTool, listTools, projectPath, queryClient, setPanelNotice }) {
  const [toolEditorOpen, setToolEditorOpen] = useState(false);
  const [loadState, setLoadState] = useState({ status: 'idle', skills: [], registeredNames: [], error: '' });
  const [selection, setSelection] = useState('');
  const [description, setDescription] = useState('');
  const [enabled, setEnabled] = useState(true);
  const [toolSaving, setToolSaving] = useState(false);
  const [saveError, setSaveError] = useState('');

  const openEditor = useCallback(async () => {
    setSelection('');
    setDescription('');
    setEnabled(true);
    setSaveError('');
    setToolEditorOpen(true);
    const cwd = optionalSettingsCwd(projectPath);
    if (!cwd) {
      setLoadState({ status: 'error', skills: [], registeredNames: [], error: '请先在聊天页选择项目' });
      return;
    }
    setLoadState({ status: 'loading', skills: [], registeredNames: [], error: '' });
    try {
      const [skills, toolsResponse] = await Promise.all([
        fetchSkillsDashboard(cwd),
        listTools({ cwd, keyword: '', limit: 200 }),
      ]);
      const registeredNames = normalizeRegisteredNames(toolsResponse);
      setLoadState({ status: 'ready', skills, registeredNames, error: '' });
      const firstAvailable = uniqueNamedSkills(skills).find((skill) => !registeredNames.includes(skill.name));
      if (firstAvailable) {
        setSelection(firstAvailable.name);
        setDescription(firstPresentText(firstAvailable.description, firstAvailable.summary));
      }
    } catch (error) {
      setLoadState({ status: 'error', skills: [], registeredNames: [], error: errorMessage(error) });
    }
  }, [listTools, projectPath]);

  const closeEditor = useCallback(() => {
    if (!toolSaving) setToolEditorOpen(false);
  }, [toolSaving]);

  const selectSkill = useCallback((name) => {
    setSelection(trimmedText(name));
    setLoadState((current) => {
      const skill = current.skills.find((item) => item.name === trimmedText(name));
      if (skill) setDescription(firstPresentText(skill.description, skill.summary));
      return current;
    });
  }, []);

  const saveTool = useCallback(async () => {
    if (toolSaving) return;
    const cwd = optionalSettingsCwd(projectPath);
    if (!cwd) {
      setSaveError('请先在聊天页选择项目');
      return;
    }
    if (!selection) {
      setSaveError('请选择要注册的技能');
      return;
    }
    const desc = description.trim();
    if (!desc) {
      setSaveError('请填写工具描述');
      return;
    }
    setToolSaving(true);
    setSaveError('');
    try {
      // 保存前重新拉取真实数据：确认所选 Skill 仍存在、且未被并发注册。
      const [freshSkills, freshToolsResponse] = await Promise.all([
        fetchSkillsDashboard(cwd),
        listTools({ cwd, keyword: '', limit: 200 }),
      ]);
      const freshRegisteredNames = normalizeRegisteredNames(freshToolsResponse);
      const refreshed = { status: 'ready', skills: freshSkills, registeredNames: freshRegisteredNames, error: '' };
      const matchingSkills = freshSkills.filter((skill) => skill.name === selection);
      if (matchingSkills.length === 0) {
        setLoadState(refreshed);
        setSelection('');
        setSaveError(`技能「${selection}」已不存在，请重新选择`);
        return;
      }
      if (matchingSkills.length > 1) {
        setLoadState(refreshed);
        setSelection('');
        setSaveError(`技能「${selection}」存在同名冲突，无法注册为工具`);
        return;
      }
      if (freshRegisteredNames.includes(selection)) {
        setLoadState(refreshed);
        setSaveError(`技能「${selection}」已注册为工具，无需重复注册`);
        return;
      }
      await createTool({ cwd, methodName: selection, description: desc, enabled });
      setToolEditorOpen(false);
      setPanelNotice(`已注册工具：${selection}`);
      await queryClient.invalidateQueries({ queryKey: skillToolsQueryKey(cwd) });
    } catch (error) {
      setSaveError(`注册工具失败：${errorMessage(error)}`);
      throw error;
    } finally {
      setToolSaving(false);
    }
  }, [createTool, description, enabled, listTools, projectPath, queryClient, selection, setPanelNotice, toolSaving]);

  const availableSkills = useMemo(
    () => uniqueNamedSkills(loadState.skills)
      .filter((skill) => !loadState.registeredNames.includes(skill.name)),
    [loadState.skills, loadState.registeredNames],
  );

  return {
    availableSkills,
    closeEditor,
    description,
    enabled,
    loadState,
    openEditor,
    registeredCount: loadState.registeredNames.length,
    saveError,
    saveTool,
    selectSkill,
    selection,
    setDescription,
    setEnabled,
    toolEditorOpen,
    toolSaving,
  };
}
