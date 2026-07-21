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
  useClientStore,
  App,
  backend,
  dispatchPointer,
  waitForBackendThreadHeading,
  getThreadCardByName,
  clickThreadCardByName,
  findThreadCardByName,
  createShellLayoutStorage,
} = testEnv;

it("closes the right sidebar when dragged flush to the right edge", async () => {
  Object.defineProperty(window, "innerWidth", {
    configurable: true,
    value: 1980,
  });
  const storage = createShellLayoutStorage("380");

  render(<App shellLayoutStorage={storage} />);
  await waitForBackendThreadHeading();

  const layout = screen.getByTestId("chat-layout");

  fireEvent.click(screen.getByRole("button", { name: "显示侧边栏" }));
  const rightResizer = screen.getByTestId("right-panel-resizer");

  dispatchPointer(rightResizer, "pointerdown", 1100);
  dispatchPointer(window, "pointermove", 1480);

  expect(screen.queryByTestId("runtime-panel")).not.toBeInTheDocument();
  expect(screen.queryByTestId("right-panel-resizer")).not.toBeInTheDocument();
  expect(layout).toHaveStyle({ gridTemplateColumns: "minmax(0, 1fr)" });
  expect(storage.value()).toBe("0");
});

it("isolates right sidebar diff, warnings, and tool stats to the selected agent", async () => {
  backend.getSidebarState.mockResolvedValue({
    activeThreadId: "thread-a",
    threads: [
      {
        id: "thread-a",
        agentId: "agent-a",
        name: "Agent A",
        provider: "codex",
        status: "running",
      },
      {
        id: "thread-b",
        agentId: "agent-b",
        name: "Agent B",
        provider: "codex",
        status: "running",
      },
    ],
    activityStatsByThread: {
      "agent-a": {
        lspCalls: 1,
        commands: 0,
        fileEdits: 1,
        toolCalls: { mcp__lsp__patch_edit: 1 },
      },
      "agent-b": {
        lspCalls: 7,
        commands: 0,
        fileEdits: 0,
        toolCalls: { shell: 7 },
      },
    },
    diffTextByThread: {
      "agent-a": "diff --git a/a b/a",
      "agent-b": "diff --git a/b b/b",
    },
  });
  backend.getThreadState.mockImplementation(({ threadId }) =>
    Promise.resolve({
      activeThreadId: threadId,
      timelinesByThread: {
        [threadId]: [
          {
            id: `assistant-${threadId}`,
            kind: "assistant",
            text: `${threadId} ready`,
          },
        ],
      },
    }),
  );

  render(<App />);
  await findThreadCardByName("Agent A");

  act(() => {
    backend.__bridgeCallback({
      type: "thread.send/failed",
      payload: { method: "turn/start", agentId: "agent-a", error: "a failed" },
    });
    backend.__bridgeCallback({
      type: "bridge.call/failed",
      payload: { method: "turn/start", agentId: "agent-b", error: "b failed" },
    });
    backend.__bridgeCallback({
      type: "api.rpc.failed",
      payload: { method: "thread/config/get", error: "global failed" },
    });
  });

  fireEvent.click(screen.getByRole("button", { name: "显示侧边栏" }));
  expect(screen.queryByTestId("warning-log-panel")).not.toBeInTheDocument();
  fireEvent.keyDown(screen.getByTestId("activity-panel-resizer"), {
    key: "ArrowUp",
  });

  expect(
    within(screen.getByTestId("runtime-panel")).getByRole("button", {
      name: "折叠 a",
    }),
  ).toBeInTheDocument();
  expect(screen.getByTestId("runtime-panel")).not.toHaveTextContent(
    "diff --git a/a b/a",
  );
  expect(screen.getByTestId("runtime-panel")).not.toHaveTextContent(
    "diff --git a/b b/b",
  );
  expect(screen.getByLabelText("LSP (7 tools) 调用次数")).toHaveTextContent(
    "1",
  );
  expect(screen.getByTestId("warning-log-panel")).toHaveTextContent(
    "thread.send/failed",
  );
  expect(screen.getByTestId("warning-log-panel")).toHaveTextContent(
    "api.rpc.failed",
  );
  expect(screen.getByTestId("warning-log-panel")).not.toHaveTextContent(
    "bridge.call/failed",
  );

  clickThreadCardByName("Agent B");

  await waitFor(() => {
    expect(
      within(screen.getByTestId("runtime-panel")).getByRole("button", {
        name: "折叠 b",
      }),
    ).toBeInTheDocument();
    expect(screen.getByTestId("runtime-panel")).not.toHaveTextContent(
      "diff --git a/a b/a",
    );
    expect(screen.getByTestId("runtime-panel")).not.toHaveTextContent(
      "diff --git a/b b/b",
    );
    expect(screen.getByLabelText("LSP (7 tools) 调用次数")).toHaveTextContent(
      "7",
    );
    expect(screen.getByTestId("warning-log-panel")).toHaveTextContent(
      "bridge.call/failed",
    );
    expect(screen.getByTestId("warning-log-panel")).toHaveTextContent(
      "api.rpc.failed",
    );
    expect(screen.getByTestId("warning-log-panel")).not.toHaveTextContent(
      "thread.send/failed",
    );
  });
});

