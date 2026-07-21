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
  rightPanelWidthSchema,
  rightPanelDefaultWidth,
  rightPanelMaxWidth,
  threadRailTargetWidth,
  App,
  dispatchPointer,
  waitForBackendThreadHeading,
  createShellLayoutStorage,
} = testEnv;

it("starts with only the chat rail and conversation, then toggles the right sidebar from the toolbar", async () => {
  const { container } = render(<App />);
  const restoredRightPanelWidth = Math.min(
    rightPanelWidthSchema.initialValue,
    rightPanelMaxWidth(
      window.innerWidth,
      threadRailTargetWidth(window.innerWidth),
    ),
  );

  await waitForBackendThreadHeading();
  const layout = screen.getByTestId("chat-layout");

  expect(screen.queryByTestId("runtime-panel")).not.toBeInTheDocument();
  expect(screen.queryByTestId("right-panel-resizer")).not.toBeInTheDocument();
  expect(
    screen.getByRole("button", { name: "显示侧边栏" }),
  ).toBeInTheDocument();
  expect(layout).toHaveStyle({ gridTemplateColumns: "minmax(0, 1fr)" });

  fireEvent.click(screen.getByRole("button", { name: "显示侧边栏" }));

  expect(screen.getByTestId("runtime-panel")).toBeInTheDocument();
  expect(screen.getByTestId("right-panel-resizer")).toBeInTheDocument();
  expect(
    screen.getByRole("button", { name: "隐藏侧边栏" }),
  ).toBeInTheDocument();
  expect(
    within(container.querySelector(".runtime-panel")).getByRole("button", {
      name: "折叠 file",
    }),
  ).toBeInTheDocument();
  expect(container.querySelector(".runtime-panel")).not.toHaveTextContent(
    "diff --git a/file b/file",
  );
  expect(
    screen.getByRole("list", { name: "工具调用统计" }),
  ).toBeInTheDocument();
  expect(screen.queryByTestId("warning-log-panel")).not.toBeInTheDocument();
  expect(layout).toHaveStyle({
    gridTemplateColumns: `minmax(0, 1fr) 6px ${restoredRightPanelWidth}px`,
  });
});

it("supports keyboard resizing for chat and activity resizer controls", async () => {
  Object.defineProperty(window, "innerWidth", {
    configurable: true,
    value: 1400,
  });
  Object.defineProperty(window, "innerHeight", {
    configurable: true,
    value: 640,
  });
  const storage = createShellLayoutStorage(
    String(rightPanelWidthSchema.initialValue),
  );

  render(<App shellLayoutStorage={storage} />);

  await waitForBackendThreadHeading();
  const layout = screen.getByTestId("chat-layout");
  const leftResizer = screen.getByRole("separator", { name: "调整会话栏宽度" });
  expect(leftResizer.tagName).toBe("BUTTON");

  expect(leftResizer).toHaveAttribute("aria-valuenow", "264");

  fireEvent.keyDown(leftResizer, { key: "ArrowLeft" });

  expect(leftResizer).toHaveAttribute("aria-valuenow", "248");
  expect(layout).toHaveStyle({ gridTemplateColumns: "minmax(0, 1fr)" });

  fireEvent.click(screen.getByRole("button", { name: "显示侧边栏" }));
  let rightResizer = screen.getByRole("separator", { name: "调整侧边栏宽度" });
  expect(rightResizer.tagName).toBe("BUTTON");

  const rightPanelMaximum = rightPanelMaxWidth(window.innerWidth, 248);
  const restoredWidth = Math.min(
    rightPanelWidthSchema.initialValue,
    rightPanelMaximum,
  );
  expect(rightResizer).toHaveAttribute("aria-valuenow", String(restoredWidth));
  expect(storage.value()).toBe(String(rightPanelWidthSchema.initialValue));

  fireEvent.keyDown(rightResizer, { key: "ArrowLeft" });

  const arrowWidth = Number(rightResizer.getAttribute("aria-valuenow"));
  expect(arrowWidth).toBeGreaterThan(restoredWidth);
  expect(storage.value()).toBe(String(arrowWidth));
  expect(layout).toHaveStyle({
    gridTemplateColumns: `minmax(0, 1fr) 6px ${arrowWidth}px`,
  });

  fireEvent.keyDown(rightResizer, { key: "Home" });

  expect(storage.value()).toBe("0");
  expect(screen.queryByTestId("runtime-panel")).not.toBeInTheDocument();

  fireEvent.click(screen.getByRole("button", { name: "显示侧边栏" }));
  rightResizer = screen.getByRole("separator", { name: "调整侧边栏宽度" });
  const defaultWidth = Math.min(
    rightPanelDefaultWidth(window.innerWidth),
    rightPanelMaximum,
  );
  expect(rightResizer).toHaveAttribute("aria-valuenow", String(defaultWidth));
  expect(storage.value()).toBe(String(defaultWidth));

  fireEvent.keyDown(rightResizer, { key: "End" });

  expect(rightResizer).toHaveAttribute(
    "aria-valuenow",
    String(rightPanelMaximum),
  );
  expect(storage.value()).toBe(String(rightPanelMaximum));

  fireEvent.click(screen.getByRole("button", { name: "隐藏侧边栏" }));
  fireEvent.click(screen.getByRole("button", { name: "显示侧边栏" }));
  rightResizer = screen.getByRole("separator", { name: "调整侧边栏宽度" });
  expect(rightResizer).toHaveAttribute(
    "aria-valuenow",
    String(rightPanelMaximum),
  );
  expect(storage.value()).toBe(String(rightPanelMaximum));
  expect(storage.set).toHaveBeenCalledTimes(4);

  const activityResizer = screen.getByRole("separator", {
    name: "调整工具使用面板高度",
  });
  expect(activityResizer.tagName).toBe("BUTTON");

  expect(activityResizer).toHaveAttribute("aria-valuenow", "64");

  fireEvent.keyDown(activityResizer, { key: "ArrowUp" });

  expect(activityResizer).toHaveAttribute("aria-valuenow", "80");
  expect(screen.getByTestId("runtime-panel")).toHaveStyle({
    "--activity-panel-height": "80px",
  });
});

