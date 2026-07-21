import { normalizeMemorySnapshot as normalizeMemorySnapshotForFacade } from "../adapters/memoryAdapter.js";

function optionalFixtureString(value, fieldName) {
  if (value === undefined || value === null) return "";
  if (typeof value !== "string") {
    throw new TypeError(`${fieldName} must be a string`);
  }
  return value;
}

function optionalFixtureArray(value, fieldName) {
  if (value === undefined || value === null) return [];
  if (!Array.isArray(value)) {
    throw new TypeError(`${fieldName} must be an array`);
  }
  return value;
}

function mockObservabilityDefaults(backend) {
  const emptyTraceResult = {
    source: "memory",
    events: [],
    slowest_events: [],
    errors: [],
    total_duration_ms: 0,
    truncated: false,
  };
  backend.getObservabilityStatus.mockResolvedValue({
    enabled: true,
    schema_version: 1,
    index_trace_keys: 1,
    sink_events_written: 2,
    sink_write_errors: 0,
  });
  backend.getObservabilityTrace.mockResolvedValue(emptyTraceResult);
  backend.getObservabilityThreadRecent.mockResolvedValue(emptyTraceResult);
  backend.listObservabilityRecent.mockResolvedValue(emptyTraceResult);
  backend.listObservabilitySlow.mockResolvedValue(emptyTraceResult);
  backend.listObservabilityErrors.mockResolvedValue(emptyTraceResult);
}

function mockPromptDefaults(backend) {
  backend.listPromptAssets.mockResolvedValue({ prompts: [] });
  backend.getDashboardPrompts.mockResolvedValue({ prompts: [] });
  backend.getPrompt.mockResolvedValue({ prompt: { content: "" } });
  backend.writePrompt.mockResolvedValue({ prompt: { id: "saved-prompt" } });
  backend.deletePrompt.mockResolvedValue({ deleted: true });
  backend.getPersonalizationProfile.mockResolvedValue({ profile: {} });
  backend.savePersonalizationProfile.mockResolvedValue({ profile: {} });
  backend.listPromptSections.mockResolvedValue({ sections: [] });
  backend.writePromptSection.mockResolvedValue({ ok: true });
  backend.deletePromptSection.mockResolvedValue({ ok: true });
  backend.draftPromptIntent.mockResolvedValue({
    draft_key: "intent/expert/default",
    kind: "expert",
    scope: "project",
    status: "review",
    card: {
      kind: "expert",
      title: "默认专家",
      summary: "整理后的能力",
      output: "执行说明",
      hit_examples: ["需要专家能力时"],
      miss_examples: ["普通聊天"],
    },
    issues: [],
  });
  backend.commitPromptIntent.mockResolvedValue({
    prompt: { id: "intent/expert/default" },
  });
  backend.discardPromptIntent.mockResolvedValue({ ok: true });
  backend.dryRunPromptIntent.mockResolvedValue({
    would_use: true,
    reasons: ["matched"],
  });
}

function canonicalPromptRPCItem(overrides = {}) {
  return {
    id: "main/canonical",
    name: "规范提示词",
    content: "",
    description: "",
    agentType: "main",
    when_to_use: "",
    createdAt: "2026-07-11T00:00:00Z",
    updatedAt: "2026-07-11T00:00:00Z",
    enabled: true,
    scope: "project",
    tags: ["intent:expert"],
    ...overrides,
  };
}

function mockPromptWizardEntryPrompt(backend, overrides = {}) {
  const name = overrides.name || "待确认入口";
  const content = overrides.content || "待确认内容";
  const scope = overrides.scope || "project";
  backend.listPromptAssets.mockResolvedValue({
    prompts: [
      canonicalPromptRPCItem({
        id: overrides.id || "intent/expert/entry",
        draft_key: overrides.draftKey || "intent/expert/entry",
        draft_status: overrides.status || "ready_to_save",
        state: "pending_confirm",
        name,
        content,
        description: optionalFixtureString(
          overrides.description,
          "prompt description",
        ),
        tags: overrides.tags || ["intent:expert"],
        scope,
        enabled: true,
        card: overrides.card || {
          kind: "expert",
          scope,
          title: name,
          output: content,
          hit_examples: [],
          miss_examples: [],
        },
        issues: optionalFixtureArray(overrides.issues, "prompt issues"),
      }),
    ],
  });
}