it("switches identity immediately but shields stale target-thread content while refreshing", async () => {
  let resolveThreadBState;
  backend.getSidebarState.mockResolvedValue({
    activeThreadId: "thread-a",
    threads: [
      { id: "thread-a", name: "Agent A", provider: "codex", status: "idle" },
      { id: "thread-b", name: "Agent B", provider: "codex", status: "idle" },
    ],
  });
  backend.getThreadState.mockImplementation(({ threadId }) => {
    if (threadId === "thread-b") {
      return new Promise((resolve) => {
        resolveThreadBState = resolve;
      });
    }
    return Promise.resolve({
      activeThreadId: threadId,
      timelinesByThread: {
        [threadId]: [
          { id: "assistant-a", kind: "assistant", text: "Agent A ready" },
        ],
      },
    });
  });

  render(<App />);
  await screen.findByText("Agent A ready");

  act(() => {
    useClientStore.setState((state) => ({
      timelinesByThread: {
        ...state.timelinesByThread,
        "thread-b": [
          {
            id: "stale-b",
            role: "assistant",
            text: "stale cached Agent B content",
          },
        ],
      },
    }));
  });

  clickThreadCardByName("Agent B");

  await waitFor(() =>
    expect(backend.getThreadState).toHaveBeenCalledWith({
      cwd: "/repo/app",
      threadId: "thread-b",
      includeDiff: false,
    }),
  );
  expect(useClientStore.getState().activeThreadId).toBe("thread-b");
  expect(useClientStore.getState().pendingActiveThreadId).toBe("");
  expect(useClientStore.getState().threadStateLoadingByThread["thread-b"]).toBe(
    true,
  );
  expect(getThreadCardByName("Agent B")).toHaveClass("active");
  expect(screen.queryByText("Agent A ready")).not.toBeInTheDocument();
  expect(
    screen.queryByText("stale cached Agent B content"),
  ).not.toBeInTheDocument();
  expect(screen.queryByText(/让我们从/)).not.toBeInTheDocument();
  expect(screen.getByTestId("timeline-loading-placeholder")).toHaveTextContent(
    "正在同步会话历史",
  );

  act(() => {
    resolveThreadBState({
      activeThreadId: "thread-b",
      threads: [
        { id: "thread-a", name: "Agent A", provider: "codex", status: "idle" },
        { id: "thread-b", name: "Agent B", provider: "codex", status: "idle" },
      ],
      timelinesByThread: {
        "thread-b": [
          { id: "fresh-b", kind: "assistant", text: "fresh Agent B content" },
        ],
      },
    });
  });

  await screen.findByText("fresh Agent B content");
  expect(useClientStore.getState().activeThreadId).toBe("thread-b");
  expect(useClientStore.getState().pendingActiveThreadId).toBe("");
  expect(screen.queryByText("Agent A ready")).not.toBeInTheDocument();
  expect(
    screen.queryByText("stale cached Agent B content"),
  ).not.toBeInTheDocument();
});

