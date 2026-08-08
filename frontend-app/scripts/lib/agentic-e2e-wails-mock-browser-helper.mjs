function agenticE2EMockFrontendReadinessResponse(state, params) {
  if (!params || typeof params !== 'object' || Array.isArray(params)) {
    throw new Error('wails frontend readiness: request must contain one JSON object');
  }
  const unknownField = Object.keys(params).find((field) => field !== 'phase' && field !== 'epoch');
  if (unknownField) throw new Error(`wails frontend readiness: decode request: json: unknown field "${unknownField}"`);

  const phase = typeof params.phase === 'string' ? params.phase : '';
  if (!phase) throw new Error('wails frontend readiness: phase is required');
  const hasEpoch = Object.prototype.hasOwnProperty.call(params, 'epoch');
  if (phase === 'probe') {
    if (hasEpoch) throw new Error('wails frontend readiness: probe must not include an epoch');
    return { epoch: state.frontendReadiness.epoch };
  }
  if (phase === 'commit') {
    if (!hasEpoch || !Number.isSafeInteger(params.epoch) || params.epoch <= 0) {
      throw new Error('wails frontend readiness: commit epoch is required');
    }
    if (params.epoch !== state.frontendReadiness.epoch) {
      throw new Error('wails frontend readiness: epoch does not match current activation');
    }
    state.frontendReadiness.committedEpoch = params.epoch;
    return { epoch: state.frontendReadiness.epoch };
  }
  throw new Error('wails frontend readiness: phase must be probe or commit');
}

function agenticE2EMockFrontendTraceIngestResponse(params = {}) {
  if (!params || typeof params !== 'object' || Array.isArray(params) || !Array.isArray(params.events)) {
    throw new Error('wails frontend trace ingest: events must be an array');
  }
  return { enabled: true, recorded: params.events.length, dropped: 0 };
}

function agenticE2EMockConfigReadResponse(sandboxConfig) {
  return {
    model: 'gpt-5.5',
    modelProvider: null,
    cwd: sandboxConfig.projectDir,
    approvalPolicy: 'on-failure',
    sandbox: 'workspace-write',
    config: null,
    baseInstructions: null,
    developerInstructions: null,
    personality: null,
    toolRouting: {
      mode: 'legacy',
      routerModel: '',
      routerProvider: 'openai_compatible',
      routerBaseURL: '',
      routerHasAPIKey: false,
      confidenceThreshold: 0.65,
      timeoutSec: 8,
    },
  };
}

function agenticE2EMockPreferenceResponse(sandboxConfig, params = {}) {
  const key = String(params.key || '');
  if (key.includes('provider.active')) return 'codex';
  if (key.includes('.effort')) return 'medium';
  if (key.includes('codexModelProvider')) return 'openai';
  if (key.includes('codexHome')) return sandboxConfig.homeDir;
  if (key.includes('codexInstanceKey')) return 'default';
  if (key.includes('.sandbox')) return 'workspace-write';
  if (key.includes('.approvalPolicy')) return 'on-failure';
  if (key.includes('.personality')) return 'friendly';
  if (key.includes('.summary')) return 'auto';
  return '';
}

function agenticE2EMockSidebarSnapshot() {
  return {
    threads: [],
    agents: [],
    recent_turns: [],
    workspace: { runs: [] },
    token_usage: {
      inputTokens: 0,
      outputTokens: 0,
      totalTokens: 0,
      usedTokens: 0,
      contextWindowTokens: 0,
      usedPercent: 0,
    },
  };
}

window.__AGENTIC_E2E_MOCK_WAILS_HELPERS__ = Object.freeze({
  frontendReadinessResponse: agenticE2EMockFrontendReadinessResponse,
  frontendTraceIngestResponse: agenticE2EMockFrontendTraceIngestResponse,
  configReadResponse: agenticE2EMockConfigReadResponse,
  preferenceResponse: agenticE2EMockPreferenceResponse,
  sidebarSnapshot: agenticE2EMockSidebarSnapshot,
});
