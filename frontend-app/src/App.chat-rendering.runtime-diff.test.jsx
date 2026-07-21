import { testEnv } from "./test-utils/AppChatRenderingSetup.jsx";
const {
  act,
  fireEvent,
  render,
  screen,
  waitFor,
  within,
  expect,
  it,
  App,
  backend,
  waitForBackendThreadHeading,
} = testEnv;

it("derives runtime code-change metrics from the backend diff for the selected thread", async () => {
  backend.getThreadState.mockResolvedValue({
    activeThreadId: "thread-1",
    timelinesByThread: {
      "thread-1": [
        {
          id: "assistant-1",
          kind: "assistant",
          text: "来自后端的消息",
          ts: "2026-05-30T00:00:00Z",
        },
      ],
    },
    diffTextByThread: {
      "thread-1": [
        "diff --git a/src/a.js b/src/a.js",
        "--- a/src/a.js",
        "+++ b/src/a.js",
        "@@ -1,2 +1,3 @@",
        " keep",
        "-old",
        "+new",
        "+extra",
        "diff --git a/src/b.js b/src/b.js",
        "--- a/src/b.js",
        "+++ b/src/b.js",
        "@@ -4,2 +4,2 @@",
        "-removed",
        "+added",
      ].join("\n"),
    },
  });

  render(<App />);
  await waitForBackendThreadHeading();

  act(() => {
    backend.__bridgeCallback({
      type: "bridge.call/failed",
      payload: {
        method: "turn/start",
        threadId: "thread-1",
        error: "backend failed",
      },
    });
  });
  fireEvent.click(screen.getByRole("button", { name: "显示侧边栏" }));

  const fileCountMetric = screen.getByLabelText("代码变更文件数");
  const changedLineMetric = screen.getByLabelText("代码变更行数");
  expect(fileCountMetric).toHaveTextContent("2");
  expect(fileCountMetric.querySelector("svg")).toHaveClass("lucide-file-text");
  expect(changedLineMetric).toHaveTextContent("5");
  expect(changedLineMetric.querySelector("svg")).toHaveClass("lucide-code-xml");
  expect(screen.getByLabelText("代码新增行数")).toHaveTextContent("+3");
  expect(screen.getByLabelText("代码删除行数")).toHaveTextContent("-2");
  expect(screen.getByLabelText("代码新增行数")).not.toHaveTextContent("+0");
  expect(screen.getByLabelText("代码删除行数")).not.toHaveTextContent("-1");
});

it("renders a grouped line-by-line diff instead of raw patch text", async () => {
  backend.getThreadState.mockResolvedValue({
    activeThreadId: "thread-1",
    timelinesByThread: {
      "thread-1": [
        {
          id: "assistant-1",
          kind: "assistant",
          text: "来自后端的消息",
          ts: "2026-05-30T00:00:00Z",
        },
      ],
    },
    diffTextByThread: {
      "thread-1": [
        "diff --git a/src/a.js b/src/a.js",
        "--- a/src/a.js",
        "+++ b/src/a.js",
        "@@ -1 +1,2 @@",
        "-old",
        "+new",
        "+extra",
        "diff --git a/src/b.js b/src/b.js",
        "--- a/src/b.js",
        "+++ b/src/b.js",
        "@@ -4 +4 @@",
        "-removed",
        "+added",
        "diff --git a/docs/notes.md b/docs/notes.md",
        "--- a/docs/notes.md",
        "+++ b/docs/notes.md",
        "@@ -1,0 +1 @@",
        "+note",
      ].join("\n"),
    },
  });

  render(<App />);
  await waitForBackendThreadHeading();

  fireEvent.click(screen.getByRole("button", { name: "显示侧边栏" }));

  const diffView = screen.getByTestId("diff-view");
  const fileGroups = diffView.querySelectorAll(".diff-file-group");
  expect(fileGroups).toHaveLength(3);
  expect(diffView).not.toHaveTextContent("diff --git");

  const firstFile = fileGroups[0];
  expect(
    within(firstFile).getByRole("button", { name: "折叠 src/a.js" }),
  ).toHaveTextContent("+2");
  expect(
    within(firstFile).getByRole("button", { name: "折叠 src/a.js" }),
  ).toHaveTextContent("-1");
  expect(firstFile.querySelector(".diff-line.hunk")).toHaveTextContent(
    "@@ -1 +1,2 @@",
  );
  expect(firstFile.querySelector(".diff-line.del")).toHaveTextContent("old");
  expect(firstFile.querySelector(".diff-line.add")).toHaveTextContent("new");
  expect(
    firstFile.querySelector(".diff-line.add .diff-line-new"),
  ).toHaveTextContent("1");
  expect(
    firstFile.querySelector(".diff-line.del .diff-line-old"),
  ).toHaveTextContent("1");
  expect(firstFile).not.toHaveTextContent("diff --git");
  expect(firstFile).not.toHaveTextContent("--- a/src/a.js");
  expect(firstFile).not.toHaveTextContent("+++ b/src/a.js");

  expect(diffView).toHaveTextContent("src/b.js");
  expect(diffView).toHaveTextContent("docs/notes.md");
  expect(screen.queryByTestId("diff-raw")).not.toBeInTheDocument();
});