it("shows trusted cached target-thread history immediately while refreshing", async () => {
  backend.getSidebarState.mockResolvedValue({
    activeThreadId: "thread-a",
    threads: [
      { id: "thread-a", name: "Agent A", provider: "codex", status: "idle" },
      { id: "thread-b", name: "Agent B", provider: "codex", status: "idle" },
    ],
  });
  backend.getThreadState.mockImplementation(({ threadId }) => {
    if (threadId === "thread-b") return new Promise(() => {});
    return Promise.resolve({
      activeThreadId: threadId,
      timelinesByThread: {
        [threadId]: [
          { id: "assistant-a", kind: "assistant", text: "Agent A ready" },
        ],
      },
    });
  });
  backend.getThreadMessages.mockImplementation(({ threadId }) => {
    if (threadId === "thread-b") return new Promise(() => {});
    return Promise.resolve({
      messages: [],
      total: 0,
      hasMore: false,
      nextBefore: "",
    });
  });

  render(<App />);
  await screen.findByText("Agent A ready");

  act(() => {
    useClientStore.setState((state) => ({
      timelinesByThread: {
        ...state.timelinesByThread,
        "thread-b": [
          { id: "cached-b", role: "assistant", text: "cached Agent B content" },
        ],
      },
      threadTimelineReadyByThread: {
        ...state.threadTimelineReadyByThread,
        "thread-b": true,
      },
    }));
  });

  clickThreadCardByName("Agent B");

  await waitFor(() =>
    expect(backend.getThreadState).toHaveBeenCalledWith({
      cwd: "/repo/app",
      threadId: "thread-b",
      includeDiff: false,
    }),
  );
  expect(screen.getByText("cached Agent B content")).toBeInTheDocument();
  expect(screen.queryByText("Agent A ready")).not.toBeInTheDocument();
  expect(
    screen.queryByTestId("timeline-loading-placeholder"),
  ).not.toBeInTheDocument();
});

it("resizes the chat rail and right sidebar without crossing their minimum widths", async () => {
  render(<App />);
  await waitForBackendThreadHeading();

  const layout = screen.getByTestId("chat-layout");
  const leftResizer = screen.getByTestId("thread-rail-resizer");

  dispatchPointer(leftResizer, "pointerdown", 280);
  dispatchPointer(window, "pointermove", 40);
  dispatchPointer(window, "pointerup", 40);

  expect(layout).toHaveStyle({ gridTemplateColumns: "minmax(0, 1fr)" });

  fireEvent.click(screen.getByRole("button", { name: "显示侧边栏" }));
  const rightResizer = screen.getByTestId("right-panel-resizer");

  dispatchPointer(rightResizer, "pointerdown", 1100);
  dispatchPointer(window, "pointermove", 1500);
  dispatchPointer(window, "pointerup", 1500);

  await waitFor(() => {
    expect(screen.queryByTestId("runtime-panel")).not.toBeInTheDocument();
    expect(layout).toHaveStyle({ gridTemplateColumns: "minmax(0, 1fr)" });
  });
});

