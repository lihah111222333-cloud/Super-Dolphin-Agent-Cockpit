import { expect, it, vi } from "vitest";
import { RPC_METHODS, createBackendApi } from "./backendApi.js";
import { expectInvalidInputDoesNotCall } from "./support/backendApi.testAssertions.js";

it("allows explicit empty skill content from skills/local/read", async () => {
  const response = {
    skill: { path: ".agents/skills/demo/SKILL.md", content: "" },
  };
  const callAPI = vi.fn().mockResolvedValue(response);
  const api = createBackendApi({ callAPI });

  await expect(api.readSkill({ cwd: "/repo/app", path: ".agents/skills/demo/SKILL.md" })).resolves.toBe(response);
  expect(callAPI).toHaveBeenCalledWith(RPC_METHODS.SKILLS_LOCAL_READ, {
    cwd: "/repo/app",
    path: ".agents/skills/demo/SKILL.md",
  });
});

it("rejects turn/start legacy prompt with legacy attachments before calling the backend", () => {
  const callAPI = vi.fn().mockResolvedValue({ ok: true });
  const api = createBackendApi({ callAPI });

  expectInvalidInputDoesNotCall(
    callAPI,
    () =>
      api.startTurn({
        cwd: "/repo/app",
        threadId: "thread-123",
        prompt: "build it",
        attachments: ["/tmp/a.txt"],
      }),
    "turn/start: prompt and attachments cannot both contain content",
  );
});

it("rejects turn/start array input with legacy attachments before calling the backend", () => {
  const callAPI = vi.fn().mockResolvedValue({ ok: true });
  const api = createBackendApi({ callAPI });

  expectInvalidInputDoesNotCall(
    callAPI,
    () =>
      api.startTurn({
        cwd: "/repo/app",
        threadId: "thread-123",
        input: [{ type: "text", text: "build it" }],
        attachments: ["/tmp/a.txt"],
      }),
    "turn/start: input and attachments cannot both contain content",
  );
});

it("rejects turn/start string input with legacy attachments before calling the backend", () => {
  const callAPI = vi.fn().mockResolvedValue({ ok: true });
  const api = createBackendApi({ callAPI });

  expectInvalidInputDoesNotCall(
    callAPI,
    () =>
      api.startTurn({
        cwd: "/repo/app",
        threadId: "thread-123",
        input: "build it",
        attachments: ["/tmp/a.txt"],
      }),
    "turn/start: input and attachments cannot both contain content",
  );
});
