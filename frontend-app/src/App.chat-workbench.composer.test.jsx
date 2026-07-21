import { installAppTestHooks, testEnv } from "./test-utils/appTestHarness.jsx";

installAppTestHooks();
const {
  act,
  fireEvent,
  render,
  screen,
  waitFor,
  within,
  expect,
  it,
  vi,
  resetClientStoreForTests,
  App,
  backend,
  deferred,
  waitForBackendThreadHeading,
} = testEnv;

it("shows a non-timed preparation status before the first real turn starts", async () => {
  backend.getSidebarState.mockResolvedValue({
    activeThreadId: "",
    threads: [],
  });
  backend.getThreadState.mockResolvedValue({ timelinesByThread: {} });
  backend.startThread.mockResolvedValue({ thread: { id: "thread-new" } });
  const startTurnDeferred = deferred();
  backend.startTurn.mockReturnValue(startTurnDeferred.promise);

  render(<App />);

  await screen.findByText("我们应该在 燧元 中构建什么？");
  fireEvent.change(screen.getByTestId("composer-input"), {
    target: { value: "请真正调用后端聊天" },
  });
  fireEvent.click(screen.getByLabelText("发送消息"));

  await waitFor(() => expect(backend.startTurn).toHaveBeenCalled());
  const preparingTrace = screen.getByLabelText("AI 思考记录");
  expect(preparingTrace).toHaveTextContent("正在准备响应");
  expect(preparingTrace).not.toHaveTextContent("正在思考");
  expect(preparingTrace).not.toHaveTextContent("0s");

  act(() => {
    backend.__bridgeCallback({
      type: "ui/thread/patch",
      payload: {
        threadId: "thread-new",
        sequence: "1",
        activeTurn: {
          id: "turn-live",
          threadId: "thread-new",
          status: "running",
          startedAt: "2026-05-30T00:00:00Z",
        },
      },
    });
  });

  await waitFor(() =>
    expect(screen.getByLabelText("AI 思考记录")).toHaveTextContent(
      /正在思考 \d+[sm]/,
    ),
  );

  await act(async () => {
    startTurnDeferred.resolve({ ok: true });
    await Promise.resolve();
  });
});

it("updates active thinking elapsed time in place every second", async () => {
  await import("./pages/chat/ChatPage.jsx").then(() => vi.useFakeTimers());
  try {
    vi.setSystemTime(new Date("2026-05-30T00:00:00Z"));
    resetClientStoreForTests({
      bootstrapStatus: "ready",
      cwd: "/repo/app",
      activeProject: "/repo/app",
      activeThreadId: "thread-1",
      threads: [
        {
          id: "thread-1",
          name: "后端线程",
          provider: "codex",
          status: "running",
        },
      ],
      timelinesByThread: {
        "thread-1": [
          {
            id: "thinking-live",
            role: "assistant",
            kind: "thinking",
            title: "grep",
            text: "正在搜索。",
            time: "2026-05-30T00:00:00Z",
            done: false,
          },
        ],
      },
      threadTimelineReadyByThread: { "thread-1": true },
      threadStateLoadingByThread: {},
    });

    render(<App skipBootstrap />);

    await act(async () => {
      await Promise.resolve();
      await Promise.resolve();
    });

    const trace = screen.getByLabelText("AI 思考记录");
    expect(trace).toHaveTextContent("正在思考 0s");

    await act(async () => {
      await vi.advanceTimersByTimeAsync(2100);
    });

    expect(trace).toHaveTextContent("正在思考 2s");
  } finally {
    vi.useRealTimers();
  }
});

