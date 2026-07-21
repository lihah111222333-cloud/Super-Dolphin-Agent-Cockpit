import { beforeEach, describe, expect, it, vi } from "vitest";

vi.mock("../../../../../shared/api/sessionApi.js", () => ({
  sessionApi: {
    startTurn: vi.fn(),
  },
}));

import { sessionApi } from "../../../../../shared/api/sessionApi.js";
import { createDashboardCommandActions } from "./clientStoreDashboardCommandActions.js";

describe("createDashboardCommandActions", () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  it("sends a dashboard command through the existing active thread", async () => {
    const state = {
      activeThreadId: "thread-1",
      activityThreadAtById: {},
      attachments: [],
      composerCapabilities: [],
      draft: "",
      threads: [{ id: "thread-1", cwd: "/repo/app" }],
      timelinesByThread: {},
    };
    const runtime = {
      addWarning: vi.fn(),
      clearComposerDraft: vi.fn(),
      get: () => state,
      requireCwd: vi.fn(() => "/repo/app"),
      set: vi.fn((patch) => Object.assign(state, typeof patch === "function" ? patch(state) : patch)),
    };
    sessionApi.startTurn.mockResolvedValue(undefined);

    await expect(
      createDashboardCommandActions(runtime).runDashboardCommand({
        command_template: "git status --short",
      }),
    ).resolves.toBe(true);

    expect(sessionApi.startTurn).toHaveBeenCalledWith({
      cwd: "/repo/app",
      input: [{
        text: "请执行以下命令并反馈结果：\ngit status --short",
        type: "text",
      }],
      manualSkillSelection: false,
      threadId: "thread-1",
    });
    expect(runtime.clearComposerDraft).toHaveBeenCalledTimes(3);
  });
});
