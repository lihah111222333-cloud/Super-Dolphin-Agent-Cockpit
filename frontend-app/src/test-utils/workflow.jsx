import {
  act,
  fireEvent,
  render,
  screen,
  waitFor,
  within,
} from "@testing-library/react";
import { expect } from "vitest";

function createWorkflowFixtures() {
  const dag = {
    dag_key: "daily-brief",
    title: "Daily Brief",
    description: "每日简报",
    status: "ready",
    trigger: "manual",
    version: 7,
  };
  const agentNode = {
    node_key: "draft",
    title: "起草",
    node_type: "agent",
    assigned_to: "agent-a",
    depends_on: [],
    config: {
      provider: "codex",
      model: "gpt-5",
      prompt_key: "main/writer",
      first_turn: "请起草简报",
    },
  };
  return { dag, agentNode };
}

function createWorkflowThreadState(threadId) {
  if (threadId === "thread-design") {
    return {
      timelinesByThread: { "thread-design": [] },
      activeThreadId: "thread-design",
      threads: [
        {
          id: "thread-design",
          name: "AI 设计流程",
          provider: "codex",
          status: "created",
          agentKey: "dag_designer",
        },
      ],
    };
  }
  return {
    activeThreadId: "thread-1",
    threads: [
      {
        id: "thread-1",
        name: "后端线程",
        provider: "codex",
        status: "工作中",
      },
    ],
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
      "thread-1": "diff --git a/file b/file",
    },
  };
}

function getWorkflowPreference(key) {
  return (
    {
      "settings.provider.active": "codex",
      "settings.provider.codex.model": "gpt-5.5",
      "settings.provider.codex.effort": "xhigh",
      "settings.provider.codex.codexHome": "/Users/test/.codex-alt",
      "settings.provider.codex.codexInstanceKey": "desktop-main",
      "settings.provider.codex.codexModelProvider": "openrouter",
      "settings.activePromptKey": "main/reviewer",
    }[key] ?? null
  );
}

function buildWorkflowScheduleRequest() {
  const patch = {
    trigger: "scheduled",
    cron_expr: "CRON_TZ=Asia/Shanghai 0 9 * * 1-5",
  };
  const operation = { op: "update_dag", patch };
  return {
    dagKey: "daily-brief",
    baseVersion: 7,
    ops: [operation],
  };
}

function buildWorkflowStepUpdateExpectation() {
  const exec = expect.objectContaining({
    provider: "codex",
    model: "gpt-5",
    prompt_key: "main/writer",
  });
  const config = expect.objectContaining({ exec });
  const patch = expect.objectContaining({
    title: "起草 v2",
    assigned_to: "agent-b",
    depends_on: ["outline"],
    config,
  });
  const operation = expect.objectContaining({
    op: "update_node",
    node_key: "draft",
    patch,
  });
  return {
    dagKey: "daily-brief",
    baseVersion: 7,
    ops: [operation],
  };
}

async function continueChatFromFinalSharedFile() {
  const remainingFinalCard = screen.getByText("final.md").closest("article");
  fireEvent.click(
    within(remainingFinalCard).getByRole("button", {
      name: "用此文件继续对话",
    }),
  );
  const forkCard = await screen.findByTestId("fork-draft-card");
  expect(
    within(forkCard).getByText("继承自会话：后端线程"),
  ).toBeInTheDocument();
  expect(
    within(forkCard).getByRole("checkbox", {
      name: "选择共享文件 reports/final.md",
    }),
  ).toBeChecked();
}

