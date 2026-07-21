import { installAppTestHooks, testEnv } from "./test-utils/appTestHarness.jsx";

installAppTestHooks();
const {
  act,
  render,
  screen,
  waitFor,
  expect,
  it,
  resetClientStoreForTests,
  useClientStore,
  App,
  backend,
  waitForBackendThreadHeading,
} = testEnv;

it("does not render the removed work status from the backend turn state machine", async () => {
  backend.getSidebarState.mockResolvedValue({
    activeThreadId: "thread-1",
    threads: [
      {
        id: "thread-1",
        name: "后端线程",
        provider: "codex",
        status: "preparing",
      },
    ],
    tokenUsageByThread: {
      "thread-1": {
        usedTokens: 128,
        contextWindowTokens: 1024,
        usedPercent: 12.5,
      },
    },
  });

  const { container } = render(<App />);

  await waitForBackendThreadHeading();
  expect(container.querySelector(".work-status")).toBeNull();

  act(() => {
    backend.__bridgeCallback({
      type: "ui/thread/patch",
      payload: {
        threadId: "thread-1",
        sequence: "1",
        status: "force_completing",
      },
    });
  });

  expect(container.querySelector(".work-status")).toBeNull();
});

it("keeps backend projected thread states out of the removed work status bar", async () => {
  backend.getSidebarState.mockResolvedValue({
    activeThreadId: "thread-1",
    threads: [
      { id: "thread-1", name: "后端线程", provider: "codex", status: "idle" },
    ],
  });

  const { container } = render(<App />);

  await waitForBackendThreadHeading();
  expect(container.querySelector(".work-status")).toBeNull();

  for (const [index, status] of [
    "starting",
    "thinking",
    "editing",
    "waiting",
    "syncing",
    "responding",
    "error",
    "archived",
  ].entries()) {
    act(() => {
      backend.__bridgeCallback({
        type: "ui/thread/patch",
        payload: {
          threadId: "thread-1",
          sequence: `${index + 1}`,
          status,
        },
      });
    });
    expect(container.querySelector(".work-status")).toBeNull();
  }
});

it("does not render removed work status details or token chip", async () => {
  backend.getSidebarState.mockResolvedValue({
    activeThreadId: "thread-1",
    threads: [
      { id: "thread-1", name: "后端线程", provider: "codex", status: "idle" },
    ],
    tokenUsageByThread: {
      "thread-1": {
        usedTokens: 21017,
        contextWindowTokens: 258400,
        usedPercent: 8.1,
      },
    },
  });
  backend.getThreadState.mockResolvedValue({
    activeThreadId: "thread-1",
    timelinesByThread: { "thread-1": [] },
  });

  const { container } = render(<App />);
  await waitForBackendThreadHeading();

  act(() => {
    useClientStore.setState((state) => ({
      statuses: {
        ...state.statuses,
        "thread-1": {
          status: "idle",
          statusDetails:
            "��持被跳过，但写入成功|临时文件清理|输出 `scratch_removed`",
        },
      },
    }));
  });

  expect(container.querySelector(".work-status")).toBeNull();
  expect(container).not.toHaveTextContent("持被跳过，但写入成功");
  expect(container).not.toHaveTextContent("21017 / 258400 tokens");
});

it("does not expose internal thread identifiers when the work status bar is hidden", async () => {
  const internalId = "agent_1780284988948557000";
  backend.getSidebarState.mockResolvedValueOnce({
    activeThreadId: internalId,
    threads: [
      { id: internalId, name: internalId, provider: "codex", status: "idle" },
    ],
    statuses: { [internalId]: "idle" },
  });
  backend.getThreadState.mockResolvedValueOnce({
    activeThreadId: internalId,
    timelinesByThread: { [internalId]: [] },
  });

  const { container } = render(<App />);

  await screen.findByRole("button", { name: "新对话" });
  expect(container.querySelector(".work-status")).toBeNull();
  expect(container).not.toHaveTextContent(internalId);
  expect(
    screen.getAllByRole("button", { name: "新对话" }).length,
  ).toBeGreaterThan(0);
});

it("shows a lightweight history placeholder when the active thread has no trusted cache", async () => {
  const { container } = render(<App />);
  await waitForBackendThreadHeading();

  act(() => {
    useClientStore.setState((state) => ({
      statuses: { ...state.statuses, "thread-1": "idle" },
      threads: state.threads.map((thread) =>
        thread.id === "thread-1" ? { ...thread, status: "idle" } : thread,
      ),
      timelinesByThread: {
        ...state.timelinesByThread,
        "thread-1": [],
      },
      threadTimelineReadyByThread: {
        ...state.threadTimelineReadyByThread,
        "thread-1": false,
      },
      threadStateLoadingByThread: {
        ...state.threadStateLoadingByThread,
        "thread-1": true,
      },
    }));
  });

  await waitFor(() => {
    expect(
      screen.getByTestId("timeline-loading-placeholder"),
    ).toHaveTextContent("正在同步会话历史");
    expect(container.querySelector(".work-status")).toBeNull();
  });
});