function mockMemoryDefaults(backend) {
  // mock 必须与真实 facade 输出一致：backendApi.getMemorySnapshot 经 validateMemorySnapshotResponse
  // parse + transform 后返回扁平 { overview, entries }，而不是原始 wire 的 private/team 结构。
  backend.getMemorySnapshot.mockResolvedValue(
    normalizeMemorySnapshotForFacade({
      overview: {
        enabled: true,
        autoDreamEnabled: false,
        autoDreamIntent: null,
        projectRoot: "/repo/app",
        health: {
          preferenceCount: 0,
          projectCount: 0,
          maxPerCategory: 15,
          similarGroups: [],
        },
      },
      private: { entries: [] },
      team: { entries: [] },
    }),
  );
  backend.getMemoryEntry.mockResolvedValue({
    target: "private",
    path: "feedback/tdd.md",
    name: "tdd-rule",
    title: "遵守 TDD",
    description: "先写红测",
    type: "feedback",
    content: "规则\n先写红测",
  });
  backend.upsertMemoryEntry.mockResolvedValue({ path: "feedback/tdd.md" });
  backend.deleteMemoryEntry.mockResolvedValue({ deleted: true });
  backend.setMemoryAutoDreamIntent.mockResolvedValue({
    ok: true,
    enabled: true,
  });
  backend.mergeMemoryEntries.mockResolvedValue({ path: "feedback/tdd.md" });
  backend.ignoreMemorySimilarity.mockResolvedValue({ ok: true });
  backend.consolidateMemorySimilarities.mockResolvedValue({
    merged: 1,
    ignored: 0,
    failed: 0,
    skipped: 0,
  });
  backend.startConsolidateMemorySimilarities.mockResolvedValue({
    jobId: "memory-job-1",
    status: "running",
  });
  backend.getMemoryConsolidationStatus.mockResolvedValue({
    jobId: "memory-job-1",
    status: "succeeded",
    result: { merged: 1, ignored: 0, failed: 0, skipped: 0 },
  });
}

function mockWorkflowDefaults(backend) {
  backend.listDags.mockResolvedValue({ dags: [] });
  backend.getDagDetail.mockResolvedValue({ dag: null, nodes: [] });
  backend.getDagRuns.mockResolvedValue({ runs: [] });
  backend.getDagRun.mockResolvedValue({ run: null, nodes: [] });
  backend.startDag.mockResolvedValue({ runKey: "run-started" });
  backend.terminateDagRun.mockResolvedValue({ ok: true });
  backend.deleteDag.mockResolvedValue({ ok: true });
  backend.applyDagOps.mockResolvedValue({ newVersion: 2 });
  backend.listWorkflowTemplates.mockResolvedValue({ templates: [] });
  backend.getWorkflowTemplate.mockResolvedValue({ template: null });
  backend.renderWorkflowTemplateDraft.mockResolvedValue({ draft: null });
}

function mockCronDefaults(backend) {
  backend.listCronJobs.mockResolvedValue({ jobs: [] });
  backend.getCronJob.mockResolvedValue({ id: "cron-1" });
  backend.createCronJob.mockResolvedValue({ id: "cron-created" });
  backend.updateCronJob.mockResolvedValue({ id: "cron-1" });
  backend.deleteCronJob.mockResolvedValue({ ok: true });
  backend.runCronJobOnce.mockResolvedValue({ id: "cron-1" });
  backend.setCronJobEnabled.mockResolvedValue({
    id: "cron-1",
    enabled: true,
  });
  backend.listCronJobRuns.mockResolvedValue({ runs: [] });
}