it("locates, previews and saves runtime diff files through code RPCs", async () => {
  backend.getThreadState.mockResolvedValue({
    activeThreadId: "thread-1",
    timelinesByThread: {
      "thread-1": [
        {
          id: "assistant-1",
          kind: "assistant",
          text: "来自后端的消息",
          ts: "2026-05-30T00:00:00Z",
        },
      ],
    },
    diffTextByThread: {
      "thread-1": [
        "diff --git a/src/a.js b/src/a.js",
        "--- a/src/a.js",
        "+++ b/src/a.js",
        "@@ -1 +1,2 @@",
        "-old",
        "+new",
        "+extra",
      ].join("\n"),
    },
  });

  render(<App />);
  await waitForBackendThreadHeading();

  fireEvent.click(screen.getByRole("button", { name: "显示侧边栏" }));
  fireEvent.click(screen.getByRole("button", { name: "定位 src/a.js" }));

  await waitFor(() => {
    expect(backend.locateCodeFile).toHaveBeenCalledWith({
      filePath: "src/a.js",
      project: "/repo/app",
      projects: ["/repo/app"],
    });
    expect(screen.getByTestId("runtime-panel")).toHaveTextContent(
      "定位到 1 个路径",
    );
  });

  fireEvent.click(screen.getByRole("button", { name: "打开 src/a.js" }));

  const previewDialog = await screen.findByRole("dialog", { name: "文件预览" });
  expect(backend.openCodeFile).toHaveBeenCalledWith({
    filePath: "src/a.js",
    project: "/repo/app",
    projects: ["/repo/app"],
  });
  expect(within(previewDialog).getByText("src/a.js")).toBeInTheDocument();

  const previewEditor = within(previewDialog).getByLabelText("文件预览内容");
  expect(previewEditor).toHaveValue("old\nkeep");

  fireEvent.change(previewEditor, { target: { value: "new\nkeep" } });
  fireEvent.click(
    within(previewDialog).getByRole("button", { name: "保存预览更改" }),
  );

  await waitFor(() => {
    expect(backend.saveCodeFile).toHaveBeenCalledWith({
      filePath: "/repo/app/src/a.js",
      content: "new\nkeep",
      previewMode: "full",
      contentVersion: "version-src-a",
      project: "/repo/app",
      projects: ["/repo/app"],
    });
    expect(
      within(previewDialog).getByText("已保存 src/a.js"),
    ).toBeInTheDocument();
  });
});

it("opens a path choice dialog when runtime diff locate returns multiple matches", async () => {
  backend.locateCodeFile.mockResolvedValueOnce({
    ok: true,
    paths: ["/repo/app/src/a.js", "/repo/app/packages/demo/src/a.js"],
    matches: [
      { path: "/repo/app/src/a.js", relative: "src/a.js" },
      {
        path: "/repo/app/packages/demo/src/a.js",
        relative: "packages/demo/src/a.js",
      },
    ],
    truncated: true,
  });
  backend.openCodeFile.mockResolvedValueOnce({
    ok: true,
    filePath: "/repo/app/packages/demo/src/a.js",
    relative: "packages/demo/src/a.js",
    snippet: [{ line: 1, text: "chosen file" }],
  });
  backend.getThreadState.mockResolvedValue({
    activeThreadId: "thread-1",
    timelinesByThread: {
      "thread-1": [
        {
          id: "assistant-1",
          kind: "assistant",
          text: "来自后端的消息",
          ts: "2026-05-30T00:00:00Z",
        },
      ],
    },
    diffTextByThread: {
      "thread-1": [
        "diff --git a/src/a.js b/src/a.js",
        "--- a/src/a.js",
        "+++ b/src/a.js",
        "@@ -1 +1 @@",
        "-old",
        "+new",
      ].join("\n"),
    },
  });

  render(<App />);
  await waitForBackendThreadHeading();

  fireEvent.click(screen.getByRole("button", { name: "显示侧边栏" }));
  fireEvent.click(screen.getByRole("button", { name: "定位 src/a.js" }));

  const chooser = await screen.findByRole("dialog", { name: "选择文件路径" });
  expect(within(chooser).getByText("/repo/app/src/a.js")).toBeInTheDocument();
  expect(
    within(chooser).getByText("/repo/app/packages/demo/src/a.js"),
  ).toBeInTheDocument();
  expect(
    within(chooser).getByText("结果已截断，仅显示部分结果"),
  ).toBeInTheDocument();

  fireEvent.click(
    within(chooser).getByRole("button", {
      name: "/repo/app/packages/demo/src/a.js",
    }),
  );

  const previewDialog = await screen.findByRole("dialog", { name: "文件预览" });
  expect(backend.openCodeFile).toHaveBeenCalledWith({
    filePath: "/repo/app/packages/demo/src/a.js",
    project: "/repo/app",
    projects: ["/repo/app"],
  });
  expect(
    within(previewDialog).getByText("packages/demo/src/a.js"),
  ).toBeInTheDocument();
  expect(within(previewDialog).getByText("chosen file")).toBeInTheDocument();
  expect(
    within(previewDialog).queryByLabelText("文件预览内容"),
  ).not.toBeInTheDocument();
  expect(
    within(previewDialog).queryByRole("button", { name: "保存预览更改" }),
  ).not.toBeInTheDocument();
});

