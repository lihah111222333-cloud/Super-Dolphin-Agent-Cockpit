import { installAppTestHooks, testEnv } from "./test-utils/appTestHarness.jsx";

installAppTestHooks();
const {
  fireEvent,
  render,
  screen,
  waitFor,
  expect,
  it,
  useClientStore,
  App,
  backend,
  waitForBackendThreadHeading,
  queryThreadCardByName,
  findThreadCardByName,
} = testEnv;
it("clamps right-edge runtime click details into the viewport", async () => {
  Object.defineProperty(window, "innerHeight", {
    configurable: true,
    value: 640,
  });

  render(<App />);
  await waitForBackendThreadHeading();

  fireEvent.click(screen.getByRole("button", { name: "显示侧边栏" }));

  const toolStat = screen.getByLabelText("工具调用总数");
  toolStat.getBoundingClientRect = () => ({
    x: 980,
    y: 580,
    left: 980,
    right: 1008,
    top: 580,
    bottom: 596,
    width: 28,
    height: 16,
    toJSON() {
      return this;
    },
  });

  fireEvent.click(toolStat);

  const tooltip = screen.getByTestId("runtime-stat-tooltip");
  expect(tooltip).toHaveTextContent("工具");
  expect(tooltip.style.getPropertyValue("--runtime-stat-tooltip-left")).toBe(
    "652px",
  );
  expect(tooltip.style.getPropertyValue("--runtime-stat-tooltip-bottom")).toBe(
    "70px",
  );
});

it("lets bottom-right runtime click details use the available vertical space", async () => {
  Object.defineProperty(window, "innerHeight", {
    configurable: true,
    value: 640,
  });
  backend.getSidebarState.mockResolvedValue({
    activeThreadId: "thread-1",
    threads: [
      { id: "thread-1", name: "后端线程", provider: "codex", status: "工作中" },
    ],
    active_turn: { id: "turn-1", thread_id: "thread-1", status: "running" },
    tokenUsageByThread: {
      "thread-1": {
        usedTokens: 128,
        contextWindowTokens: 1024,
        usedPercent: 12.5,
      },
    },
    activityStatsByThread: {
      "thread-1": {
        toolCalls: Object.fromEntries(
          Array.from({ length: 18 }, (_, index) => [
            `very_long_tool_name_${index + 1}`,
            index + 1,
          ]),
        ),
      },
    },
  });

  render(<App />);
  await waitForBackendThreadHeading();

  fireEvent.click(screen.getByRole("button", { name: "显示侧边栏" }));

  const toolStat = screen.getByLabelText("工具调用总数");
  toolStat.getBoundingClientRect = () => ({
    x: 980,
    y: 580,
    left: 980,
    right: 1008,
    top: 580,
    bottom: 596,
    width: 28,
    height: 16,
    toJSON() {
      return this;
    },
  });

  fireEvent.click(toolStat);

  const tooltip = screen.getByTestId("runtime-stat-tooltip");
  expect(tooltip).toHaveTextContent("very_long_tool_name_18");
  expect(tooltip.style.getPropertyValue("--runtime-stat-tooltip-left")).toBe(
    "652px",
  );
  expect(tooltip.style.getPropertyValue("--runtime-stat-tooltip-bottom")).toBe(
    "70px",
  );
  expect(
    tooltip.style.getPropertyValue("--runtime-stat-tooltip-max-height"),
  ).toBe("558px");
});

it("matches the legacy thread rail archive-list toggle", async () => {
  backend.getSidebarState.mockResolvedValue({
    activeThreadId: "thread-1",
    threads: [
      { id: "thread-1", name: "活跃线程", provider: "codex", status: "工作中" },
      {
        id: "thread-archived",
        name: "归档线程",
        provider: "codex",
        status: "archived",
      },
    ],
  });
  backend.getThreadState.mockResolvedValue({
    activeThreadId: "thread-1",
    timelinesByThread: {},
  });

  render(<App />);
  await findThreadCardByName("活跃线程");

  expect(screen.getByLabelText("会话列表")).toBeInTheDocument();
  expect(screen.getByLabelText("1 个 Agent")).toBeInTheDocument();
  expect(screen.queryByText("归档线程")).not.toBeInTheDocument();

  fireEvent.click(screen.getByLabelText("打开归档列表"));

  expect(await screen.findByText("归档线程")).toBeInTheDocument();
  expect(screen.getByLabelText("归档列表")).toBeInTheDocument();
  expect(screen.getByLabelText("返回会话列表")).toBeInTheDocument();
  expect(queryThreadCardByName("活跃线程")).not.toBeInTheDocument();

  fireEvent.click(screen.getByLabelText("恢复会话"));

  await waitFor(() => {
    expect(backend.unarchiveThread).toHaveBeenCalledWith({
      threadId: "thread-archived",
    });
    expect(backend.setPreference).toHaveBeenCalledWith(
      expect.objectContaining({
        cwd: "/repo/app",
        key: "archivedThreadAtById.thread-archived",
        value: null,
      }),
    );
    expect(screen.getByText("暂无归档会话")).toBeInTheDocument();
  });
});

it("opens archived thread content from the archive list without showing the new-chat draft", async () => {
  backend.getSidebarState.mockResolvedValue({
    activeThreadId: "thread-1",
    threads: [
      { id: "thread-1", name: "活跃线程", provider: "codex", status: "工作中" },
      {
        id: "thread-archived",
        name: "归档线程",
        provider: "codex",
        status: "archived",
      },
    ],
  });
  backend.getThreadState.mockImplementation(({ threadId }) =>
    Promise.resolve({
      activeThreadId: threadId,
      threads: [
        {
          id: "thread-1",
          name: "活跃线程",
          provider: "codex",
          status: "工作中",
        },
        {
          id: "thread-archived",
          name: "归档线程",
          provider: "codex",
          status: "idle",
        },
      ],
      timelinesByThread: {
        [threadId]: [
          {
            id: `${threadId}-assistant`,
            kind: "assistant",
            text:
              threadId === "thread-archived"
                ? "归档线程历史内容"
                : "活跃线程内容",
          },
        ],
      },
    }),
  );

  render(<App />);
  await screen.findByText("活跃线程内容");

  fireEvent.click(screen.getByLabelText("打开归档列表"));
  fireEvent.click(await screen.findByRole("button", { name: /归档线程/ }));

  await waitFor(() =>
    expect(useClientStore.getState().activeThreadId).toBe("thread-archived"),
  );
  expect(await screen.findByText("归档线程历史内容")).toBeInTheDocument();
  expect(screen.queryByText(/让我们从/)).not.toBeInTheDocument();
  expect(screen.queryByLabelText("复制当前线程")).not.toBeInTheDocument();
});
