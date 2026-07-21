import {
  fireEvent,
  render,
  screen,
  waitFor,
  within,
} from "@testing-library/react";
import { expect } from "vitest";

function mockTraceDashboardQueryResult(ctx) {
  const { backend } = ctx;
  backend.listObservabilityRecent.mockResolvedValueOnce({
    source: "mixed",
    total_duration_ms: 135,
    truncated: false,
    slowest_events: [],
    errors: [],
    events: [
      {
        ts: "2026-06-02T09:01:20.100Z",
        trace_id: "trace-1",
        span_id: "span-rpc",
        method: "rpc.dispatch",
        status: "slow",
        duration_ms: 120,
        thread_id: "thread-1",
      },
    ],
  });
  backend.getObservabilityTrace.mockResolvedValue({
    source: "mixed",
    total_duration_ms: 135,
    truncated: false,
    slowest_events: [],
    errors: [],
    events: [
      {
        ts: "2026-06-02T09:01:19.000Z",
        trace_id: "trace-1",
        span_id: "span-begin",
        method: "tool.call.begin",
        status: "ok",
        thread_id: "thread-1",
      },
      {
        ts: "2026-06-02T09:01:20.100Z",
        trace_id: "trace-1",
        span_id: "span-rpc",
        method: "rpc.dispatch",
        status: "slow",
        duration_ms: 120,
        thread_id: "thread-1",
        parent_span_id: "span-root",
        code: {
          file: "internal/platform/rpc/server.go",
          function: "(*Server).Dispatch",
          line: 270,
        },
        stack: [
          {
            file: "internal/platform/rpc/server.go",
            function: "(*Server).Dispatch",
            line: 270,
          },
        ],
        error: "rpc dispatch exceeded slow threshold",
        metadata: { component: "rpc", route: "observability/trace/get" },
      },
      {
        ts: "2026-06-02T09:01:23.000Z",
        trace_id: "trace-1",
        span_id: "span-ui",
        method: "ui/sidebar/get",
        status: "ok",
        thread_id: "thread-1",
      },
      {
        ts: "2026-06-02T09:01:24.000Z",
        trace_id: "trace-1",
        span_id: "span-noise",
        method: "bus.event.lifecycle",
        kind: "bus_event",
        status: "dropped_summary",
        thread_id: "thread-1",
      },
    ],
  });
}

async function openTraceDashboardForTraceId(ctx) {
  const { App } = ctx;
  render(<App />);
  fireEvent.click(await screen.findByRole("button", { name: "链路追踪" }));
  fireEvent.change(await screen.findByLabelText("Trace ID"), {
    target: { value: "trace-1" },
  });
  fireEvent.click(screen.getByRole("button", { name: "查询最新日志" }));
  const table = await screen.findByTestId("observability-recent-logs");
  fireEvent.click(
    within(table).getByRole("button", { name: "打开 Trace trace-1" }),
  );
  return table;
}

function expectTraceDashboardRpcCalls(ctx) {
  const { backend } = ctx;
  expect(backend.listObservabilityRecent).toHaveBeenCalledTimes(1);
  expect(backend.listObservabilityRecent).toHaveBeenCalledWith({
    limit: 50,
    status: "",
    component: "",
    method: "",
    traceId: "trace-1",
    threadId: "",
    agentId: "",
    keyword: "",
    includeTail: true,
  });
  expect(backend.getObservabilityTrace).toHaveBeenCalledWith({
    traceId: "trace-1",
    limit: 50,
  });
}

async function expectTraceDashboardRows(ctx, table) {
  const { formatParsedTimestampForTest } = ctx;
  const inlineTrace = await within(table).findByTestId(
    "observability-inline-trace-trace-1",
  );
  await waitFor(() => expect(inlineTrace).toHaveTextContent("source=mixed"));
  expect(
    screen.getAllByText(/internal\/platform\/rpc\/server.go:270/).length,
  ).toBeGreaterThan(0);
  let traceRows = [];
  await waitFor(() => {
    traceRows = within(inlineTrace)
      .getAllByRole("listitem")
      .filter((row) => row.classList.contains("observability-event-row"));
    expect(traceRows[0]).toHaveClass("observability-event-row");
  });
  expect(traceRows[0]).not.toHaveClass("settings-row");
  expect(traceRows[0]).toHaveTextContent("120ms");
  expect(traceRows[0]).toHaveTextContent("请求上下文");
  expect(traceRows[0]).toHaveTextContent("链路标识");
  expect(traceRows[0]).toHaveTextContent("失败原因");
  const zeroDurationRow = traceRows.find((row) =>
    row.textContent.includes("ui/sidebar/get"),
  );
  expect(zeroDurationRow).toBeTruthy();
  expect(zeroDurationRow).toHaveTextContent(
    formatParsedTimestampForTest("2026-06-02T09:01:23.000Z"),
  );
  expect(zeroDurationRow).toHaveTextContent("耗时未记录");
  expect(zeroDurationRow).not.toHaveTextContent("0ms");
  expect(zeroDurationRow).not.toHaveTextContent("code=-");
  expect(traceRows[0]).toHaveTextContent("trace");
  expect(traceRows[0]).toHaveTextContent("trace-1");
  expect(traceRows[0]).toHaveTextContent("span");
  expect(traceRows[0]).toHaveTextContent("span-rpc");
  expect(traceRows[0]).toHaveTextContent("parent");
  expect(traceRows[0]).toHaveTextContent("span-root");
}