function mockSkillDefaults(backend) {
  backend.deleteSkill.mockResolvedValue({ ok: true });
  backend.listMCPServers.mockResolvedValue({
    mcpServers: {
      sqlite: { enabled: false },
      playwright: { enabled: false },
    },
  });
  backend.startSQLiteMCPServer.mockResolvedValue({
    serverName: "sqlite",
    enabled: true,
  });
  backend.stopSQLiteMCPServer.mockResolvedValue({
    serverName: "sqlite",
    enabled: false,
  });
  backend.startPlaywrightMCPServer.mockResolvedValue({
    serverName: "playwright",
    enabled: true,
  });
  backend.stopPlaywrightMCPServer.mockResolvedValue({
    serverName: "playwright",
    enabled: false,
  });
  backend.readSkill.mockImplementation(({ path }) =>
    Promise.resolve({
      skill: {
        content: path.endsWith("/SKILL.md")
          ? [
              "---",
              'name: "backend"',
              'display_name: "后端"',
              'description: "当你需要 Go 后端开发时使用。"',
              'trigger_words: ["Go", "backend"]',
              "---",
              "",
              "## 后端规则",
            ].join("\n")
          : "关联文件内容",
      },
    }),
  );
  backend.listSkillFiles.mockResolvedValue({
    files: [
      {
        name: "SKILL.md",
        path: "/repo/app/.agents/skills/backend/SKILL.md",
        is_main: true,
      },
      {
        name: "guide.md",
        path: "/repo/app/.agents/skills/backend/references/guide.md",
        is_main: false,
      },
    ],
  });
  backend.writeSkill.mockResolvedValue({
    path: "/repo/app/.agents/skills/backend/SKILL.md",
  });
  backend.importSkillDirectories.mockResolvedValue({
    imported: [
      {
        name: "ImportedSkill",
        skill_file: "/imports/ImportedSkill/SKILL.md",
      },
    ],
    failures: [],
  });
  backend.suggestSkillSummary.mockResolvedValue("当你需要部署服务时使用。");
  backend.selectProjectDir.mockResolvedValue("/repo/new");
  backend.selectProjectDirs.mockResolvedValue(["/imports/ImportedSkill"]);
  backend.listSkillResolutions.mockResolvedValue({ items: [] });
  backend.listSkillTools.mockResolvedValue({ tools: [] });
  backend.createSkillTool.mockResolvedValue({
    id: 1,
    name: "Test Tool",
    command: "echo",
    args: [],
    enabled: true,
  });
  backend.getSkillTool.mockResolvedValue({
    id: 1,
    name: "Test Tool",
    command: "echo",
    args: [],
    enabled: true,
  });
  backend.updateSkillTool.mockResolvedValue({
    id: 1,
    name: "Test Tool",
    command: "echo",
    args: [],
    enabled: true,
  });
  backend.deleteSkillTool.mockResolvedValue({ id: 1, deleted: true });
  backend.previewSkillResolution.mockResolvedValue({
    items: [
      {
        provider: "codex",
        preview_id: "preview-1",
        preview_hash: "hash-1",
        source_path: "/repo/app/.agents/skills/backend/SKILL.md",
        target_path: "/Users/test/.codex/skills/backend/SKILL.md",
      },
    ],
  });
  backend.applySkillResolution.mockResolvedValue({ ok: true });
}

function mockSharedFileDefaults(backend) {
  backend.listSharedFiles.mockResolvedValue({
    files: [],
    finalOutputRefs: [],
    sharedFileRetention: {
      items: [],
      protectedCount: 0,
      cleanupCandidateCount: 0,
    },
  });
  backend.readSharedFile.mockImplementation(({ path }) =>
    Promise.resolve({
      path,
      content: `content for ${path}`,
      updatedBy: "agent",
      updatedAt: "2026-05-30T07:00:00Z",
    }),
  );
  backend.deleteSharedFile.mockResolvedValue({ deleted: true });
  backend.saveTextFile.mockResolvedValue("/exports/file.md");
}