it("opens the right sidebar at one fifth on wide screens", async () => {
  Object.defineProperty(window, "innerWidth", {
    configurable: true,
    value: 1980,
  });

  const storage = createShellLayoutStorage();
  render(<App shellLayoutStorage={storage} />);
  await waitForBackendThreadHeading();

  const layout = screen.getByTestId("chat-layout");

  expect(layout).toHaveStyle({ gridTemplateColumns: "minmax(0, 1fr)" });

  fireEvent.click(screen.getByRole("button", { name: "显示侧边栏" }));

  expect(layout).toHaveStyle({
    gridTemplateColumns: "minmax(0, 1fr) 6px 380px",
  });
});

it("clamps a persisted panel width on viewport shrink and keeps the committed clamp when it grows", async () => {
  Object.defineProperty(window, "innerWidth", {
    configurable: true,
    value: 1400,
  });
  const persistedWidth = 480.5;
  const storage = createShellLayoutStorage(String(persistedWidth));

  render(<App shellLayoutStorage={storage} />);
  await waitForBackendThreadHeading();

  const layout = screen.getByTestId("chat-layout");

  fireEvent.click(screen.getByRole("button", { name: "显示侧边栏" }));
  expect(layout).toHaveStyle({
    gridTemplateColumns: `minmax(0, 1fr) 6px ${persistedWidth}px`,
  });
  expect(storage.set).not.toHaveBeenCalled();

  Object.defineProperty(window, "innerWidth", {
    configurable: true,
    value: 1024,
  });
  act(() => {
    window.dispatchEvent(new Event("resize"));
  });

  const clampedWidth = rightPanelMaxWidth(
    window.innerWidth,
    threadRailTargetWidth(window.innerWidth),
  );
  await waitFor(() => {
    expect(layout).toHaveStyle({
      gridTemplateColumns: `minmax(0, 1fr) 6px ${clampedWidth}px`,
    });
    expect(storage.value()).toBe(String(clampedWidth));
  });
  expect(storage.set).toHaveBeenCalledExactlyOnceWith(
    rightPanelWidthSchema.key,
    String(clampedWidth),
  );

  Object.defineProperty(window, "innerWidth", {
    configurable: true,
    value: 1980,
  });
  act(() => {
    window.dispatchEvent(new Event("resize"));
  });

  await waitFor(() => {
    expect(layout).toHaveStyle({
      gridTemplateColumns: `minmax(0, 1fr) 6px ${clampedWidth}px`,
    });
  });
  expect(storage.value()).toBe(String(clampedWidth));
  expect(storage.set).toHaveBeenCalledTimes(1);
});

it("lets the right sidebar grow toward two fifths while preserving two fifths for conversation", async () => {
  Object.defineProperty(window, "innerWidth", {
    configurable: true,
    value: 1980,
  });

  render(<App />);
  await waitForBackendThreadHeading();

  const layout = screen.getByTestId("chat-layout");

  fireEvent.click(screen.getByRole("button", { name: "显示侧边栏" }));
  const rightResizer = screen.getByTestId("right-panel-resizer");

  dispatchPointer(rightResizer, "pointerdown", 1100);
  dispatchPointer(window, "pointermove", 500);
  dispatchPointer(window, "pointerup", 500);

  await waitFor(() => {
    expect(layout).toHaveStyle({
      gridTemplateColumns: "minmax(0, 1fr) 6px 751px",
    });
  });
});

it("keeps right sidebar drag updates local until the pointer is released", async () => {
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

  expect(storage.value()).toBe("380");

  dispatchPointer(rightResizer, "pointerdown", 1100);
  dispatchPointer(window, "pointermove", 700);

  expect(layout).toHaveStyle({
    gridTemplateColumns: "minmax(0, 1fr) 6px 751px",
  });
  expect(storage.value()).toBe("380");

  dispatchPointer(window, "pointerup", 700);

  expect(storage.value()).toBe("751");
});

it("stops right sidebar resizing when the pointer is no longer pressed", async () => {
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
  dispatchPointer(window, "pointermove", 1000);
  expect(layout).toHaveStyle({
    gridTemplateColumns: "minmax(0, 1fr) 6px 480px",
  });

  dispatchPointer(window, "pointermove", 700, { buttons: 0 });
  expect(layout).toHaveStyle({
    gridTemplateColumns: "minmax(0, 1fr) 6px 480px",
  });
  expect(storage.value()).toBe("480");

  dispatchPointer(window, "pointermove", 500, { buttons: 0 });
  expect(layout).toHaveStyle({
    gridTemplateColumns: "minmax(0, 1fr) 6px 480px",
  });
});

it("keeps the right sidebar draggable past the previous early close width", async () => {
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
  dispatchPointer(window, "pointermove", 1330);

  expect(screen.getByTestId("runtime-panel")).toBeInTheDocument();
  expect(screen.getByTestId("right-panel-resizer")).toBeInTheDocument();
  expect(layout).toHaveStyle({
    gridTemplateColumns: "minmax(0, 1fr) 6px 150px",
  });

  dispatchPointer(window, "pointerup", 1330);

  expect(storage.value()).toBe("150");
});