function mockWorkflowDagLifecycle(ctx) {
  const { backend } = ctx;
  const { dag, agentNode } = createWorkflowFixtures();
  backend.getDashboardPage.mockImplementation(({ page }) =>
    Promise.resolve(page === "dags" ? { dags: [dag] } : { skills: [] }),
  );
  backend.getDagDetail.mockResolvedValue({ dag, nodes: [agentNode] });
  let hasActiveRun = false;
  backend.getDagRuns.mockImplementation(({ status }) =>
    Promise.resolve({
      runs:
        status === "running" && hasActiveRun
          ? [{ run_key: "run-live", status: "running" }]
          : [],
    }),
  );
  backend.getDagRun.mockResolvedValue({
    run: { run_key: "run-live", status: "running" },
    nodes: [agentNode],
  });
  backend.startDag.mockImplementation(() => {
    hasActiveRun = true;
    return Promise.resolve({ runKey: "run-live" });
  });
  backend.terminateDagRun.mockImplementation(() => {
    hasActiveRun = false;
    return Promise.resolve({ ok: true });
  });
  backend.getPreference.mockImplementation(({ key }) =>
    Promise.resolve(getWorkflowPreference(key)),
  );
  backend.startThread.mockResolvedValue({
    thread: { id: "thread-design" },
    provider: "codex",
    modelProvider: "codex",
  });
  backend.getThreadState.mockImplementation(({ threadId }) =>
    Promise.resolve(createWorkflowThreadState(threadId)),
  );
}

async function openWorkflowDashboard(ctx) {
  const { App } = ctx;
  render(<App />);
  fireEvent.click(await screen.findByLabelText("自动化"));
  expect(
    (await screen.findAllByText("Daily Brief")).length,
  ).toBeGreaterThanOrEqual(2);
}

async function runAndStopWorkflowDag(ctx) {
  const { backend } = ctx;
  fireEvent.click(await screen.findByRole("button", { name: "运行" }));
  await waitFor(() => {
    expect(backend.startDag).toHaveBeenCalledWith(
      expect.objectContaining({
        dagKey: "daily-brief",
        triggerSource: "manual",
      }),
    );
  });

  fireEvent.click(await screen.findByRole("button", { name: "停止运行" }));
  await waitFor(() => {
    expect(backend.terminateDagRun).toHaveBeenCalledWith({
      dagKey: "daily-brief",
      runKey: "run-live",
      reason: "user_requested",
    });
  });
  await waitFor(() =>
    expect(
      screen.queryByRole("button", { name: "停止运行" }),
    ).not.toBeInTheDocument(),
  );
}

async function createWorkflowSchedule(ctx) {
  const { backend } = ctx;
  fireEvent.click(screen.getByRole("button", { name: "创建定时任务" }));
  const scheduleDialog = await screen.findByRole("dialog", {
    name: "创建定时任务",
  });
  expect(scheduleDialog).toBeInTheDocument();
  expect(
    within(scheduleDialog).queryByLabelText("Cron 表达式"),
  ).not.toBeInTheDocument();
  fireEvent.change(within(scheduleDialog).getByLabelText("运行频率"), {
    target: { value: "weekdays" },
  });
  fireEvent.change(within(scheduleDialog).getByLabelText("运行时间"), {
    target: { value: "09:00" },
  });
  expect(
    within(scheduleDialog).getByText("工作日 09:00 自动运行"),
  ).toBeInTheDocument();
  fireEvent.click(
    within(scheduleDialog).getByRole("button", { name: "创建定时任务" }),
  );
  await waitFor(() => {
    expect(backend.applyDagOps).toHaveBeenCalledWith(
      buildWorkflowScheduleRequest(),
    );
  });
  expect(await screen.findByText("已保存定时任务")).toBeInTheDocument();
}