it("renders runtime tool details with long names in a shrink-safe structure", async () => {
  const longToolName =
    "mcp__very_long_server_name_that_would_overflow__deeply_nested_tool_name_with_many_segments";
  backend.getSidebarState.mockResolvedValueOnce({
    activeThreadId: "thread-1",
    threads: [
      {
        id: "thread-1",
        name: "后端线程",
        provider: "codex",
        status: "running",
      },
    ],
    activityStatsByThread: {
      "thread-1": {
        toolCalls: { [longToolName]: 3 },
      },
    },
  });

  const { container } = render(<App />);
  await waitForBackendThreadHeading();
  fireEvent.click(screen.getByRole("button", { name: "显示侧边栏" }));

  const toolStat = screen.getByRole("button", { name: "工具调用总数" });
  expect(toolStat).not.toHaveAttribute("title");
  fireEvent.mouseEnter(toolStat);
  expect(screen.queryByTestId("runtime-stat-tooltip")).not.toBeInTheDocument();
  fireEvent.click(toolStat);

  const tooltip = await screen.findByTestId("runtime-stat-tooltip");
  expect(tooltip).toHaveTextContent(
    "deeply_nested_tool_name_with_many_segments",
  );
  expect(
    tooltip.querySelector(".runtime-stat-tooltip-row"),
  ).toBeInTheDocument();
  expect(
    tooltip.querySelector(".runtime-stat-tooltip-name"),
  ).not.toHaveAttribute("title");
  expect(container.querySelector(".runtime-panel")).toHaveClass(
    "runtime-panel",
  );
});

it("sets the chat composer textarea to three visible rows", async () => {
  render(<App />);
  await waitForBackendThreadHeading();

  const composer = screen.getByRole("combobox", {
    name: "输入给 Agent 的内容",
  });
  expect(composer).toHaveAttribute("rows", "3");
  expect(composer).toHaveAttribute("placeholder", "随心输入");
});

it("does not render a desktop titlebar inside the workbench shell", async () => {
  const { container } = render(<App />);

  expect(await waitForBackendThreadHeading()).toBeInTheDocument();
  expect(container.querySelector(".traffic-lights")).toBeNull();
  expect(container.querySelectorAll(".titlebar")).toHaveLength(0);
  expect(
    within(screen.getByTestId("app-sidebar")).getByText("燧元"),
  ).toBeInTheDocument();
  expect(screen.getByTestId("suiyuan-brand-light-logo")).toBeInTheDocument();
  expect(screen.getByTestId("suiyuan-brand-dark-logo")).toBeInTheDocument();
  expect(
    within(screen.getByTestId("app-sidebar"))
      .getByRole("button", { name: "新对话" })
      .querySelector(".lucide-plus"),
  ).toBeInTheDocument();
  expect(
    within(screen.getByTestId("app-sidebar"))
      .getByRole("button", { name: "聊天页面" })
      .querySelector(".lucide-message-square-text"),
  ).toBeInTheDocument();
  expect(
    within(screen.getByTestId("app-sidebar"))
      .getByRole("button", { name: "自动化" })
      .querySelector(".lucide-sliders-horizontal"),
  ).toBeInTheDocument();
  expect(
    within(screen.getByTestId("app-sidebar"))
      .getByRole("button", { name: "链路追踪" })
      .querySelector(".lucide-database"),
  ).toBeInTheDocument();
});

it("keeps the user message visible and calls thread/start before turn/start for a new chat", async () => {
  backend.getSidebarState.mockResolvedValue({
    activeThreadId: "",
    threads: [],
  });
  backend.getThreadState.mockResolvedValue({ timelinesByThread: {} });
  backend.startThread.mockResolvedValue({ thread: { id: "thread-new" } });
  backend.startTurn.mockResolvedValue({ ok: true });

  render(<App />);

  await screen.findByText("我们应该在 燧元 中构建什么？");
  expect(screen.queryByTestId("composer-project")).not.toBeInTheDocument();
  expect(screen.queryByLabelText("发送权限")).not.toBeInTheDocument();
  fireEvent.change(screen.getByTestId("composer-input"), {
    target: { value: "请真正调用后端聊天" },
  });
  fireEvent.click(screen.getByLabelText("发送消息"));

  await waitFor(() => {
    expect(backend.startThread).toHaveBeenCalledBefore(backend.startTurn);
    expect(backend.startTurn).toHaveBeenCalledWith({
      cwd: "/repo/app",
      threadId: "thread-new",
      input: [{ type: "text", text: "请真正调用后端聊天" }],
      manualSkillSelection: false,
    });
  });
  const startPayload = backend.startThread.mock.calls[0][0];
  expect(startPayload).not.toHaveProperty("prompt");
  expect(startPayload).not.toHaveProperty("optimisticUserMessage");
  expect(startPayload).not.toHaveProperty("skipInitialRuntimeSync");
  expect(startPayload.config).toEqual({
    codexHome: "~/.codex",
    codexInstanceKey: "default",
    codexModelProvider: "openai",
  });

  expect(
    screen.getAllByText("请真正调用后端聊天").length,
  ).toBeGreaterThanOrEqual(1);
});