function mockSettingsAndThreadDefaults(backend) {
  backend.onFilesDropped.mockReturnValue(() => {});
  backend.beginTextClipboardWrite.mockReturnValue(null);
  backend.copyTextToClipboard.mockResolvedValue(true);
  backend.readLspPromptHint.mockResolvedValue({
    hint: "effective prompt text",
    defaultHint: "default prompt text",
    overrideHint: "",
    usingDefault: true,
  });
  backend.writeLspPromptHint.mockResolvedValue({
    hint: "saved prompt text",
    defaultHint: "default prompt text",
    overrideHint: "saved prompt text",
    usingDefault: false,
  });
  backend.readBuiltinTools.mockResolvedValue({ tools: [] });
  backend.writeBuiltinTool.mockResolvedValue({ tools: [] });
  backend.listToolbridgeTools.mockResolvedValue({ tools: [] });
  backend.listDashboardLogs.mockResolvedValue({ logs: [] });
  backend.listModelProviders.mockResolvedValue({
    activeVendorId: "",
    vendors: [],
  });
  backend.getBuildInfo.mockResolvedValue({
    version: "v1.2.3",
    runtime: "linux/amd64",
    buildTime: "2026-05-30T07:00:00Z",
    commit: "abc123def456",
  });
  backend.getPreference.mockImplementation(({ key }) =>
    Promise.resolve(
      {
        "settings.provider.active": "codex",
        "settings.provider.codex.model": "gpt-5.5",
        "settings.provider.codex.effort": "xhigh",
        "settings.provider.codex.codexHome": "~/.codex",
        "settings.provider.codex.codexInstanceKey": "default",
        "settings.provider.codex.codexModelProvider": "openai",
        "settings.provider.claude.model": "sonnet",
        "settings.provider.claude.effort": "high",
      }[key] ?? null,
    ),
  );
  backend.archiveThread.mockResolvedValue({ ok: true });
  backend.unarchiveThread.mockResolvedValue({ ok: true });
  backend.deleteThread.mockResolvedValue({ ok: true });
  backend.getThreadConfig.mockResolvedValue({
    threadId: "thread-1",
    provider: "codex",
    supportsThreadOverride: true,
    override: {},
    effective: { model: "gpt-5.4", effort: "medium" },
  });
  backend.setThreadConfig.mockResolvedValue({
    threadId: "thread-1",
    provider: "codex",
    supportsThreadOverride: true,
    override: { model: "gpt-5.5", effort: "" },
    effective: { model: "gpt-5.5", effort: "medium" },
  });
  backend.setPreference.mockResolvedValue({ ok: true });
  backend.locateCodeFile.mockResolvedValue({
    ok: true,
    paths: ["/repo/app/src/a.js"],
    matches: [
      { path: "/repo/app/src/a.js", relative: "src/a.js", totalLines: 2 },
    ],
  });
  backend.openCodeFile.mockResolvedValue({
    ok: true,
    filePath: "/repo/app/src/a.js",
    relative: "src/a.js",
    startLine: 1,
    endLine: 2,
    totalLines: 2,
    previewMode: "full",
    contentVersion: "version-src-a",
    snippet: [
      { line: 1, text: "old" },
      { line: 2, text: "keep" },
    ],
  });
  backend.saveCodeFile.mockResolvedValue({
    ok: true,
    filePath: "/repo/app/src/a.js",
    relative: "src/a.js",
    totalLines: 2,
  });
}

export function createDefaultsFactory(ctx) {
  const { backend } = ctx;
  return {
    mockObservabilityDefaults: () => mockObservabilityDefaults(backend),
    mockPromptDefaults: () => mockPromptDefaults(backend),
    canonicalPromptRPCItem,
    mockPromptWizardEntryPrompt: (overrides) =>
      mockPromptWizardEntryPrompt(backend, overrides),
    mockMemoryDefaults: () => mockMemoryDefaults(backend),
    mockWorkflowDefaults: () => mockWorkflowDefaults(backend),
    mockCronDefaults: () => mockCronDefaults(backend),
    mockSkillDefaults: () => mockSkillDefaults(backend),
    mockSharedFileDefaults: () => mockSharedFileDefaults(backend),
    mockSettingsAndThreadDefaults: () => mockSettingsAndThreadDefaults(backend),
  };
}
