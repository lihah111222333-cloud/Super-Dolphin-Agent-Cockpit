import { testEnv } from "./test-utils/AppChatRenderingSetup.jsx";
const {
  fireEvent,
  render,
  screen,
  waitFor,
  within,
  expect,
  it,
  App,
  backend,
} = testEnv;

it("renders unfenced terminal transcripts as code blocks instead of markdown quotes", async () => {
  backend.getThreadState.mockResolvedValue({
    activeThreadId: "thread-1",
    timelinesByThread: {
      "thread-1": [
        {
          id: "assistant-terminal-transcript",
          kind: "assistant",
          text: [
            "执行结果：",
            "$ npm test",
            "> super-dolphin-frontend-app@0.1.0 test",
            "> vitest run",
            "PASS src/App.test.jsx",
          ].join("\n"),
          ts: "2026-05-30T00:00:00Z",
        },
      ],
    },
  });

  const { container } = render(<App />);

  await screen.findByText("执行结果：");
  await waitFor(() =>
    expect(
      container.querySelector(".message-markdown pre code"),
    ).toBeInTheDocument(),
  );
  const codeBlock = container.querySelector(".message-markdown pre code");
  expect(codeBlock).toHaveTextContent("$ npm test");
  expect(codeBlock).toHaveTextContent("> vitest run");
  expect(codeBlock).toHaveTextContent("PASS src/App.test.jsx");
  expect(container.querySelector(".message-markdown blockquote")).toBeNull();
});

it("renders generated local image paths from assistant replies as image previews", async () => {
  const imagePath =
    "/Users/ai/.codex/generated_images/019e8195-2f77-7aa1-96bd-63f784e87ac4/ig_088272cb55a587f8016a1d3d9660148191951c218f7b0b6c1.png";
  backend.getThreadState.mockResolvedValue({
    activeThreadId: "thread-1",
    timelinesByThread: {
      "thread-1": [
        {
          id: "assistant-image-path",
          kind: "assistant",
          text: `已展示。图片文件路径：\`${imagePath}\``,
          ts: "2026-05-30T00:00:00Z",
        },
      ],
    },
  });

  render(<App />);

  const image = await screen.findByRole("img", {
    name: "ig_088272cb55a587f8016a1d3d9660148191951c218f7b0b6c1.png",
  });
  expect(image).toHaveAttribute(
    "src",
    `/generated-image?path=${encodeURIComponent(imagePath)}`,
  );
  expect(
    screen.getByRole("button", {
      name: "放大图片 ig_088272cb55a587f8016a1d3d9660148191951c218f7b0b6c1.png",
    }),
  ).toBeInTheDocument();
  expect(screen.queryByText(imagePath)).not.toBeInTheDocument();
});

it("opens assistant image previews in an enlarged lightbox with an external link", async () => {
  const imagePath =
    "/Users/ai/.codex/generated_images/019e8195-2f77-7aa1-96bd-63f784e87ac4/ig_lightbox.png";
  const routedSrc = `/generated-image?path=${encodeURIComponent(imagePath)}`;
  backend.getThreadState.mockResolvedValue({
    activeThreadId: "thread-1",
    timelinesByThread: {
      "thread-1": [
        {
          id: "assistant-image-lightbox",
          kind: "assistant",
          text: `图片已生成：${imagePath}`,
          ts: "2026-05-30T00:00:00Z",
        },
      ],
    },
  });

  render(<App />);

  await screen.findByRole("button", { name: "放大图片 ig_lightbox.png" });
  fireEvent.click(
    screen.getByRole("button", { name: "放大图片 ig_lightbox.png" }),
  );

  const dialog = await screen.findByRole("dialog", {
    name: "图片预览：ig_lightbox.png",
  });
  expect(dialog).toHaveClass("image-lightbox-dialog");
  expect(
    within(dialog).getByRole("img", { name: "ig_lightbox.png" }),
  ).toHaveAttribute("src", routedSrc);
  expect(
    within(dialog).queryByRole("link", { name: "外部打开" }),
  ).not.toBeInTheDocument();
});

it("shows a readable fallback when a generated image preview cannot load", async () => {
  const imagePath =
    "/Users/ai/.codex/generated_images/019e8195-2f77-7aa1-96bd-63f784e87ac4/ig_missing.png";
  backend.getThreadState.mockResolvedValue({
    activeThreadId: "thread-1",
    timelinesByThread: {
      "thread-1": [
        {
          id: "assistant-missing-image-path",
          kind: "assistant",
          text: `图片文件路径：\`${imagePath}\``,
          ts: "2026-05-30T00:00:00Z",
        },
      ],
    },
  });

  render(<App />);

  await screen.findByRole("img", { name: "ig_missing.png" });
  const image = screen.getByRole("img", { name: "ig_missing.png" });
  fireEvent.error(image);

  const note = await screen.findByRole("note");
  expect(note).toHaveTextContent("图片无法加载");
  expect(note).toHaveTextContent("ig_missing.png");
});

