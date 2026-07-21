import { installAppTestHooks, testEnv } from "./test-utils/appTestHarness.jsx";

installAppTestHooks();
const {
  act,
  render,
  screen,
  within,
  expect,
  it,
  resetClientStoreForTests,
  useClientStore,
  App,
  backend,
  waitForBackendThreadHeading,
  findThreadCardByName,
} = testEnv;

it("shows tool execution details inside the AI processing frame", async () => {
  backend.getThreadState.mockResolvedValue({
    activeThreadId: "thread-1",
    timelinesByThread: {
      "thread-1": [
        {
          id: "tool-file-read",
          kind: "tool",
          title: "file.open",
          status: "completed",
          text: "读取 frontend-app/src/App.jsx，定位 ReasoningTrace。",
          done: true,
          ts: "2026-05-30T00:00:00Z",
          completedAt: "2026-05-30T00:00:03Z",
        },
        {
          id: "assistant-after-tool",
          kind: "assistant",
          text: "工具结果已整理。",
          ts: "2026-05-30T00:00:04Z",
        },
      ],
    },
  });

  render(<App />);

  await screen.findByText("已处理 file.open 3s");
  const trace = screen
    .getAllByLabelText("AI 思考记录")
    .find((record) => record.textContent.includes("已处理 file.open 3s"));
  expect(trace).toBeDefined();
  expect(trace).toHaveClass("reasoning-message");
  expect(trace).not.toHaveClass("message");
  expect(trace).not.toHaveClass("assistant");
  expect(trace).toHaveTextContent("已处理 file.open 3s");
  const step = within(trace).getByLabelText("工具步骤");
  expect(step).toHaveTextContent("读取 frontend-app/src/App.jsx");
  expect(screen.getByText("工具结果已整理。")).toBeInTheDocument();
});

it("shows active agent timeline tool cards when timeline state is keyed by agent id", async () => {
  backend.getSidebarState.mockResolvedValue({
    activeThreadId: "thread-1",
    threads: [
      {
        id: "thread-1",
        agentId: "agent-1",
        name: "Thread 1",
        provider: "codex",
        status: "running",
      },
    ],
  });

  render(<App />);

  await findThreadCardByName("Thread 1");

  act(() => {
    useClientStore.setState((state) => ({
      activeThreadId: "thread-1",
      threads: [
        {
          id: "thread-1",
          agentId: "agent-1",
          name: "Thread 1",
          provider: "codex",
          status: "running",
        },
      ],
      timelinesByThread: {
        ...state.timelinesByThread,
        "agent-1": [
          {
            id: "tool-agent-keyed",
            kind: "tool",
            title: "file",
            status: "completed",
            text: "agent keyed tool result",
            done: true,
            ts: "2026-05-30T00:00:00Z",
          },
        ],
      },
      threadTimelineReadyByThread: {
        ...state.threadTimelineReadyByThread,
        "agent-1": true,
      },
      threadStateLoadingByThread: {},
    }));
  });

  const trace = await screen.findByLabelText("AI 思考记录");
  expect(trace).toHaveTextContent("agent keyed tool result");
});

it("hides ghost command timeline cards from the conversation body", async () => {
  render(<App />);

  await waitForBackendThreadHeading();

  act(() => {
    useClientStore.setState((state) => ({
      timelinesByThread: {
        ...state.timelinesByThread,
        "thread-1": [
          {
            id: "ghost-command",
            kind: "command",
            title: "执行命令",
            status: "completed",
            done: true,
          },
          {
            id: "assistant-after-ghost",
            role: "assistant",
            kind: "assistant",
            text: "正常回复",
            time: "2026-05-30T00:00:00Z",
          },
        ],
      },
      threadTimelineReadyByThread: {
        ...state.threadTimelineReadyByThread,
        "thread-1": true,
      },
    }));
  });

  expect(await screen.findByText("正常回复")).toBeInTheDocument();
  expect(screen.queryByText("执行命令")).not.toBeInTheDocument();
  expect(screen.queryByLabelText("AI 思考记录")).not.toBeInTheDocument();
});

