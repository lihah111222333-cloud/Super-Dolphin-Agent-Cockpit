import { testEnv } from "./test-utils/AppChatRenderingSetup.jsx";
const {
  fireEvent,
  render,
  screen,
  waitFor,
  expect,
  it,
  mermaid,
  App,
  backend,
  decodedSvgDataUrl,
} = testEnv;

it("renders assistant markdown messages as formatted content", async () => {
  backend.getThreadState.mockResolvedValue({
    activeThreadId: "thread-1",
    timelinesByThread: {
      "thread-1": [
        {
          id: "assistant-md",
          kind: "assistant",
          text: [
            "## 结果汇总",
            "",
            "| 工具 | 结果 |",
            "| --- | --- |",
            "| edit | 可用 |",
            "",
            "> 这是一条引用",
            "",
            "- [x] 已完成",
            "- [ ] 待处理",
            "",
            "访问 [官网](https://example.com)，这是 ~~旧内容~~。",
            "",
            "---",
            "",
            "![图例](https://example.com/chart.png)",
            "",
            "<script>alert(1)</script>",
          ].join("\n"),
          ts: "2026-05-30T00:00:00Z",
        },
      ],
    },
  });

  const { container } = render(<App />);

  expect(
    await screen.findByRole("heading", { name: "结果汇总", level: 2 }),
  ).toBeInTheDocument();
  expect(screen.getByRole("table")).toBeInTheDocument();
  expect(
    screen.getByRole("columnheader", { name: "工具" }),
  ).toBeInTheDocument();
  expect(screen.getByRole("cell", { name: "可用" })).toBeInTheDocument();
  expect(
    screen.getByText("这是一条引用").closest("blockquote"),
  ).toBeInTheDocument();
  expect(screen.getByRole("link", { name: "官网" })).toHaveAttribute(
    "href",
    "https://example.com/",
  );
  expect(screen.getByText("旧内容").tagName.toLowerCase()).toBe("del");
  expect(screen.getByRole("checkbox", { name: "已完成" })).toBeChecked();
  expect(screen.getByRole("checkbox", { name: "待处理" })).not.toBeChecked();
  expect(container.querySelector("hr")).toBeInTheDocument();
  expect(screen.getByRole("img", { name: "图例" })).toHaveAttribute(
    "src",
    "https://example.com/chart.png",
  );
  expect(screen.getByText("<script>alert(1)</script>")).toBeInTheDocument();
  expect(screen.queryByText("## 结果汇总")).not.toBeInTheDocument();
});

it("copies completed AI output from the assistant message action", async () => {
  const text = [
    "这是 AI 输出。",
    "",
    "```js",
    'console.log("copy me");',
    "```",
  ].join("\n");
  backend.getThreadState.mockResolvedValue({
    activeThreadId: "thread-1",
    timelinesByThread: {
      "thread-1": [
        {
          id: "assistant-copyable",
          kind: "assistant",
          text,
          ts: "2026-05-30T00:00:00Z",
        },
      ],
    },
  });

  render(<App />);

  await screen.findByText("这是 AI 输出。");
  fireEvent.click(screen.getByRole("button", { name: "复制 AI 输出" }));

  await waitFor(() =>
    expect(backend.copyTextToClipboard).toHaveBeenCalledWith(text),
  );
  expect(
    screen.getByRole("button", { name: "复制 AI 输出" }),
  ).toHaveTextContent("已复制");
});

it("renders mermaid code fences as diagrams instead of plain code blocks", async () => {
  backend.getThreadState.mockResolvedValue({
    activeThreadId: "thread-1",
    timelinesByThread: {
      "thread-1": [
        {
          id: "assistant-mermaid",
          kind: "assistant",
          text: [
            "总体结构如下：",
            "```mermaid",
            "flowchart TD",
            "  User[用户] --> App[前端]",
            "  App --> API[后端]",
            "```",
          ].join("\n"),
          ts: "2026-05-30T00:00:00Z",
        },
      ],
    },
  });

  const { container } = render(<App />);

  expect(await screen.findByLabelText("Mermaid 图表")).toBeInTheDocument();
  const image = await screen.findByRole("img", { name: "Mermaid 图表" });
  expect(decodedSvgDataUrl(image)).toContain("flowchart TD");
  expect(container.querySelector(".mermaid-diagram")).toHaveTextContent(
    "点击放大",
  );
});

it("does not render Mermaid diagrams from unmaterialized older timeline history", async () => {
  const messages = Array.from({ length: 85 }, (_, index) => {
    if (index === 0) {
      return {
        id: "older-mermaid",
        kind: "assistant",
        text: [
          "旧 Mermaid 图表：",
          "```mermaid",
          "flowchart TD",
          "  Old[旧历史] --> Hidden[首屏隐藏]",
          "```",
        ].join("\n"),
        ts: "2026-05-30T00:00:00Z",
      };
    }
    return {
      id: `recent-${index}`,
      kind: index % 2 === 0 ? "user" : "assistant",
      text: `最近 timeline 消息 ${index}`,
      ts: "2026-05-30T00:00:00Z",
    };
  });
  backend.getThreadState.mockResolvedValue({
    activeThreadId: "thread-1",
    timelinesByThread: {
      "thread-1": messages,
    },
  });

  render(<App />);

  expect(await screen.findByText("最近 timeline 消息 84")).toBeInTheDocument();
  expect(screen.queryByText("旧 Mermaid 图表：")).not.toBeInTheDocument();
  expect(mermaid.render).not.toHaveBeenCalled();

  fireEvent.click(
    screen.getByRole("button", { name: "显示更早的消息（5 条）" }),
  );

  await waitFor(() => expect(mermaid.render).toHaveBeenCalledTimes(1));
  expect(screen.getByText("旧 Mermaid 图表：")).toBeInTheDocument();
});

it("sanitizes rendered mermaid SVG before rendering it as an image data URL", async () => {
  mermaid.render.mockResolvedValueOnce({
    svg: [
      '<svg role="img" aria-label="unsafe mermaid" onload="alert(1)">',
      "<script>alert(1)</script>",
      "<foreignObject><div>unsafe html</div></foreignObject>",
      '<a href="javascript:alert(1)"><text>unsafe link</text></a>',
      '<rect style="background: url( javascript:alert(1) )" />',
      "<text>safe mermaid</text>",
      "</svg>",
    ].join(""),
  });
  backend.getThreadState.mockResolvedValue({
    activeThreadId: "thread-1",
    timelinesByThread: {
      "thread-1": [
        {
          id: "assistant-mermaid-sanitized",
          kind: "assistant",
          text: ["```mermaid", "flowchart TD", "  A-->B", "```"].join("\n"),
          ts: "2026-05-30T00:00:00Z",
        },
      ],
    },
  });

  const { container } = render(<App />);

  await screen.findByLabelText("Mermaid 图表");
  const image = await screen.findByRole("img", { name: "Mermaid 图表" });
  const svg = decodedSvgDataUrl(image);
  expect(svg).toContain("safe mermaid");
  expect(svg).not.toContain("<script");
  expect(svg).not.toContain("foreignObject");
  expect(svg).not.toContain("onload");
  expect(svg).not.toContain("javascript:alert");
  expect(container.querySelector("script")).toBeNull();
  expect(container.querySelector("foreignObject")).toBeNull();
  expect(container.querySelector("[onload]")).toBeNull();
  expect(container.querySelector('[href^="javascript:"]')).toBeNull();
});
