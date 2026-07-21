import { installAppTestHooks, testEnv } from "./test-utils/appTestHarness.jsx";

installAppTestHooks();
const {
  React,
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
  useClientStore,
  App,
  backend,
  waitForBackendThreadHeading,
} = testEnv;

it("disables thread-scoped chat buttons before a backend thread exists", async () => {
  await import("./pages/chat/ChatPage.jsx");
  resetClientStoreForTests({
    bootstrapStatus: "ready",
    cwd: "/repo/app",
    activeProject: "/repo/app",
    activeThreadId: "",
    threads: [],
    timelinesByThread: {},
  });

  render(<App skipBootstrap />);

  await screen.findByText("我们应该在 燧元 中构建什么？");
  expect(screen.queryByLabelText("复制当前线程")).not.toBeInTheDocument();
  expect(screen.queryByLabelText("停止")).not.toBeInTheDocument();
  expect(screen.queryByLabelText("线程状态")).not.toBeInTheDocument();
  expect(screen.queryByLabelText("压缩当前线程")).not.toBeInTheDocument();
  expect(screen.queryByLabelText("选择附件")).not.toBeInTheDocument();
  expect(screen.queryByLabelText("权限")).not.toBeInTheDocument();
  expect(screen.queryByLabelText("请先选择会话")).not.toBeInTheDocument();
  expect(
    screen.queryByRole("button", { name: "自定义配置" }),
  ).not.toBeInTheDocument();
  expect(
    screen.queryByRole("button", { name: "语音输入" }),
  ).not.toBeInTheDocument();
  expect(screen.getByLabelText("添加文件")).toBeInTheDocument();
  expect(screen.queryByLabelText("发送权限")).not.toBeInTheDocument();
  expect(screen.getByLabelText("会话列表")).toBeInTheDocument();
  expect(screen.getByLabelText("0 个 Agent")).toBeInTheDocument();
  expect(screen.getByLabelText("打开归档列表")).toBeEnabled();
  expect(
    screen.getByText("暂无会话，点击「新建对话」开始草稿"),
  ).toBeInTheDocument();
});

it("disables thread-scoped chat buttons when the active backend thread is archived", async () => {
  backend.getSidebarState.mockResolvedValue({
    activeThreadId: "essay_agent_15",
    threads: [
      {
        id: "essay_agent_15",
        name: "作文Agent-15",
        provider: "codex",
        status: "archived",
      },
    ],
  });
  backend.getThreadState.mockResolvedValue({ timelinesByThread: {} });

  render(<App />);

  await screen.findByText("我们应该在 燧元 中构建什么？");
  expect(screen.queryByLabelText("复制当前线程")).not.toBeInTheDocument();
  expect(screen.queryByLabelText("线程状态")).not.toBeInTheDocument();
  expect(screen.queryByLabelText("停止")).not.toBeInTheDocument();
  expect(screen.queryByLabelText("强制完成")).not.toBeInTheDocument();
  expect(screen.queryByLabelText("请先选择会话")).not.toBeInTheDocument();
  expect(
    screen.queryByRole("button", { name: "自定义配置" }),
  ).not.toBeInTheDocument();
  expect(
    screen.queryByRole("button", { name: "语音输入" }),
  ).not.toBeInTheDocument();
  expect(screen.queryByText("作文Agent-15")).not.toBeInTheDocument();
  expect(backend.getThreadState).not.toHaveBeenCalledWith(
    expect.objectContaining({ threadId: "essay_agent_15" }),
  );
});