it("renders the inherited timeline used by fork drafts", async () => {
  backend.getThreadState.mockResolvedValue({
    activeThreadId: "thread-1",
    timelinesByThread: {
      "thread-1": [
        { id: "user-1", kind: "user", text: "原始需求：补齐工作台能力" },
        {
          id: "assistant-1",
          kind: "assistant",
          text: "阶段结论：先迁移 fork draft 链路",
        },
      ],
    },
  });

  render(<App />);

  expect(
    await screen.findByText("阶段结论：先迁移 fork draft 链路"),
  ).toBeInTheDocument();
});

it("opens a fork draft card from the chat composer and submits an inherited thread", async () => {
  backend.getThreadState.mockResolvedValue({
    activeThreadId: "thread-1",
    timelinesByThread: {
      "thread-1": [
        { id: "user-1", kind: "user", text: "原始需求：补齐工作台能力" },
        {
          id: "assistant-1",
          kind: "assistant",
          text: "阶段结论：先迁移 fork draft 链路",
        },
      ],
    },
  });
  backend.listSharedFiles.mockResolvedValue({
    files: [{ path: "reports/final.md" }],
    finalOutputRefs: [],
    sharedFileRetention: {
      items: [],
      protectedCount: 0,
      cleanupCandidateCount: 0,
    },
  });
  backend.forkThread.mockResolvedValue({
    thread: { id: "thread-fork", forkedFrom: "thread-1" },
    kickoffState: "created_only",
  });
  backend.startTurn.mockResolvedValue({ ok: true });

  render(<App />);

  await waitForBackendThreadHeading();
  fireEvent.click(screen.getByRole("button", { name: "聊天操作" }));
  fireEvent.click(
    await screen.findByRole("menuitem", { name: "继承当前对话" }),
  );

  const card = await screen.findByTestId("fork-draft-card");
  expect(card).toHaveTextContent("继承自会话：后端线程");
  fireEvent.click(within(card).getByLabelText("选择共享文件 reports/final.md"));
  fireEvent.click(within(card).getByRole("button", { name: "创建继承对话" }));

  await waitFor(() => {
    expect(backend.forkThread).toHaveBeenCalledWith({ threadId: "thread-1" });
  });
  expect(backend.startThread).not.toHaveBeenCalled();
  expect(backend.startTurn).toHaveBeenCalledWith({
    cwd: "/repo/app",
    threadId: "thread-fork",
    input: [
      {
        type: "text",
        text: "请基于已继承的完整对话历史，简要总结当前进展并提出下一步建议。",
      },
      {
        type: "filecontent",
        path: "reports/final.md",
        name: "reports/final.md",
        content: "content for reports/final.md",
      },
    ],
    manualSkillSelection: false,
  });
});