it("uses backend activity stats for the resizable tool usage panel", async () => {
  Object.defineProperty(window, "innerHeight", {
    configurable: true,
    value: 640,
  });

  render(<App />);
  await waitForBackendThreadHeading();

  fireEvent.click(screen.getByRole("button", { name: "显示侧边栏" }));

  expect(screen.getByTestId("runtime-panel")).toHaveStyle({
    "--activity-panel-height": "64px",
    "--activity-panel-min-height": "64px",
    "--activity-panel-max-height": "286px",
    "--diff-panel-min-height": "286px",
    "--diff-panel-max-height": "509px",
  });
  expect(screen.queryByTestId("warning-log-panel")).not.toBeInTheDocument();
  expect(screen.getByLabelText("LSP (7 tools) 调用次数")).toHaveTextContent(
    "3",
  );
  expect(screen.getByLabelText("LSP (7 tools) 调用次数")).not.toHaveAttribute(
    "title",
  );
  expect(screen.getByLabelText("工具调用总数")).toHaveTextContent("6");
  expect(screen.queryByText("patch_edit:")).not.toBeInTheDocument();

  fireEvent.mouseEnter(screen.getByLabelText("LSP (7 tools) 调用次数"));
  expect(screen.queryByTestId("runtime-stat-tooltip")).not.toBeInTheDocument();
  fireEvent.click(screen.getByLabelText("LSP (7 tools) 调用次数"));
  expect(screen.getByTestId("runtime-stat-tooltip")).toHaveTextContent(
    "LSP (7 tools)",
  );
  expect(screen.getByTestId("runtime-stat-tooltip")).toHaveTextContent(
    "patch_edit",
  );
  expect(screen.getByTestId("runtime-stat-tooltip")).toHaveTextContent("3");
  fireEvent.keyDown(
    screen.getByRole("dialog", { name: "LSP (7 tools) 调用明细" }),
    { key: "Escape" },
  );
  await waitFor(() => {
    expect(
      screen.queryByTestId("runtime-stat-tooltip"),
    ).not.toBeInTheDocument();
  });

  fireEvent.mouseDown(screen.getByTestId("activity-panel-resizer"), {
    clientY: 500,
  });
  fireEvent.mouseMove(window, { clientY: 0 });
  fireEvent.mouseUp(window);

  await waitFor(() => {
    expect(screen.getByTestId("runtime-panel")).toHaveStyle({
      "--activity-panel-height": "286px",
    });
  });
  expect(screen.getByTestId("warning-log-panel")).toBeInTheDocument();
});

it("shows tool return entries alongside warning lines in the runtime panel", async () => {
  render(<App />);
  await waitForBackendThreadHeading();

  act(() => {
    backend.__bridgeCallback({
      type: "ui/thread/patch",
      payload: {
        threadId: "thread-1",
        sequence: "9007199254740993124",
        timelineItems: [
          {
            id: "tool-grep",
            kind: "tool",
            tool: "mcp__lsp__grep",
            status: "completed",
            preview: '{"total":3}',
            output: "src/App.jsx: runtime log result",
            ts: "2026-05-30T08:00:00Z",
          },
        ],
      },
    });
    backend.__bridgeCallback({
      type: "api.rpc.failed",
      payload: {
        method: "thread/config/get",
        threadId: "thread-1",
        error: "backend unavailable",
      },
    });
  });

  fireEvent.click(screen.getByRole("button", { name: "显示侧边栏" }));
  fireEvent.keyDown(screen.getByTestId("activity-panel-resizer"), {
    key: "ArrowUp",
  });

  const logPanel = screen.getByTestId("warning-log-panel");
  expect(logPanel).toHaveTextContent("api.rpc.failed");
  expect(logPanel).toHaveTextContent("grep");
  expect(logPanel).toHaveTextContent("返回");
  expect(logPanel).not.toHaveTextContent('{"total":3}');

  const resultLine = within(logPanel).getByRole("button", { name: /grep/ });
  fireEvent.mouseEnter(resultLine);
  expect(screen.queryByTestId("warning-log-popover")).not.toBeInTheDocument();
  fireEvent.click(resultLine);

  const popover = screen.getByTestId("warning-log-popover");
  expect(popover).toHaveTextContent("[redacted]");
  expect(popover).not.toHaveTextContent("src/App.jsx: runtime log result");
  expect(popover).not.toHaveTextContent('"preview": "{\\"total\\":3}"');
});