it("connects ComposerMeta attachments as plain arrays and conversation operation buttons", async () => {
  backend.selectFiles.mockResolvedValue(["/tmp/a.txt"]);
  backend.resolveThreadIdentity.mockResolvedValue({
    id: "thread-1",
    providerThreadId: "provider-thread-1",
    agent_id: "agent-1",
  });

  render(<App />);
  await waitForBackendThreadHeading();

  fireEvent.click(screen.getByLabelText("添加文件"));
  expect(await screen.findByText("a.txt")).toBeInTheDocument();

  fireEvent.click(screen.getByLabelText("复制当前线程"));
  fireEvent.click(screen.getByLabelText("停止"));
  fireEvent.click(screen.getByLabelText("强制完成"));
  fireEvent.click(screen.getByLabelText("进程恢复"));
  expect(screen.queryByLabelText("归档会话")).not.toBeInTheDocument();

  await waitFor(() => {
    expect(backend.selectFiles).toHaveBeenCalledWith();
    expect(JSON.parse(backend.copyTextToClipboard.mock.calls[0][0])).toEqual(
      expect.objectContaining({
        agentId: "agent-1",
        providerThreadId: "provider-thread-1",
        provider: "codex",
      }),
    );
    expect(backend.interruptTurn).toHaveBeenCalledWith(
      expect.objectContaining({
        cwd: "/repo/app",
        threadId: "thread-1",
        expectedTurnId: expect.any(String),
        requestId: expect.any(String),
        source: "ui_stop",
      }),
    );
    expect(backend.forceCompleteTurn).toHaveBeenCalledWith({
      cwd: "/repo/app",
      threadId: "thread-1",
    });
    expect(backend.recoverThread).toHaveBeenCalledWith({
      cwd: "/repo/app",
      threadId: "thread-1",
    });
    expect(backend.archiveThread).not.toHaveBeenCalled();
  });
});

it("submits timeline approval decisions from the React chat timeline", async () => {
  backend.respondApproval.mockResolvedValue(null);
  backend.getThreadState.mockResolvedValue({
    activeThreadId: "thread-1",
    timelinesByThread: {
      "thread-1": [
        {
          id: "approval-1",
          role: "assistant",
          kind: "approval",
          title: "shell",
          text: "需要执行 deploy 命令",
          sessionScope: "session-scope-a",
          callId: "call-11",
          requestId: 11,
          status: "pending",
          ts: "2026-05-30T00:00:00Z",
        },
      ],
    },
  });

  render(<App />);

  expect(await screen.findByTestId("approval-request-11")).toHaveTextContent(
    "需要执行 deploy 命令",
  );
  fireEvent.click(screen.getByRole("button", { name: "同意" }));
  expect(backend.respondApproval).not.toHaveBeenCalled();
  fireEvent.click(screen.getByRole("button", { name: "确认选择" }));

  await waitFor(() => {
    expect(backend.respondApproval).toHaveBeenCalledWith({
      sessionScope: "session-scope-a",
      callId: "call-11",
      requestId: 11,
      approved: true,
    });
  });
  expect(screen.getByRole("button", { name: "同意" })).toBeDisabled();
  expect(screen.getByRole("button", { name: "确认选择" })).toBeDisabled();
});

it("interrupts the selected conversation when Escape is pressed", async () => {
  const interruptActiveThread = vi.spyOn(
    useClientStore.getState(),
    "interruptActiveThread",
  );
  try {
    render(<App />);
    await waitForBackendThreadHeading();

    fireEvent.keyDown(window, { key: "Escape", code: "Escape" });

    await waitFor(() => {
      expect(interruptActiveThread).toHaveBeenCalledTimes(1);
      expect(backend.interruptTurn).toHaveBeenCalledWith(
        expect.objectContaining({
          cwd: "/repo/app",
          threadId: "thread-1",
          expectedTurnId: expect.any(String),
          requestId: expect.any(String),
          source: "ui_stop",
        }),
      );
    });
  } finally {
    interruptActiveThread.mockRestore();
  }
});

it("leaves Escape to an open local surface without interrupting or preventing it", async () => {
  render(<App />);
  await waitForBackendThreadHeading();
  const localSurface = document.createElement("div");
  localSurface.setAttribute("role", "dialog");
  document.body.append(localSurface);

  const event = new KeyboardEvent("keydown", {
    key: "Escape",
    code: "Escape",
    bubbles: true,
    cancelable: true,
  });
  act(() => window.dispatchEvent(event));

  expect(event.defaultPrevented).toBe(false);
  expect(backend.interruptTurn).not.toHaveBeenCalled();
  localSurface.remove();
});

it("does not interrupt the selected conversation when Escape is handled by the composer", async () => {
  render(<App />);
  await waitForBackendThreadHeading();

  const input = screen.getByTestId("composer-input");
  input.focus();
  fireEvent.keyDown(input, { key: "Escape", code: "Escape" });

  expect(backend.interruptTurn).not.toHaveBeenCalled();
});

