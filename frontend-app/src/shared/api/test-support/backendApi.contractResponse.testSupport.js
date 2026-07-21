export function builtinToolsResponse() {
  return { tools: [{ id: "Shell", label: "Shell", enabled: true }] };
}

export function codeSaveResponse() {
  return {
    ok: true,
    filePath: "/repo/app/src/App.jsx",
    relative: "src/App.jsx",
    totalLines: 1,
  };
}

export function dashboardLogsResponse() {
  return { logs: [] };
}

export function dashboardPageResponse() {
  return {
    agents: [],
    dags: [],
    skills: [],
    commandCards: [],
    prompts: [],
    memory: [],
    finalOutputRefs: [],
    sharedFileRetention: {
      items: [],
      protectedCount: 0,
      cleanupCandidateCount: 0,
    },
  };
}

export function frontendIngestResponse() {
  return { enabled: true, recorded: 1, dropped: 0 };
}

export function modelProviderRegistryResponse() {
  return { activeVendorId: "", vendors: [] };
}

export function okResponse() {
  return { ok: true };
}

export function openWindowResponse() {
  return { ok: true, windowId: "window-2", cwd: "/repo/app" };
}

export function projectsStateResponse() {
  return { projects: ["/repo/app"], active: "/repo/app" };
}

export function runtimeConfigResponse(overrides = {}) {
  return {
    model: "gpt-5.5",
    modelProvider: null,
    cwd: "/repo/app",
    approvalPolicy: "on-failure",
    sandbox: "workspace-write",
    config: null,
    baseInstructions: null,
    developerInstructions: null,
    personality: null,
    toolRouting: {
      mode: "legacy",
      routerModel: "",
      routerProvider: "openai_compatible",
      routerBaseURL: "",
      routerHasAPIKey: false,
      confidenceThreshold: 0.65,
      timeoutSec: 8,
    },
    ...overrides,
  };
}

export function sidebarStateResponse(overrides = {}) {
  return {
    threads: [
      {
        id: "thread-1",
        name: "Main",
        agent_id: "agent-1",
        createdAt: "2026-07-13T00:00:00Z",
        updatedAt: "2026-07-13T00:00:01Z",
        lifecycleStatus: "active",
        state: "running",
        threadStatus: "running",
        agentState: "working",
        lastMessage: "Working",
        overlayText: "Running",
        overlayType: "status",
        overlayPriority: 1,
      },
    ],
    agents: [
      {
        id: "agent-1",
        name: "Main agent",
        thread_id: "thread-1",
        provider_thread_id: "provider-thread-1",
        parent_id: "",
        state: "running",
        provider: "codex",
        model: "gpt-5.5",
        cwd: "/repo/app",
        port: 8090,
        logPath: "/tmp/agent.log",
        createdAt: "2026-07-13T00:00:00Z",
        updatedAt: "2026-07-13T00:00:01Z",
        last_report: "Working",
        agentState: "working",
        threadStatus: "running",
        lastMessage: "Working",
      },
    ],
    active_turn: {
      id: "turn-1",
      agent_id: "agent-1",
      thread_id: "thread-1",
      status: "running",
      success: true,
      error: "",
      reason: "",
      started_at: "2026-07-13T00:00:00Z",
      completed_at: "2026-07-13T00:00:01Z",
    },
    recent_turns: [],
    workspace: {
      runs: [
        {
          run_key: "run-1",
          dag_key: "dag-1",
          status: "running",
          source_root: "/repo/app",
          workspace_path: "/repo/worktree",
          created_by: "agent-1",
          updated_by: "agent-1",
          merged_file_count: 1,
          conflicts: 0,
          errors: 0,
          message: "Working",
          updated_at: "2026-07-13T00:00:01Z",
        },
      ],
    },
    token_usage: {
      inputTokens: 1,
      outputTokens: 2,
      totalTokens: 3,
      usedTokens: 3,
      contextWindowTokens: 128000,
      usedPercent: 0.01,
    },
    statuses: { "thread-1": "running" },
    interruptibleByThread: { "thread-1": true },
    statusHeadersByThread: { "thread-1": "Running" },
    statusDetailsByThread: { "thread-1": "Working" },
    agentRuntimeById: { "agent-1": { pid: 42 } },
    activeThreadId: "thread-1",
    activeCmdThreadId: "thread-1",
    mainAgentId: "agent-1",
    "viewPrefs.chat": { density: "compact" },
    "viewPrefs.cmd": { wrap: true },
    "threadPins.chat": { "thread-1": 1 },
    "threadArchives.chat": { "thread-2": 2 },
    groups: [{ key: "active", title: "Active", threads: [{ id: "thread-1" }] }],
    ...overrides,
  };
}

export function videoApiKeyStatusResponse() {
  return { configured: false, masked: "" };
}

export function windowBootstrapResponse() {
  return { snapshot: {} };
}
