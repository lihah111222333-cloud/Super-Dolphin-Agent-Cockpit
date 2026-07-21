import {
  act,
  fireEvent,
  render,
  screen,
  waitFor,
  within,
} from "@testing-library/react";
import { expect } from "vitest";

function initialSharedFiles() {
  return [
    {
      path: "reports/final.md",
      content: "final summary",
      updated_by: "dag-runner",
      updated_at: "2026-05-30T08:00:00Z",
    },
    {
      path: "scratch/work.json",
      content: '{"step":1}',
      updated_by: "agent",
      updated_at: "2026-05-30T07:00:00Z",
    },
  ];
}

function sharedFileRetention() {
  return {
    items: [
      {
        path: "reports/final.md",
        protected: true,
        cleanupCandidate: false,
        reason: "final_output",
      },
      {
        path: "scratch/work.json",
        protected: false,
        cleanupCandidate: true,
        reason: "unreferenced",
      },
    ],
    protectedCount: 1,
    cleanupCandidateCount: 1,
  };
}

function sharedFilePayload(memoryFiles) {
  return {
    files: memoryFiles,
    memory: memoryFiles,
    finalOutputRefs: [
      {
        path: "reports/final.md",
        runKey: "run-1",
        dagKey: "daily-brief",
        sourceNodeKey: "report",
      },
    ],
    sharedFileRetention: sharedFileRetention(),
  };
}

function createSharedFileState() {
  let memoryFiles = initialSharedFiles();
  return {
    payload: () => sharedFilePayload(memoryFiles),
    add(file) {
      memoryFiles = [...memoryFiles, file];
    },
    remove(path) {
      memoryFiles = memoryFiles.filter((item) => item.path !== path);
    },
  };
}

function mockSharedFileWorkflow(backend, sharedFiles) {
  backend.listSharedFiles.mockImplementation(() =>
    Promise.resolve(sharedFiles.payload()),
  );
  backend.readSharedFile.mockImplementation(({ path }) =>
    Promise.resolve({
      path,
      content:
        path === "reports/final.md"
          ? "FINAL CONTENT"
          : '{"step":1,"detail":true}',
      updatedBy: path === "reports/final.md" ? "dag-runner" : "agent",
      updatedAt: "2026-05-30T08:30:00Z",
    }),
  );
  backend.deleteSharedFile.mockImplementation(({ path }) => {
    sharedFiles.remove(path);
    return Promise.resolve({ deleted: true });
  });
  backend.saveTextFile.mockResolvedValue("/exports/work.json");
}

async function openSharedFilesPage(ctx) {
  const { backend, App, waitForBackendThreadHeading } = ctx;
  render(<App />);
  await waitForBackendThreadHeading();
  fireEvent.click(screen.getByLabelText("共享文件"));

  expect(await screen.findByText("final.md")).toBeInTheDocument();
  expect(screen.getByText("work.json")).toBeInTheDocument();
  expect(
    screen.queryByRole("button", { name: /刷新/ }),
  ).not.toBeInTheDocument();
  expect(screen.getByRole("tab", { name: "全部 2" })).toBeInTheDocument();
  expect(screen.getByRole("tab", { name: "最终产物 1" })).toBeInTheDocument();
  expect(screen.getByRole("tab", { name: "工作文件 1" })).toBeInTheDocument();
  await waitFor(() => {
    expect(backend.listSharedFiles).toHaveBeenCalledWith();
  });
}

async function refreshSharedFilesFromBridge(ctx, sharedFiles) {
  sharedFiles.add({
    path: "scratch/notes.md",
    content: "fresh notes",
    updated_by: "agent",
    updated_at: "2026-05-30T09:00:00Z",
  });
  await act(async () => {
    ctx.bridgeCallback?.({
      type: "ui/shared-files/changed",
      payload: { path: "scratch/notes.md", action: "write" },
    });
  });
  expect(await screen.findByText("notes.md")).toBeInTheDocument();
  expect(screen.getByRole("tab", { name: "全部 3" })).toBeInTheDocument();
  expect(screen.getByRole("tab", { name: "工作文件 2" })).toBeInTheDocument();
}

async function refreshSharedFilesFromFocus(sharedFiles) {
  sharedFiles.add({
    path: "scratch/focus-refresh.md",
    content: "focus refresh",
    updated_by: "agent",
    updated_at: "2026-05-30T09:01:00Z",
  });
  await act(async () => {
    window.dispatchEvent(new Event("focus"));
  });
  expect(await screen.findByText("focus-refresh.md")).toBeInTheDocument();
  expect(screen.getByRole("tab", { name: "全部 4" })).toBeInTheDocument();
  expect(screen.getByRole("tab", { name: "工作文件 3" })).toBeInTheDocument();
}

async function previewFinalSharedFile(backend) {
  const finalCard = screen.getByText("final.md").closest("article");
  expect(within(finalCard).getByText("最终产物")).toBeInTheDocument();
  expect(
    within(finalCard).getByRole("button", { name: "不可删除" }),
  ).toBeDisabled();
  fireEvent.click(within(finalCard).getByRole("button", { name: "打开" }));

  expect(
    await screen.findByRole("dialog", { name: "文件预览" }),
  ).toBeInTheDocument();
  expect(screen.getByText("FINAL CONTENT")).toBeInTheDocument();
  expect(backend.readSharedFile).toHaveBeenCalledWith({
    path: "reports/final.md",
  });
  fireEvent.click(screen.getByRole("button", { name: "关闭" }));
}

async function exportAndDeleteWorkSharedFile(backend) {
  const workCard = screen.getByText("work.json").closest("article");
  fireEvent.click(within(workCard).getByRole("button", { name: "导出" }));
  await waitFor(() => {
    expect(backend.saveTextFile).toHaveBeenCalledWith({
      defaultPath: "/repo/app",
      defaultFilename: "work.json",
      content: '{"step":1,"detail":true}',
    });
  });
  expect(
    await screen.findByText(/已保存到：\/exports\/work\.json/),
  ).toBeInTheDocument();

  fireEvent.click(within(workCard).getByRole("button", { name: "删除" }));
  expect(
    await screen.findByRole("dialog", { name: "删除文件" }),
  ).toBeInTheDocument();
  fireEvent.click(screen.getByRole("button", { name: "确认删除" }));
  await waitFor(() => {
    expect(backend.deleteSharedFile).toHaveBeenCalledWith({
      path: "scratch/work.json",
    });
  });
  expect(
    await screen.findByText(/已删除文件：scratch\/work\.json/),
  ).toBeInTheDocument();
}

export function createSharedFilesFactory(ctx) {
  const { backend } = ctx;
  return {
    createSharedFileState,
    mockSharedFileWorkflow: (sharedFiles) =>
      mockSharedFileWorkflow(backend, sharedFiles),
    openSharedFilesPage: () => openSharedFilesPage(ctx),
    refreshSharedFilesFromBridge: (sharedFiles) =>
      refreshSharedFilesFromBridge(ctx, sharedFiles),
    refreshSharedFilesFromFocus,
    previewFinalSharedFile: () => previewFinalSharedFile(backend),
    exportAndDeleteWorkSharedFile: () => exportAndDeleteWorkSharedFile(backend),
  };
}
