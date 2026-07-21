import { testEnv } from "./test-utils/AppChatRenderingSetup.jsx";
const {
  fireEvent,
  render,
  screen,
  within,
  expect,
  it,
  App,
  backend,
} = testEnv;

it("opens rendered mermaid diagrams in the enlarged preview with an external link", async () => {
  backend.getThreadState.mockResolvedValue({
    activeThreadId: "thread-1",
    timelinesByThread: {
      "thread-1": [
        {
          id: "assistant-mermaid-lightbox",
          kind: "assistant",
          text: [
            "```mermaid",
            "flowchart TD",
            "  A[开始] --> B[完成]",
            "```",
          ].join("\n"),
          ts: "2026-05-30T00:00:00Z",
        },
      ],
    },
  });

  render(<App />);

  fireEvent.click(
    await screen.findByRole("button", { name: "放大 Mermaid 图表" }),
  );

  const dialog = screen.getByRole("dialog", { name: "图片预览：Mermaid 图表" });
  expect(dialog).toHaveClass("image-lightbox-dialog");
  expect(
    within(dialog).getByRole("img", { name: "Mermaid 图表" }),
  ).toBeInTheDocument();
  expect(
    within(dialog).queryByRole("link", { name: "外部打开" }),
  ).not.toBeInTheDocument();
});

it("keeps assistant output from the thread snapshot when thread message history is stale", async () => {
  backend.getThreadState.mockResolvedValue({
    activeThreadId: "thread-1",
    timelinesByThread: {
      "thread-1": [
        {
          id: "user-stale-history",
          kind: "user",
          text: "我要图片。",
          ts: "2026-05-30T00:00:00Z",
        },
        {
          id: "assistant-visible-output",
          kind: "assistant",
          text: "这是 AI 输出。",
          ts: "2026-05-30T00:00:02Z",
        },
      ],
    },
  });
  backend.getThreadMessages.mockResolvedValue({
    messages: [
      {
        id: 1,
        role: "user",
        content: "我要图片。",
        createdAt: "2026-05-30T00:00:00Z",
      },
    ],
    total: 1,
    hasMore: false,
    nextBefore: "",
  });

  render(<App />);

  expect(await screen.findByText("这是 AI 输出。")).toBeInTheDocument();
});

it("hides injected AGENTS instructions from restored chat history", async () => {
  backend.getThreadState.mockResolvedValue({
    activeThreadId: "thread-1",
    timelinesByThread: {},
  });
  backend.getThreadMessages.mockResolvedValue({
    messages: [
      {
        id: 1,
        role: "user",
        content: [
          "# AGENTS.md instructions for /home/ai01@f666.com/桌面/project/Super-Dolphin",
          "",
          "<INSTRUCTIONS>",
          "# Super Dolphin Agent Agent Context Policy",
          "",
          "## Scope",
          "This file defines how agents should load context.",
          "</INSTRUCTIONS>",
        ].join("\n"),
        createdAt: "2026-05-30T00:00:00Z",
      },
      {
        id: 2,
        role: "user",
        content: "请修复前端渲染问题",
        createdAt: "2026-05-30T00:01:00Z",
      },
      {
        id: 3,
        role: "assistant",
        content: "已完成修复。",
        createdAt: "2026-05-30T00:02:00Z",
      },
    ],
    total: 3,
    hasMore: false,
    nextBefore: "",
  });

  render(<App />);

  expect(await screen.findByText("请修复前端渲染问题")).toBeInTheDocument();
  expect(screen.getByText("已完成修复。")).toBeInTheDocument();
  expect(screen.queryByText(/AGENTS\.md instructions/)).not.toBeInTheDocument();
  expect(
    screen.queryByText(/Super Dolphin Agent Agent Context Policy/),
  ).not.toBeInTheDocument();
});