it("renders markdown runtime diff previews and blocks closing dirty edits", async () => {
  backend.openCodeFile.mockResolvedValueOnce({
    ok: true,
    filePath: "/repo/app/docs/readme.md",
    relative: "docs/readme.md",
    language: "markdown",
    startLine: 1,
    endLine: 3,
    totalLines: 3,
    previewMode: "full",
    contentVersion: "version-docs-readme",
    snippet: "# Guide\n\n- first step",
  });
  backend.getThreadState.mockResolvedValue({
    activeThreadId: "thread-1",
    timelinesByThread: {
      "thread-1": [
        {
          id: "assistant-1",
          kind: "assistant",
          text: "来自后端的消息",
          ts: "2026-05-30T00:00:00Z",
        },
      ],
    },
    diffTextByThread: {
      "thread-1": [
        "diff --git a/docs/readme.md b/docs/readme.md",
        "--- a/docs/readme.md",
        "+++ b/docs/readme.md",
        "@@ -1 +1 @@",
        "-old",
        "+new",
      ].join("\n"),
    },
  });

  render(<App />);
  await waitForBackendThreadHeading();

  fireEvent.click(screen.getByRole("button", { name: "显示侧边栏" }));
  fireEvent.click(screen.getByRole("button", { name: "打开 docs/readme.md" }));

  const previewDialog = await screen.findByRole("dialog", { name: "文件预览" });
  expect(
    within(previewDialog).getByRole("heading", { name: "Guide" }),
  ).toBeInTheDocument();
  expect(within(previewDialog).getByText("first step")).toBeInTheDocument();
  expect(
    within(previewDialog).queryByLabelText("文件预览内容"),
  ).not.toBeInTheDocument();

  fireEvent.click(
    within(previewDialog).getByRole("button", { name: "编辑预览" }),
  );
  const previewEditor = within(previewDialog).getByLabelText("文件预览内容");
  fireEvent.change(previewEditor, { target: { value: "# Guide\n\nchanged" } });
  fireEvent.click(
    within(previewDialog).getByRole("button", { name: "关闭文件预览" }),
  );

  expect(screen.getByRole("dialog", { name: "文件预览" })).toBeInTheDocument();
  expect(within(previewDialog).getByRole("alert")).toHaveTextContent(
    "请先保存或放弃预览更改",
  );
});

it("renders image runtime diff previews without the text editor", async () => {
  backend.openCodeFile.mockResolvedValueOnce({
    ok: true,
    image: true,
    filePath: "/repo/app/assets/logo.png",
    relative: "assets/logo.png",
    mediaType: "image/png",
    previewURL: "/local-image?id=logo_full",
    thumbnailURL: "/local-image?id=logo_thumb",
    sizeBytes: 2048,
  });
  backend.getThreadState.mockResolvedValue({
    activeThreadId: "thread-1",
    timelinesByThread: {
      "thread-1": [
        {
          id: "assistant-1",
          kind: "assistant",
          text: "来自后端的消息",
          ts: "2026-05-30T00:00:00Z",
        },
      ],
    },
    diffTextByThread: {
      "thread-1": [
        "diff --git a/assets/logo.png b/assets/logo.png",
        "--- a/assets/logo.png",
        "+++ b/assets/logo.png",
        "@@ -1 +1 @@",
        "-old",
        "+new",
      ].join("\n"),
    },
  });

  render(<App />);
  await waitForBackendThreadHeading();

  fireEvent.click(screen.getByRole("button", { name: "显示侧边栏" }));
  fireEvent.click(screen.getByRole("button", { name: "打开 assets/logo.png" }));

  const previewDialog = await screen.findByRole("dialog", { name: "文件预览" });
  const image = within(previewDialog).getByRole("img", {
    name: "assets/logo.png",
  });
  expect(image).toHaveAttribute("src", "/local-image?id=logo_thumb");
  expect(
    within(previewDialog).getByText("image/png · 2.0 KB"),
  ).toBeInTheDocument();
  expect(
    within(previewDialog).queryByLabelText("文件预览内容"),
  ).not.toBeInTheDocument();
  expect(
    within(previewDialog).queryByRole("button", { name: "保存预览更改" }),
  ).not.toBeInTheDocument();
});