it("does not send an invalid interrupt when a running conversation has no active turn id", async () => {
  backend.getSidebarState.mockResolvedValueOnce({
    activeThreadId: "thread-1",
    threads: [
      { id: "thread-1", name: "后端线程", provider: "codex", status: "工作中" },
    ],
  });

  render(<App />);
  await waitForBackendThreadHeading();

  fireEvent.keyDown(window, { key: "Escape", code: "Escape" });

  await waitFor(() =>
    expect(screen.getByTestId("chat-action-feedback")).toHaveTextContent(
      "当前没有可中断任务",
    ),
  );
  expect(backend.interruptTurn).not.toHaveBeenCalled();
});

it("previews attachments on click and removes them only with the remove control", async () => {
  backend.selectFiles.mockResolvedValue(["/tmp/a.txt"]);

  render(<App />);
  await waitForBackendThreadHeading();

  fireEvent.click(screen.getByLabelText("添加文件"));
  const attachment = await screen.findByRole("button", {
    name: /预览附件 a\.txt/,
  });
  fireEvent.click(attachment);

  const dialog = screen.getByRole("dialog", { name: "附件预览" });
  expect(dialog).toBeInTheDocument();
  expect(dialog).toHaveTextContent("a.txt");
  expect(dialog).not.toHaveTextContent("/tmp/a.txt");
  expect(
    screen.getByRole("button", { name: /预览附件 a\.txt/ }),
  ).toBeInTheDocument();

  fireEvent.click(screen.getByLabelText("关闭附件预览"));
  fireEvent.click(screen.getByLabelText("移除附件 a.txt"));

  expect(
    screen.queryByRole("button", { name: /预览附件 a\.txt/ }),
  ).not.toBeInTheDocument();
});

it("traps focus in the attachment preview and restores focus after Escape", async () => {
  backend.selectFiles.mockResolvedValue(["/tmp/a.txt"]);

  render(<App />);
  await waitForBackendThreadHeading();

  fireEvent.click(screen.getByLabelText("添加文件"));
  const attachment = await screen.findByRole("button", {
    name: /预览附件 a\.txt/,
  });
  attachment.focus();
  fireEvent.click(attachment);

  const dialog = screen.getByRole("dialog", { name: "附件预览" });
  const closeIcon = within(dialog).getByLabelText("关闭附件预览");
  const closeText = within(dialog).getByRole("button", { name: "关闭" });
  await waitFor(() => {
    expect(document.activeElement).toBe(closeIcon);
  });

  fireEvent.keyDown(dialog, { key: "Tab", code: "Tab", shiftKey: true });
  expect(document.activeElement).toBe(closeText);
  fireEvent.keyDown(dialog, { key: "Tab", code: "Tab" });
  expect(document.activeElement).toBe(closeIcon);
  fireEvent.keyDown(dialog, { key: "Escape", code: "Escape" });

  await waitFor(() => {
    expect(
      screen.queryByRole("dialog", { name: "附件预览" }),
    ).not.toBeInTheDocument();
  });
  expect(document.activeElement).toBe(attachment);
  expect(backend.interruptTurn).not.toHaveBeenCalled();
});

it("adds pasted images and dropped files to the composer attachments", async () => {
  backend.saveClipboardImage.mockResolvedValue("/tmp/pasted.png");

  render(<App />);
  await waitForBackendThreadHeading();

  const input = screen.getByTestId("composer-input");
  const image = new File(["png"], "shot.png", { type: "image/png" });
  fireEvent.paste(input, {
    clipboardData: {
      files: [image],
      items: [],
      getData: () => "",
    },
  });

  expect(
    await screen.findByRole("button", { name: /预览附件 shot\.png/ }),
  ).toBeInTheDocument();

  const dropped = new File(["txt"], "notes.txt", { type: "text/plain" });
  Object.defineProperty(dropped, "path", { value: "/tmp/notes.txt" });
  fireEvent.drop(screen.getByTestId("composer-dock"), {
    dataTransfer: {
      files: [dropped],
      items: [],
      types: ["Files"],
    },
  });

  expect(
    await screen.findByRole("button", { name: /预览附件 notes\.txt/ }),
  ).toBeInTheDocument();

  fireEvent.paste(input, {
    clipboardData: {
      files: [],
      items: [],
      types: ["x-special/gnome-copied-files", "text/uri-list", "text/plain"],
      getData: (type) => {
        if (type === "x-special/gnome-copied-files")
          return "copy\nfile:///tmp/desktop-copy.txt";
        if (type === "text/uri-list") return "file:///tmp/desktop-copy.txt";
        if (type === "text/plain") return "/tmp/desktop-copy.txt";
        return "";
      },
    },
  });

  expect(
    await screen.findByRole("button", { name: /预览附件 desktop-copy\.txt/ }),
  ).toBeInTheDocument();
  expect(backend.saveClipboardImage).toHaveBeenCalledWith(expect.any(String));
});

