import { useCallback, useEffect, useId, useMemo, useState } from 'react';
import {
  parseSlashCommandTrigger,
  rankSlashCommandItems,
  replaceSlashCommandTrigger,
  slashCommandOptionId,
} from '../model/slashCommandModel.js';
import { slashCommandCatalogService } from '../services/slashCommandCatalogService.js';
import { useSlashCommandCatalog } from './useSlashCommandCatalog.js';

function focusTextarea(textareaRef) {
  if (textareaRef.current) textareaRef.current.focus();
}

function errorMessage(error) {
  if (error instanceof Error) return error.message;
  return String(error);
}

function selectedCapabilities(store) {
  if (!Array.isArray(store.composerCapabilities)) {
    throw new TypeError('composer capabilities must be an array');
  }
  return store.composerCapabilities.length > 0;
}

function enabledItems(items) {
  return items.filter((item) => !item.disabled);
}

function nextActiveId(items, activeId, direction) {
  const enabled = enabledItems(items);
  if (enabled.length === 0) return '';
  const currentIndex = enabled.findIndex((item) => item.id === activeId);
  if (currentIndex < 0) return direction > 0 ? enabled[0].id : enabled.at(-1).id;
  const nextIndex = (currentIndex + direction + enabled.length) % enabled.length;
  return enabled[nextIndex].id;
}

function activeIdForQuery(items, selection, query) {
  const enabled = enabledItems(items);
  if (selection.query === query && enabled.some((item) => item.id === selection.id)) {
    return selection.id;
  }
  if (enabled.length === 0) return '';
  return enabled[0].id;
}

function selectBuiltin(item, store) {
  if (item.payload.action === 'new') store.newThread();
  if (item.payload.action === 'clear') store.clearComposer();
}

function selectCapability(item, draft, setDraft, store) {
  store.addComposerCapability(item.payload.capability);
  setDraft(replaceSlashCommandTrigger(draft, ''));
}

function selectAutomation(item, draft, setDraft) {
  const { content, title } = item.payload;
  setDraft(replaceSlashCommandTrigger(draft, `${title}\n\n${content}`));
}

async function selectPrompt(item, context) {
  const { cwd, draft, service, setDraft, store } = context;
  try {
    const content = await service.loadPromptContent(cwd, item.payload.promptId);
    setDraft(replaceSlashCommandTrigger(draft, content));
    return true;
  }
  catch (error) {
    store.notifyAction(errorMessage(error), 'error');
    return false;
  }
}

async function applySelection(item, context) {
  const { draft, setDraft, store } = context;
  if (item.kind === 'builtin') selectBuiltin(item, store);
  if (item.kind === 'skill' || item.kind === 'mcp_tool') {
    selectCapability(item, draft, setDraft, store);
  }
  if (item.kind === 'prompt') return selectPrompt(item, context);
  if (item.kind === 'automation') selectAutomation(item, draft, setDraft);
  return true;
}

function eventIsComposing(event, options) {
  return Boolean(options.isComposing)
    || Boolean(event.nativeEvent?.isComposing)
    || event.keyCode === 229;
}

export function useSlashCommandPalette(options) {
  const {
    copy,
    cwd,
    draft,
    service = slashCommandCatalogService,
    setDraft,
    store,
    textareaRef,
  } = options;
  const reactId = useId();
  const listboxId = `slash-command-listbox-${reactId.replace(/:/g, '')}`;
  const [activeSelection, setActiveSelection] = useState({ id: '', query: '' });
  const [dismissedDraft, setDismissedDraft] = useState(null);
  const [selecting, setSelecting] = useState(false);
  const trigger = parseSlashCommandTrigger(draft);
  const open = trigger !== null && dismissedDraft !== draft;
  const catalog = useSlashCommandCatalog({
    copy,
    cwd,
    hasSelectedCapabilities: selectedCapabilities(store),
    paletteOpen: open,
    service,
    store,
  });
  const query = trigger === null ? '' : trigger.query;
  const items = useMemo(
    () => rankSlashCommandItems(catalog.items, query),
    [catalog.items, query],
  );
  const activeId = activeIdForQuery(items, activeSelection, query);
  const activeItem = items.find((item) => item.id === activeId);
  const activeOptionId = open && activeItem ? slashCommandOptionId(activeItem) : '';

  useEffect(() => {
    if (dismissedDraft !== null && dismissedDraft !== draft) setDismissedDraft(null);
  }, [dismissedDraft, draft]);

  useEffect(() => {
    if (!open || (activeSelection.id === activeId && activeSelection.query === query)) return;
    setActiveSelection({ id: activeId, query });
  }, [activeId, activeSelection, open, query]);

  const selectItem = useCallback(async (item) => {
    if (item.disabled || selecting) return false;
    setSelecting(true);
    try {
      const selected = await applySelection(item, {
        cwd,
        draft,
        service,
        setDraft,
        store,
      });
      if (selected && item.kind === 'builtin') setDismissedDraft(draft);
      if (selected) focusTextarea(textareaRef);
      return selected;
    }
    finally {
      setSelecting(false);
    }
  }, [cwd, draft, selecting, service, setDraft, store, textareaRef]);

  const setActiveId = useCallback((id) => {
    setActiveSelection({ id, query });
  }, [query]);

  const moveActive = useCallback((direction) => {
    setActiveSelection({ id: nextActiveId(items, activeId, direction), query });
  }, [activeId, items, query]);

  const handleKeyDown = useCallback((event, keyOptions = {}) => {
    if (eventIsComposing(event, keyOptions) || !open) return false;
    if (event.key === 'ArrowDown' || event.key === 'ArrowUp') {
      event.preventDefault();
      moveActive(event.key === 'ArrowDown' ? 1 : -1);
      return true;
    }
    if (event.key === 'Escape') {
      event.preventDefault();
      setDismissedDraft(draft);
      return true;
    }
    if (event.key !== 'Enter' && event.key !== 'Tab') return false;
    if (!activeItem || activeItem.disabled) return false;
    event.preventDefault();
    void selectItem(activeItem);
    return true;
  }, [activeItem, draft, moveActive, open, selectItem]);

  return {
    open,
    listboxId,
    activeId,
    activeOptionId,
    items,
    categoryStates: catalog.categoryStates,
    selecting,
    handleKeyDown,
    selectItem,
    setActiveId,
  };
}
