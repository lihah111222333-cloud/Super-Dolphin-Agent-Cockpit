import { testEnv } from "./test-utils/AppChatRenderingSetup.jsx";
const {
  render,
  screen,
  waitFor,
  expect,
  it,
  App,
  backend,
} = testEnv;

it("renders malformed inline markdown fences as readable code blocks", async () => {
  backend.getThreadState.mockResolvedValue({
    activeThreadId: "thread-1",
    timelinesByThread: {
      "thread-1": [
        {
          id: "assistant-inline-fence",
          kind: "assistant",
          text: [
            "下面是当前仓库结构： ```textSuper-Dolphin/",
            "├── cmd/#可执行入口",
            "├── frontend-app/#当前前端",
            "└── README.md",
          ].join("\n"),
          ts: "2026-05-30T00:00:00Z",
        },
      ],
    },
  });

  const { container } = render(<App />);

  await screen.findByText("下面是当前仓库结构：");
  await waitFor(() =>
    expect(
      container.querySelector(".message-markdown pre"),
    ).toBeInTheDocument(),
  );
  const codeBlock = container.querySelector(".message-markdown pre");
  expect(codeBlock).toHaveTextContent("Super-Dolphin/");
  expect(codeBlock).toHaveTextContent("frontend-app/#当前前端");
  expect(codeBlock).not.toHaveTextContent("```");
  expect(screen.queryByText(/```textSuper-Dolphin/)).not.toBeInTheDocument();
});

it("renders common markdown code fence variants without leaking fence metadata", async () => {
  backend.getThreadState.mockResolvedValue({
    activeThreadId: "thread-1",
    timelinesByThread: {
      "thread-1": [
        {
          id: "assistant-common-code-fences",
          kind: "assistant",
          text: [
            "常见代码块：",
            "",
            "~~~bash",
            "npm run lint",
            "~~~",
            "",
            '```bash title="frontend test"',
            "npm test",
            "```",
            "",
            "```js {1,3}",
            "const value = 1;",
            "console.log(value);",
            "```",
            "",
            "缩进代码：",
            "    pnpm install",
            "    pnpm test",
          ].join("\n"),
          ts: "2026-05-30T00:00:00Z",
        },
      ],
    },
  });

  const { container } = render(<App />);

  await screen.findByText("常见代码块：");
  await waitFor(() =>
    expect(
      container.querySelectorAll(".message-markdown pre code"),
    ).toHaveLength(4),
  );
  const codeBlocks = Array.from(
    container.querySelectorAll(".message-markdown pre code"),
  );
  expect(codeBlocks).toHaveLength(4);
  expect(codeBlocks[0]).toHaveTextContent("npm run lint");
  expect(codeBlocks[1]).toHaveTextContent("npm test");
  expect(codeBlocks[1]).not.toHaveTextContent('title="frontend test"');
  expect(codeBlocks[2]).toHaveTextContent("const value = 1;");
  expect(codeBlocks[2]).not.toHaveTextContent("{1,3}");
  expect(codeBlocks[3]).toHaveTextContent("pnpm install");
  expect(codeBlocks[3]).toHaveTextContent("pnpm test");
  expect(screen.queryByText(/~~~bash/)).not.toBeInTheDocument();
});