it("opens a fork draft from the context usage warning banner", async () => {
  backend.getSidebarState.mockResolvedValue({
    activeThreadId: "thread-1",
    threads: [
      { id: "thread-1", name: "后端线程", provider: "codex", status: "工作中" },
    ],
    active_turn: { id: "turn-1", thread_id: "thread-1", status: "running" },
    tokenUsageByThread: {
      "thread-1": {
        usedTokens: 920,
        contextWindowTokens: 1000,
        usedPercent: 92,
      },
    },
  });
  backend.getThreadState.mockResolvedValue({
    activeThreadId: "thread-1",
    timelinesByThread: {
      "thread-1": [
        { id: "user-1", kind: "user", text: "上下文快满了" },
        { id: "assistant-1", kind: "assistant", text: "建议新建继承会话" },
      ],
    },
  });
  backend.listSharedFiles.mockResolvedValue({
    files: [{ path: "reports/final.md" }],
    finalOutputRefs: [],
    sharedFileRetention: {
      items: [],
      protectedCount: 0,
      cleanupCandidateCount: 0,
    },
  });

  render(<App />);

  await screen.findByText("建议新建继承会话");
  const banner = await screen.findByTestId("context-usage-banner");
  expect(banner.tagName).toBe("OUTPUT");
  expect(banner).toHaveTextContent("上下文使用率");
  expect(banner).toHaveTextContent("92%");
  fireEvent.click(within(banner).getByRole("button", { name: "新建继承会话" }));

  const card = await screen.findByTestId("fork-draft-card");
  expect(card).toHaveTextContent("继承自会话：后端线程");
});

it("sends the composer draft when plain Enter is pressed inside the textarea", async () => {
  backend.getSidebarState.mockResolvedValue({
    activeThreadId: "thread-1",
    threads: [
      { id: "thread-1", name: "后端线程", provider: "codex", status: "idle" },
    ],
  });
  backend.startTurn.mockResolvedValue({ ok: true });
  render(<App />);

  await waitForBackendThreadHeading();
  const input = screen.getByTestId("composer-input");
  fireEvent.change(input, {
    target: { value: "普通 Enter 发送" },
  });

  expect(
    fireEvent.keyDown(input, {
      key: "Enter",
      code: "Enter",
      isComposing: false,
    }),
  ).toBe(false);

  expect(backend.startThread).not.toHaveBeenCalled();
  await waitFor(() => {
    expect(backend.startTurn).toHaveBeenCalledWith({
      cwd: "/repo/app",
      threadId: "thread-1",
      input: [{ type: "text", text: "普通 Enter 发送" }],
      manualSkillSelection: false,
    });
  });
});

it("does not send the composer draft when Enter confirms IME composition", async () => {
  render(<App />);

  await waitForBackendThreadHeading();
  const input = screen.getByTestId("composer-input");
  fireEvent.change(input, {
    target: { value: "拼音候选" },
  });

  expect(
    fireEvent.keyDown(input, {
      key: "Process",
      code: "Enter",
      keyCode: 229,
      which: 229,
      isComposing: true,
    }),
  ).toBe(true);

  expect(backend.startTurn).not.toHaveBeenCalled();
  expect(input).toHaveValue("拼音候选");
});

it("floats the composer in the intro state and docks it after the first message", async () => {
  backend.getSidebarState.mockResolvedValue({
    activeThreadId: "",
    threads: [],
  });
  backend.getThreadState.mockResolvedValue({ timelinesByThread: {} });
  backend.startThread.mockResolvedValue({ thread: { id: "thread-new" } });
  backend.startTurn.mockResolvedValue({ ok: true });

  const { container } = render(<App />);

  await screen.findByText("我们应该在 燧元 中构建什么？");
  expect(screen.getByTestId("composer-dock")).toHaveClass(
    "composer",
    "composer--floating",
  );
  expect(screen.getByTestId("chat-timeline")).toContainElement(
    screen.getByTestId("composer-dock"),
  );
  expect(container.querySelector(".work-status")).toBeNull();

  fireEvent.change(screen.getByTestId("composer-input"), {
    target: { value: "让输入框下沉到底部" },
  });
  fireEvent.click(screen.getByLabelText("发送消息"));

  await waitFor(() => {
    expect(screen.getByTestId("composer-dock")).toHaveClass(
      "composer",
      "composer--docked",
    );
  });
  expect(screen.getByTestId("composer-dock")).not.toHaveClass(
    "composer--floating",
  );
  expect(screen.getByTestId("chat-timeline")).not.toContainElement(
    screen.getByTestId("composer-dock"),
  );
  expect(container.querySelector(".work-status")).toBeNull();
});