it("coalesces running and completed lifecycle events for the same tool call", async () => {
  backend.getThreadState.mockResolvedValue({
    activeThreadId: "thread-1",
    timelinesByThread: {
      "thread-1": [
        {
          id: "tool-file-running",
          kind: "tool",
          title: "file",
          status: "running",
          call_id: "call-file-1",
          done: false,
          ts: "2026-05-30T00:00:00Z",
        },
        {
          id: "tool-file-completed",
          kind: "tool",
          title: "file",
          status: "completed",
          call_id: "call-file-1",
          text: '{\n  "success": true\n}',
          done: true,
          ts: "2026-05-30T00:00:00Z",
          completedAt: "2026-05-30T00:00:01Z",
        },
      ],
    },
  });

  render(<App />);

  const traces = await screen.findAllByLabelText("AI 思考记录");
  const fileTraces = traces.filter((node) =>
    node.textContent.includes("success"),
  );
  expect(fileTraces).toHaveLength(1);
  expect(fileTraces[0]).toHaveTextContent("已处理 file 1s");
  expect(fileTraces[0]).toHaveTextContent('"success": true');
  expect(within(fileTraces[0]).getByLabelText("工具步骤")).toHaveTextContent(
    '"success": true',
  );
  expect(fileTraces[0]).not.toHaveTextContent("正在调用工具并等待返回结果。");
});

it("does not append a pending thinking placeholder after completed processing activity", async () => {
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
    activeTurnByThread: {
      "thread-1": {
        id: "turn-running",
        threadId: "thread-1",
        status: "running",
        startedAt: "2026-05-30T00:00:00Z",
      },
    },
    timelinesByThread: {
      "thread-1": [
        {
          id: "user-waiting",
          role: "user",
          kind: "user",
          text: "请生成架构图",
          time: "2026-05-30T00:00:00Z",
        },
        {
          id: "tool-file-completed",
          role: "assistant",
          kind: "tool",
          title: "file",
          status: "completed",
          text: "读取文件完成。",
          done: true,
          time: "2026-05-30T00:00:01Z",
          completedAt: "2026-05-30T00:00:02Z",
        },
      ],
    },
    threadTimelineReadyByThread: { "thread-1": true },
    threadStateLoadingByThread: {},
  });

  render(<App skipBootstrap />);

  await act(async () => {
    await Promise.resolve();
  });
  const traces = screen.getAllByLabelText("AI 思考记录");
  expect(traces).toHaveLength(1);
  expect(traces[0]).toHaveTextContent("读取文件完成。");
  expect(traces[0]).not.toHaveTextContent("正在处理请求");
});

it("renders AI execution plans as checklist details in the processing frame", async () => {
  backend.getThreadState.mockResolvedValue({
    activeThreadId: "thread-1",
    timelinesByThread: {
      "thread-1": [
        {
          id: "plan-1",
          kind: "plan",
          title: "执行计划",
          status: "running",
          done: false,
          text: [
            "并行审查前端和后端代码",
            "✅ 1. 读取当前前端代码",
            "🔄 2. 修复项目选择器重复展示",
            "⏳ 3. 隐藏注入提示词",
          ].join("\n"),
          ts: "2026-05-30T00:00:00Z",
        },
      ],
    },
  });

  render(<App />);

  const plan = await screen.findByLabelText("AI 执行计划");
  expect(plan).toHaveTextContent("执行计划");
  expect(plan).toHaveTextContent("已完成 1/3 项任务");
  expect(within(plan).getByText("读取当前前端代码")).toBeInTheDocument();
  expect(within(plan).getByText("修复项目选择器重复展示")).toBeInTheDocument();
  expect(within(plan).getByText("隐藏注入提示词")).toBeInTheDocument();
  const list = within(plan).getByRole("list");
  expect(list.tagName).toBe("OL");
  expect(list).toHaveClass("execution-plan-list");
  const items = within(list).getAllByRole("listitem");
  expect(items).toHaveLength(3);
  expect(items[0]).toHaveAttribute("data-plan-status", "done");
  expect(items[1]).toHaveAttribute("data-plan-status", "pending");
});

it("shows an active thinking placeholder while a turn is running before output arrives", async () => {
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
    active_turn: {
      id: "turn-running",
      thread_id: "thread-1",
      status: "running",
      started_at: "2026-05-30T00:00:00Z",
    },
  });
  backend.getThreadState.mockResolvedValueOnce({
    activeThreadId: "thread-1",
    timelinesByThread: {
      "thread-1": [
        {
          id: "user-waiting",
          kind: "user",
          text: "请生成架构图",
          ts: "2026-05-30T00:00:00Z",
        },
      ],
    },
  });

  render(<App />);

  expect(await screen.findByLabelText("AI 思考记录")).toHaveTextContent(
    /正在思考 \d+[sm]/,
  );
  expect(screen.getByLabelText("AI 思考记录")).toHaveTextContent(
    "AI 正在分析上下文、选择工具并整理回答。",
  );
});