it("keeps the existing timeline visible while the active thread state is refreshing", async () => {
  resetClientStoreForTests({
    bootstrapStatus: "ready",
    cwd: "/repo/app",
    activeProject: "/repo/app",
    activeThreadId: "thread-1",
    threads: [
      { id: "thread-1", name: "后端线程", provider: "codex", status: "idle" },
    ],
    statuses: { "thread-1": "idle" },
    timelinesByThread: {
      "thread-1": [
        {
          id: "assistant-cached",
          kind: "assistant",
          text: "刷新前已有的回答",
          ts: "2026-05-30T00:00:00Z",
        },
      ],
    },
    threadTimelineReadyByThread: { "thread-1": true },
    threadStateLoadingByThread: { "thread-1": true },
  });

  const { container } = render(<App skipBootstrap />);

  await waitFor(() => {
    expect(screen.getByText("刷新前已有的回答")).toBeInTheDocument();
    expect(screen.getByTestId("chat-timeline")).toHaveTextContent(
      "刷新前已有的回答",
    );
    expect(
      screen.queryByTestId("timeline-loading-placeholder"),
    ).not.toBeInTheDocument();
    expect(container.querySelector(".work-status")).toBeNull();
  });
});

it("shows AI thinking records with elapsed time in the chat timeline", async () => {
  backend.getThreadState.mockResolvedValue({
    activeThreadId: "thread-1",
    timelinesByThread: {
      "thread-1": [
        {
          id: "thinking-1",
          kind: "thinking",
          text: "已探索 4 个文件并运行 2 条命令。",
          done: true,
          ts: "2026-05-30T00:00:00Z",
          completedAt: "2026-05-30T00:06:05Z",
        },
        {
          id: "assistant-after-thinking",
          kind: "assistant",
          text: "这是整理后的回答。",
          ts: "2026-05-30T00:06:06Z",
        },
      ],
    },
  });

  render(<App />);

  expect(await screen.findByLabelText("AI 思考记录")).toHaveTextContent(
    "已处理 AI 思考 6m 5s",
  );
  expect(screen.getByLabelText("AI 思考记录")).toHaveTextContent(
    "已探索 4 个文件并运行 2 条命令。",
  );
  expect(screen.getByText("这是整理后的回答。")).toBeInTheDocument();
});

it("does not invent elapsed time for completed thinking records without an end timestamp", async () => {
  backend.getThreadState.mockResolvedValue({
    activeThreadId: "thread-1",
    timelinesByThread: {
      "thread-1": [
        {
          id: "thinking-without-end",
          kind: "thinking",
          text: "完成态缺少结束时间。",
          done: true,
          ts: "2026-05-30T00:00:00Z",
        },
      ],
    },
  });

  render(<App />);

  const traces = await screen.findAllByLabelText("AI 思考记录");
  const trace = traces.find((node) =>
    node.textContent.includes("完成态缺少结束时间。"),
  );
  expect(trace).toBeTruthy();
  expect(trace).toHaveTextContent("已处理");
  expect(trace).not.toHaveTextContent(/已处理 \d+[sm]/);
});

it("does not show noisy zero-second elapsed time for completed thinking records", async () => {
  backend.getThreadState.mockResolvedValue({
    activeThreadId: "thread-1",
    timelinesByThread: {
      "thread-1": [
        {
          id: "thinking-zero-duration",
          kind: "thinking",
          text: "完成态小于一秒。",
          done: true,
          ts: "2026-05-30T00:00:00Z",
          completedAt: "2026-05-30T00:00:00Z",
        },
      ],
    },
  });

  render(<App />);

  const traces = await screen.findAllByLabelText("AI 思考记录");
  const trace = traces.find((node) =>
    node.textContent.includes("完成态小于一秒。"),
  );
  expect(trace).toBeTruthy();
  expect(trace).toHaveTextContent("已处理");
  expect(trace).not.toHaveTextContent("已处理 0s");
});

it("uses numeric unix timestamps for thinking elapsed time instead of dropping them", async () => {
  backend.getThreadState.mockResolvedValue({
    activeThreadId: "thread-1",
    timelinesByThread: {
      "thread-1": [
        {
          id: "thinking-numeric-time",
          kind: "thinking",
          text: "使用后端数值时间。",
          done: true,
          ts: 1000,
          completedAt: 1003,
        },
      ],
    },
  });

  render(<App />);

  const traces = await screen.findAllByLabelText("AI 思考记录");
  const trace = traces.find((node) =>
    node.textContent.includes("使用后端数值时间。"),
  );
  expect(trace).toBeTruthy();
  expect(trace).toHaveTextContent("已处理 AI 思考 3s");
});

it("uses backend-provided thinking duration when timestamps are incomplete", async () => {
  backend.getThreadState.mockResolvedValue({
    activeThreadId: "thread-1",
    timelinesByThread: {
      "thread-1": [
        {
          id: "thinking-duration-ms",
          kind: "thinking",
          text: "使用后端耗时。",
          done: true,
          ts: "2026-05-30T00:00:00Z",
          elapsedMs: 2300,
        },
      ],
    },
  });

  render(<App />);

  const traces = await screen.findAllByLabelText("AI 思考记录");
  const trace = traces.find((node) =>
    node.textContent.includes("使用后端耗时。"),
  );
  expect(trace).toBeTruthy();
  expect(trace).toHaveTextContent("已处理 AI 思考 2s");
});