function expectTraceDashboardDetails() {
  expect(
    screen.getByText("rpc dispatch exceeded slow threshold"),
  ).toBeInTheDocument();
  expect(screen.getByText(/"component": "rpc"/)).toBeInTheDocument();
  expect(
    screen.getByText(/"route": "observability\/trace\/get"/),
  ).toBeInTheDocument();
  expect(screen.getByText(/默认显示关键事件 2\/4/)).toBeInTheDocument();
  expect(screen.getByText(/已折叠 2 条成功过程事件/)).toBeInTheDocument();
  expect(screen.queryByText("tool.call.begin")).not.toBeInTheDocument();
  expect(screen.queryByText("bus.event.lifecycle")).not.toBeInTheDocument();
}

async function showAllTraceDashboardEvents() {
  fireEvent.click(screen.getByRole("button", { name: "显示全部事件" }));
  await waitFor(() =>
    expect(screen.getAllByText("tool.call.begin").length).toBeGreaterThan(0),
  );
  expect(screen.getAllByText("bus.event.lifecycle").length).toBeGreaterThan(0);
}

function mockRecentSystemLogsResult(ctx) {
  const { backend } = ctx;
  backend.listObservabilityRecent.mockResolvedValue({
    source: "mixed",
    total_duration_ms: 38,
    truncated: false,
    slowest_events: [],
    errors: [],
    events: [
      {
        ts: "2026-06-02T09:01:22.459Z",
        trace_id: "trace-frontend-1",
        span_id: "span-ui",
        method: "thread/start",
        phase: "frontend.rpc.failed",
        kind: "frontend",
        status: "error",
        duration_ms: 33,
        thread_id: "thread-1",
        client_route: "/chat",
        error: "thread start failed",
      },
      {
        ts: "2026-06-02T09:01:20.100Z",
        trace_id: "trace-frontend-1",
        span_id: "span-rpc",
        method: "rpc.dispatch",
        kind: "rpc",
        status: "ok",
        duration_ms: 5,
        thread_id: "thread-1",
      },
      {
        ts: "2026-06-02T09:02:03.000Z",
        trace_id: "trace-frontend-2",
        span_id: "span-ui-2",
        method: "thread/config/get",
        phase: "frontend.rpc.done",
        kind: "frontend",
        status: "ok",
        duration_ms: 7,
        thread_id: "thread-2",
      },
      {
        ts: "2026-06-02T09:03:04.000Z",
        trace_id: "",
        span_id: "span-provider",
        method: "provider.session.acquire",
        kind: "provider",
        status: "ok",
        duration_ms: 3268,
        thread_id: "thread-provider",
      },
    ],
  });
  backend.getObservabilityTrace.mockResolvedValue({
    source: "mixed",
    total_duration_ms: 33,
    truncated: false,
    slowest_events: [],
    errors: [],
    events: [
      {
        trace_id: "trace-frontend-1",
        span_id: "span-ui",
        method: "thread/start",
        status: "error",
        duration_ms: 33,
      },
    ],
  });
}

async function openRecentSystemLogs(ctx) {
  const { App } = ctx;
  render(<App />);
  fireEvent.click(await screen.findByRole("button", { name: "链路追踪" }));
  fireEvent.change(await screen.findByLabelText("状态"), {
    target: { value: "error" },
  });
  fireEvent.change(screen.getByLabelText("关键词"), {
    target: { value: "thread/start" },
  });
  fireEvent.click(screen.getByRole("button", { name: "查询最新日志" }));
  return screen.findByTestId("observability-recent-logs");
}

