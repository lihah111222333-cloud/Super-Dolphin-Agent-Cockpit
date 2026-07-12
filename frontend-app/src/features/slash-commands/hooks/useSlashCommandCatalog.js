import { useEffect, useMemo } from 'react';
import { useQuery } from '@tanstack/react-query';
import { builtinSlashCommandItems } from '../adapters/builtinSlashCommandAdapter.js';
import { slashCommandCatalogService } from '../services/slashCommandCatalogService.js';

const EMPTY_ITEMS = Object.freeze([]);

function queryItems(query, kind) {
  if (query.data === undefined) return EMPTY_ITEMS;
  if (!Array.isArray(query.data)) {
    throw new TypeError(`slash command ${kind} query data must be an array`);
  }
  return query.data;
}

function queryErrorMessage(error) {
  if (error === null || error === undefined) return '';
  if (error instanceof Error) return error.message;
  return String(error);
}

function queryCategoryState(query, enabled, cwd) {
  if (!cwd) return { status: 'disabled', error: '' };
  if (!enabled) return { status: 'idle', error: '' };
  if (query.isError) return { status: 'error', error: queryErrorMessage(query.error) };
  if (query.isPending) return { status: 'loading', error: '' };
  return { status: 'success', error: '' };
}

function useCatalogCategory(kind, cwd, enabled, loader) {
  const query = useQuery({
    queryKey: ['slash-command-catalog', kind, cwd],
    queryFn: () => loader(cwd),
    enabled,
    retry: false,
  });
  return {
    items: queryItems(query, kind),
    state: queryCategoryState(query, enabled, cwd),
  };
}

function useCapabilityReconciliation(kind, category, reconcile) {
  const { items, state } = category;
  useEffect(() => {
    if (state.status === 'idle') return;
    reconcile({ kind, status: state.status, items });
  }, [items, kind, reconcile, state.status]);
}

export function useSlashCommandCatalog(options) {
  const {
    copy,
    cwd,
    hasSelectedCapabilities,
    paletteOpen,
    service = slashCommandCatalogService,
    store,
  } = options;
  const enabled = Boolean(cwd) && (paletteOpen || hasSelectedCapabilities);
  const builtins = useMemo(() => builtinSlashCommandItems(copy), [copy]);
  const skills = useCatalogCategory('skill', cwd, enabled, service.loadSkills);
  const prompts = useCatalogCategory('prompt', cwd, enabled, service.loadPrompts);
  const automations = useCatalogCategory('automation', cwd, enabled, service.loadAutomations);
  const tools = useCatalogCategory('mcp_tool', cwd, enabled, service.loadMCPTools);

  useCapabilityReconciliation(
    'skill',
    skills,
    store.reconcileComposerCapabilities,
  );
  useCapabilityReconciliation(
    'mcp_tool',
    tools,
    store.reconcileComposerCapabilities,
  );

  return {
    items: [
      ...builtins,
      ...skills.items,
      ...prompts.items,
      ...automations.items,
      ...tools.items,
    ],
    categoryStates: {
      builtin: { status: 'success', error: '' },
      skill: skills.state,
      prompt: prompts.state,
      automation: automations.state,
      mcp_tool: tools.state,
    },
  };
}