async function editWorkflowStep(ctx) {
  const { backend } = ctx;
  fireEvent.click(screen.getByText("高级设置"));
  fireEvent.input(screen.getByLabelText("名称"), {
    target: { value: "起草 v2" },
  });
  expect(screen.getByLabelText("名称")).toHaveValue("起草 v2");
  expect(screen.getByLabelText("执行者")).toHaveValue("agent-a");
  fireEvent.input(screen.getByLabelText("执行者"), {
    target: { value: "agent-b" },
  });
  fireEvent.change(screen.getByLabelText("依赖步骤"), {
    target: { value: "outline" },
  });
  expect(screen.queryByLabelText("Provider")).not.toBeInTheDocument();
  expect(screen.getByLabelText("执行引擎")).toHaveValue("codex");
  expect(screen.getByLabelText("Prompt Key")).toHaveValue("main/writer");
  fireEvent.click(screen.getByRole("button", { name: "保存步骤" }));
  await waitFor(() => {
    expect(backend.applyDagOps).toHaveBeenCalledWith(
      buildWorkflowStepUpdateExpectation(),
    );
  });
}

async function deleteWorkflowDag(ctx) {
  const { backend } = ctx;
  fireEvent.click(screen.getByRole("button", { name: "删除" }));
  expect(
    await screen.findByRole("dialog", { name: "删除自动化" }),
  ).toBeInTheDocument();
  fireEvent.click(screen.getByRole("button", { name: "确认删除" }));
  await waitFor(() => {
    expect(backend.deleteDag).toHaveBeenCalledWith({ dagKey: "daily-brief" });
  });
}

async function designWorkflowWithAi(ctx) {
  const { backend } = ctx;
  fireEvent.click(screen.getByRole("button", { name: "自由设计" }));
  await waitFor(() => {
    expect(backend.startThread).toHaveBeenCalledWith(
      expect.objectContaining({
        cwd: "/repo/app",
        modelProvider: "codex",
        model: "gpt-5.5",
        effort: "xhigh",
        name: "AI 设计流程",
        agentKey: "dag_designer",
        promptKey: "main/dag_designer_zh",
        deferSpawn: true,
      }),
    );
    const designPayload = backend.startThread.mock.calls.at(-1)[0];
    expect(designPayload.provider).toBe("codex");
    expect(designPayload.config).toEqual(
      expect.objectContaining({
        codexHome: "/Users/test/.codex-alt",
        codexInstanceKey: "desktop-main",
        codexModelProvider: "openrouter",
        providerNativeSkills: false,
      }),
    );
    expect(designPayload.config.enabledTools).toContain("task_start_dag");
    expect(designPayload.config.enabledTools).toContain("task_get_run");
    expect(designPayload.config.enabledTools).toContain("task_list_runs");
    expect(designPayload.config.enabledTools).toContain("task_dispatch_node");
    expect(designPayload.config.enabledTools).toContain(
      "workflow_template_list",
    );
    expect(designPayload.config.enabledTools).toContain(
      "workflow_template_get",
    );
    expect(designPayload.config.enabledTools).toContain(
      "workflow_template_render_dag",
    );
    expect(designPayload.config.enabledTools).not.toContain("task_update_node");
  });
  expect(await screen.findByRole("status")).toHaveTextContent(
    "AI 设计流程已创建",
  );
  await act(async () => {
    fireEvent.click(screen.getByRole("button", { name: "查看设计对话" }));
    await Promise.resolve();
  });
  expect(
    (await screen.findAllByText("AI 设计流程")).length,
  ).toBeGreaterThanOrEqual(1);
  expect(screen.queryByText("unknown")).not.toBeInTheDocument();
}

export function createWorkflowFactory(ctx) {
  return {
    continueChatFromFinalSharedFile,
    mockWorkflowDagLifecycle: () => mockWorkflowDagLifecycle(ctx),
    openWorkflowDashboard: () => openWorkflowDashboard(ctx),
    runAndStopWorkflowDag: () => runAndStopWorkflowDag(ctx),
    createWorkflowSchedule: () => createWorkflowSchedule(ctx),
    editWorkflowStep: () => editWorkflowStep(ctx),
    deleteWorkflowDag: () => deleteWorkflowDag(ctx),
    designWorkflowWithAi: () => designWorkflowWithAi(ctx),
  };
}