function expectRecentSystemLogsTable(ctx, table) {
  const { formatParsedTimestampForTest } = ctx;
  expect(table).toHaveTextContent("3 条匹配 event 分组 · 4 个匹配 event");
  expect(table).toHaveTextContent(
    formatParsedTimestampForTest("2026-06-02T09:01:22.459Z"),
  );
  expect(table).toHaveTextContent(
    formatParsedTimestampForTest("2026-06-02T09:02:03.000Z"),
  );
  expect(table).toHaveTextContent(
    formatParsedTimestampForTest("2026-06-02T09:03:04.000Z"),
  );
  expect(table).not.toHaveTextContent("2026-06-02T09:01:22.459Z");
  expect(table).toHaveTextContent("thread/start");
  expect(table).toHaveTextContent("trace-frontend-1");
  expect(table).toHaveTextContent("thread start failed");
  expect(table).toHaveTextContent("provider.session.acquire");
  expect(table).toHaveTextContent("trace=-");
  expect(
    within(table).getAllByRole("button", { name: /复制 Trace ID/ }),
  ).toHaveLength(3);
  expect(
    within(table).getAllByRole("button", { name: /打开 Trace/ }),
  ).toHaveLength(3);
  expect(
    within(table).getByRole("button", { name: "复制 Trace ID -" }),
  ).toBeDisabled();
  expect(
    within(table).getByRole("button", { name: "打开 Trace -" }),
  ).toBeDisabled();
  expect(table).toHaveTextContent("2 个匹配 event");
}

function expectRecentSystemLogsRpcCall(ctx) {
  const { backend } = ctx;
  expect(backend.listObservabilityRecent).toHaveBeenCalledWith({
    limit: 50,
    status: "error",
    component: "",
    method: "",
    traceId: "",
    threadId: "",
    agentId: "",
    keyword: "thread/start",
    includeTail: true,
  });
}

async function copyTraceFromRecentLogs(ctx, table) {
  const { backend } = ctx;
  expect(screen.queryByText(/Trace 查询结果/)).not.toBeInTheDocument();
  expect(
    within(table).queryByTestId("observability-inline-trace-trace-frontend-1"),
  ).not.toBeInTheDocument();
  fireEvent.click(
    within(table).getByRole("button", {
      name: "复制 Trace ID trace-frontend-1",
    }),
  );

  await waitFor(() =>
    expect(backend.copyTextToClipboard).toHaveBeenCalledWith(
      "trace-frontend-1",
    ),
  );
  expect(
    within(table).getByRole("button", {
      name: "复制 Trace ID trace-frontend-1",
    }),
  ).toHaveTextContent("已复制");
  expect(backend.getObservabilityTrace).not.toHaveBeenCalled();
}

async function toggleInlineTraceFromRecentLogs(ctx, table) {
  const { backend } = ctx;
  fireEvent.click(
    within(table).getByRole("button", {
      name: "打开 Trace trace-frontend-1",
    }),
  );

  const inlineTrace = await within(table).findByTestId(
    "observability-inline-trace-trace-frontend-1",
  );
  await waitFor(() => {
    expect(inlineTrace).toHaveTextContent("Trace 结果");
    expect(inlineTrace).toHaveTextContent("source=mixed");
    expect(inlineTrace).toHaveTextContent("thread/start");
  });
  expect(
    within(table).getByRole("button", {
      name: "收起 Trace trace-frontend-1",
    }),
  ).toHaveAttribute("aria-expanded", "true");
  expect(backend.getObservabilityTrace).toHaveBeenCalledWith({
    traceId: "trace-frontend-1",
    limit: 50,
  });
  expect(backend.listObservabilityRecent).toHaveBeenCalledTimes(1);
  expect(table).toHaveTextContent("trace-frontend-2");

  fireEvent.click(
    within(table).getByRole("button", {
      name: "收起 Trace trace-frontend-1",
    }),
  );
  await waitFor(() =>
    expect(
      within(table).queryByTestId(
        "observability-inline-trace-trace-frontend-1",
      ),
    ).not.toBeInTheDocument(),
  );
  expect(
    within(table).getByRole("button", {
      name: "打开 Trace trace-frontend-1",
    }),
  ).toHaveAttribute("aria-expanded", "false");
  expect(backend.getObservabilityTrace).toHaveBeenCalledTimes(1);
}

export function createObservabilityFactory(ctx) {
  return {
    mockTraceDashboardQueryResult: () => mockTraceDashboardQueryResult(ctx),
    openTraceDashboardForTraceId: () => openTraceDashboardForTraceId(ctx),
    expectTraceDashboardRpcCalls: () => expectTraceDashboardRpcCalls(ctx),
    expectTraceDashboardRows: (table) => expectTraceDashboardRows(ctx, table),
    expectTraceDashboardDetails,
    showAllTraceDashboardEvents,
    mockRecentSystemLogsResult: () => mockRecentSystemLogsResult(ctx),
    openRecentSystemLogs: () => openRecentSystemLogs(ctx),
    expectRecentSystemLogsTable: (table) =>
      expectRecentSystemLogsTable(ctx, table),
    expectRecentSystemLogsRpcCall: () => expectRecentSystemLogsRpcCall(ctx),
    copyTraceFromRecentLogs: (table) => copyTraceFromRecentLogs(ctx, table),
    toggleInlineTraceFromRecentLogs: (table) =>
      toggleInlineTraceFromRecentLogs(ctx, table),
  };
}
