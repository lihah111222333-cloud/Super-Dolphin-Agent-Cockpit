import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import { firstPresentText } from '../shared/pageShared.js';
import { settingsPageService } from './services/settingsPageService.js';
import { PROVIDER_LABELS, isCurrentPreferenceRequest } from './settingsPageRuntime.js';
import { runBackgroundAction, runUIAction } from '../../shared/ui/runUIAction.js';

const { readBuiltinTools, writeBuiltinTool } = settingsPageService;

function useBuiltinToolsSettings(cwd, copy) {
  const builtinsCopy = copy.builtins;
  const [tools, setTools] = useState([]);
  const [loading, setLoading] = useState(false);
  const [savingIds, setSavingIds] = useState({});
  const [expandedGroups, setExpandedGroups] = useState({});
  const [notice, setNotice] = useState({ level: 'info', message: '' });
  const loadRequestSeq = useRef(0);
  const nextLoadRequest = useCallback(() => {
    loadRequestSeq.current += 1;
    const requestSeq = loadRequestSeq.current;
    return () => loadRequestSeq.current === requestSeq;
  }, []);
  const applyPayload = useCallback((payload) => setTools(normalizeBuiltinTools(payload)), []);
  const load = useCallback(() => runUIAction('settings.builtin.load', () => loadBuiltinTools({ applyPayload, copy, cwd, isCurrent: nextLoadRequest(), setLoading, setNotice }), { retryable: true }), [applyPayload, copy, cwd, nextLoadRequest]);
  const toggleTool = useCallback((tool) => runUIAction('settings.builtin.toggle', () => toggleBuiltinTool({ applyPayload, copy, cwd, savingIds, setNotice, setSavingIds, setTools, tool })), [applyPayload, copy, cwd, savingIds]);
  const toggleGroup = useCallback((key) => setExpandedGroups((prev) => ({ ...prev, [key]: !prev[key] })), []);
  const groups = useMemo(() => builtinToolGroups(tools, builtinsCopy), [builtinsCopy, tools]);
  useEffect(() => { runBackgroundAction('settings.builtin.bootstrap', () => loadBuiltinTools({ applyPayload, copy, cwd, isCurrent: nextLoadRequest(), setLoading, setNotice })); }, [applyPayload, copy, cwd, nextLoadRequest]);
  return {
    expandedGroups,
    filteredCount: tools.filter((tool) => tool.replacedBy || !tool.enabled).length,
    groups,
    groupSummary: (group) => groupSummary(group, builtinsCopy),
    isOpen: (key) => Boolean(expandedGroups[key]),
    load,
    loading,
    notice,
    savingIds,
    toggleGroup,
    toggleTool,
    toolMetaText: (tool) => toolMetaText(tool, builtinsCopy),
    totalToolCount: tools.length,
    tools,
  };
}

function normalizeBuiltinTools(payload) {
  const list = Array.isArray(payload?.tools) ? payload.tools : [];
  return list.map(normalizeBuiltinTool);
}

function normalizeBuiltinTool(item) {
  return {
    id: textValue(item.id),
    label: textValue(item.label || item.id),
    description: textValue(item.description),
    enabled: Boolean(item.enabled),
    provider: textValue(item.provider || 'claude'),
    replacedBy: optionalTextValue(item.replacedBy),
    filterMode: optionalTextValue(item.filterMode),
    enforcement: optionalTextValue(item.enforcement),
  };
}

function textValue(value) {
  return value === null || value === undefined ? '' : value.toString();
}

function optionalTextValue(value) {
  const text = textValue(value);
  return text || undefined;
}

async function loadBuiltinTools(state) {
  const { applyPayload, copy, cwd, isCurrent, setLoading, setNotice } = state;
  if (!cwd) {
    if (isCurrentPreferenceRequest(isCurrent)) setLoading(false);
    return;
  }
  setLoading(true);
  try {
    const payload = await readBuiltinTools({ cwd });
    if (isCurrentPreferenceRequest(isCurrent)) {
      applyPayload(payload);
      setNotice({ level: 'info', message: '' });
    }
  } catch (error) {
    if (isCurrentPreferenceRequest(isCurrent)) setNotice({ level: 'error', message: copy.builtins.loadFailed });
    throw error;
  } finally {
    if (isCurrentPreferenceRequest(isCurrent)) setLoading(false);
  }
}