it("renders bare generated local image paths from assistant replies as image previews", async () => {
  const imagePath =
    "/Users/ai/.codex/generated_images/019e8195-2f77-7aa1-96bd-63f784e87ac4/ig_bare_path.png";
  backend.getThreadState.mockResolvedValue({
    activeThreadId: "thread-1",
    timelinesByThread: {
      "thread-1": [
        {
          id: "assistant-bare-image-path",
          kind: "assistant",
          text: `图片已生成：${imagePath}`,
          ts: "2026-05-30T00:00:00Z",
        },
      ],
    },
  });

  render(<App />);

  const image = await screen.findByRole("img", { name: "ig_bare_path.png" });
  expect(image).toHaveAttribute(
    "src",
    `/generated-image?path=${encodeURIComponent(imagePath)}`,
  );
  expect(screen.queryByText(imagePath)).not.toBeInTheDocument();
});

it("renders local image paths in markdown image syntax through the generated image route", async () => {
  const imagePath =
    "/Users/ai/.codex/generated_images/019e8195-2f77-7aa1-96bd-63f784e87ac4/ig_markdown_path.png";
  backend.getThreadState.mockResolvedValue({
    activeThreadId: "thread-1",
    timelinesByThread: {
      "thread-1": [
        {
          id: "assistant-markdown-image-path",
          kind: "assistant",
          text: `![生成图](${imagePath})`,
          ts: "2026-05-30T00:00:00Z",
        },
      ],
    },
  });

  render(<App />);

  const image = await screen.findByRole("img", { name: "生成图" });
  expect(image).toHaveAttribute(
    "src",
    `/generated-image?path=${encodeURIComponent(imagePath)}`,
  );
});

it("renders common llm output forms with dedicated formatting", async () => {
  backend.getThreadState.mockResolvedValue({
    activeThreadId: "thread-1",
    timelinesByThread: {
      "thread-1": [
        {
          id: "assistant-json",
          kind: "assistant",
          text: '{"status":"ok","items":[{"name":"edit","count":2}]}',
          ts: "2026-05-30T00:00:00Z",
        },
        {
          id: "assistant-diff",
          kind: "assistant",
          text: [
            "diff --git a/src/a.js b/src/a.js",
            "--- a/src/a.js",
            "+++ b/src/a.js",
            "@@ -1 +1 @@",
            "-old",
            "+new",
          ].join("\n"),
          ts: "2026-05-30T00:00:01Z",
        },
        {
          id: "assistant-log",
          kind: "assistant",
          text: [
            "[ERROR] api.rpc.failed",
            "Error: boom",
            "    at run (app.js:10:2)",
          ].join("\n"),
          ts: "2026-05-30T00:00:02Z",
        },
        {
          id: "assistant-config",
          kind: "assistant",
          text: [
            "provider: codex",
            "model: gpt-5",
            "sandbox: workspace-write",
          ].join("\n"),
          ts: "2026-05-30T00:00:03Z",
        },
      ],
    },
  });

  render(<App />);

  expect(await screen.findByText(/"status": "ok"/)).toBeInTheDocument();
  const jsonBlock = document.querySelector('[data-output-kind="json"]');
  expect(jsonBlock).toBeInTheDocument();
  expect(jsonBlock).toHaveTextContent('"count": 2');

  const diffBlock = document.querySelector('[data-output-kind="diff"]');
  expect(diffBlock).toBeInTheDocument();
  expect(diffBlock.querySelector(".diff-line--deleted")).toHaveTextContent(
    "-old",
  );
  expect(diffBlock.querySelector(".diff-line--added")).toHaveTextContent(
    "+new",
  );
  expect(diffBlock.querySelector(".diff-line--hunk")).toHaveTextContent(
    "@@ -1 +1 @@",
  );

  const logBlock = document.querySelector('[data-output-kind="log"]');
  expect(logBlock).toBeInTheDocument();
  expect(logBlock).toHaveTextContent("[ERROR] api.rpc.failed");
  expect(logBlock).toHaveTextContent("at run (app.js:10:2)");

  const configBlock = document.querySelector('[data-output-kind="config"]');
  expect(configBlock).toBeInTheDocument();
  expect(configBlock).toHaveTextContent("sandbox: workspace-write");
});

it("[regression] renders streaming code blocks without showing opening code fences", async () => {
  backend.getThreadState.mockResolvedValue({
    activeThreadId: "thread-1",
    timelinesByThread: {
      "thread-1": [
        {
          id: "assistant-streaming-log",
          kind: "assistant",
          text: [
            "```log",
            "[INFO] starting server...",
            "[INFO] server listening on port 8080",
          ].join("\n"),
          ts: "2026-05-30T00:00:00Z",
        },
      ],
    },
  });

  render(<App />);

  expect(
    await screen.findByText(/\[INFO\] starting server\.\.\./),
  ).toBeInTheDocument();
  const logBlock = document.querySelector('[data-output-kind="log"]');
  expect(logBlock).toBeInTheDocument();
  expect(logBlock).toHaveTextContent("[INFO] starting server...");
  expect(logBlock).not.toHaveTextContent("```log");
});