it("accepts native Wails file drops on the text editor target", async () => {
  let nativeDropHandler = null;
  backend.onFilesDropped.mockImplementation((handler) => {
    nativeDropHandler = handler;
    return () => {};
  });

  render(<App />);
  await waitForBackendThreadHeading();

  const composer = screen.getByTestId("composer-dock");
  const input = screen.getByTestId("composer-input");
  const conversation = screen.getByTestId("conversation-drop-zone");
  expect(composer).toHaveAttribute("data-file-drop-target");
  expect(input).toHaveAttribute("id", "composer-input");
  expect(input).toHaveAttribute("data-file-drop-target");
  expect(conversation).toHaveAttribute("id", "conversation-drop-zone");
  expect(conversation).toHaveAttribute("data-file-drop-target");

  act(() => {
    nativeDropHandler({
      files: ["/tmp/native-editor-drop.txt"],
      details: {
        id: "composer-input",
        classList: [],
        attributes: { "data-file-drop-target": "" },
      },
    });
  });

  expect(
    await screen.findByRole("button", {
      name: /预览附件 native-editor-drop\.txt/,
    }),
  ).toBeInTheDocument();

  act(() => {
    nativeDropHandler({
      name: "files-dropped",
      data: {
        files: ["/tmp/native-wails-event-drop.txt"],
        details: {
          id: "composer-input",
          classList: [],
          attributes: { "data-file-drop-target": "" },
        },
      },
    });
  });

  expect(
    await screen.findByRole("button", {
      name: /预览附件 native-wails-event-drop\.txt/,
    }),
  ).toBeInTheDocument();

  act(() => {
    nativeDropHandler({
      payload: {
        files: ["/tmp/native-payload-drop.txt"],
        details: {
          id: "composer-input",
          classList: [],
          attributes: { "data-file-drop-target": "" },
        },
      },
    });
  });

  expect(
    await screen.findByRole("button", {
      name: /预览附件 native-payload-drop\.txt/,
    }),
  ).toBeInTheDocument();

  act(() => {
    nativeDropHandler({
      files: ["/tmp/native-composer-bar-drop.txt"],
      details: {
        id: "chat-input-bar",
        classList: ["composer"],
        attributes: { "data-file-drop-target": "" },
      },
    });
  });

  expect(
    await screen.findByRole("button", {
      name: /预览附件 native-composer-bar-drop\.txt/,
    }),
  ).toBeInTheDocument();

  act(() => {
    nativeDropHandler({
      files: ["/tmp/native-conversation-drop.txt"],
      details: {
        id: "conversation-drop-zone",
        classList: ["conversation"],
        attributes: { "data-file-drop-target": "" },
      },
    });
  });

  expect(
    await screen.findByRole("button", {
      name: /预览附件 native-conversation-drop\.txt/,
    }),
  ).toBeInTheDocument();

  act(() => {
    nativeDropHandler({
      files: ["/tmp/native-unknown-target-drop.txt"],
      details: {
        id: "timeline-inner-node",
        classList: ["timeline-inner-node"],
        attributes: { "data-testid": "timeline-inner-node" },
      },
    });
  });

  await waitFor(() => {
    expect(
      screen.queryByRole("button", {
        name: /预览附件 native-unknown-target-drop\.txt/,
      }),
    ).not.toBeInTheDocument();
  });
});