async function toggleBuiltinTool(state) {
  const { applyPayload, copy, cwd, savingIds, setNotice, setSavingIds, setTools, tool } = state;
  if (!cwd || tool.replacedBy || !tool.id || savingIds[tool.id]) return;
  const nextEnabled = !tool.enabled;
  setSavingIds((prev) => ({ ...prev, [tool.id]: true }));
  setTools((prev) => prev.map((item) => (item.id === tool.id ? { ...item, enabled: nextEnabled } : item)));
  try {
    applyPayload(await writeBuiltinTool({ cwd, id: tool.id, enabled: nextEnabled }));
    setNotice({ level: 'info', message: (tool.label || tool.id) + ' ' + (nextEnabled ? copy.builtins.enabledSuffix : copy.builtins.disabledSuffix) });
  } catch (error) {
    setTools((prev) => prev.map((item) => (item.id === tool.id ? { ...item, enabled: !nextEnabled } : item)));
    setNotice({ level: 'error', message: copy.builtins.saveFailed });
    throw error;
  } finally {
    setSavingIds((prev) => ({ ...prev, [tool.id]: false }));
  }
}

function builtinToolGroups(tools, copy) {
  const disabled = tools.filter((tool) => !tool.enabled || tool.replacedBy);
  return [
    builtinToolGroup('native-hard', copy.nativeHard, disabled.filter((tool) => builtinToolEnforcement(tool) === 'native-hard'), copy.nativeHardNote, copy),
    builtinToolGroup('effect-hard', copy.effectHard, disabled.filter((tool) => builtinToolEnforcement(tool) === 'effect-hard'), copy.effectHardNote, copy),
    builtinToolGroup('soft-audit', copy.softAudit, disabled.filter((tool) => builtinToolEnforcement(tool) === 'soft-audit'), copy.softAuditNote, copy),
    builtinUnfilteredGroup(tools, copy),
  ].filter(Boolean);
}

function builtinToolGroup(key, label, tools, note, copy) {
  if (tools.length === 0) return null;
  return { canToggle: true, disabledCount: tools.length, key, label: label + copy.countOpen + tools.length + copy.countClose, note, tools };
}

function builtinUnfilteredGroup(tools, copy) {
  const available = tools.filter((tool) => tool.enabled && !tool.replacedBy);
  if (!available.length) return null;
  return { canToggle: true, disabledCount: 0, key: 'unfiltered', label: copy.unfiltered + copy.countOpen + available.length + copy.countClose, tools: available };
}

function builtinToolEnforcement(tool) {
  const enforcement = textValue(tool.enforcement).trim();
  if (enforcement) return enforcement;
  return tool.filterMode === 'hard' ? 'native-hard' : 'soft-audit';
}

function toolStatusLabel(tool, copy) {
  if (tool.replacedBy) return copy.replaced;
  if (tool.enabled) return copy.unfiltered;
  const enforcement = builtinToolEnforcement(tool);
  if (enforcement === 'native-hard') return copy.nativeHard;
  if (enforcement === 'effect-hard') return copy.effectHard;
  return enforcement === 'soft-audit' ? copy.softAudit : copy.controlledStatus;
}

function toolMetaText(tool, copy) {
  const parts = [];
  const description = textValue(tool.description).trim();
  if (description) parts.push(description);
  const provider = firstPresentText(PROVIDER_LABELS[tool.provider], tool.provider);
  if (provider) parts.push(provider);
  parts.push(toolStatusLabel(tool, copy));
  return parts.join(' · ');
}

function groupSummary(group, copy) {
  if (group.key === 'unfiltered') return copy.availableCount.replace('{count}', group.tools.length);
  return copy.controlledCount.replace('{count}', group.disabledCount);
}

export { useBuiltinToolsSettings };
