import { expect, it, vi } from "vitest";
import {
  RPC_METHODS,
  createBackendApi,
  exportMCPToolLifecycle,
  listMCPToolLifecycle,
  setMCPToolLifecycle,
} from "./backendApi.js";
import { expectInvalidInputDoesNotCall } from "./support/backendApi.testAssertions.js";

it("rejects MCP server public responses that include config details", async () => {
  const leakedListAPI = createBackendApi({
    callAPI: vi.fn().mockResolvedValue({
      configPath: "/repo/.agent/mcp_server/config.json",
      mcpServers: {
        sqlite: {
          enabled: true,
          headers: { Authorization: "Bearer YOUR_API_KEY" },
        },
      },
    }),
  });

  await expect(leakedListAPI.listMCPServers()).rejects.toThrow("must not include headers");

  const leakedStartAPI = createBackendApi({
    callAPI: vi.fn().mockResolvedValue({
      configPath: "/repo/.agent/mcp_server/config.json",
      serverName: "sqlite",
      enabled: true,
      config: { command: "npx", args: ["@bytebase/dbhub@0.23.0"] },
    }),
  });

  await expect(leakedStartAPI.startSQLiteMCPServer()).rejects.toThrow("must not include config");
});

it("rejects malformed MCP server default control responses", async () => {
  for (const action of [
    "startSQLiteMCPServer",
    "stopSQLiteMCPServer",
    "startPlaywrightMCPServer",
    "stopPlaywrightMCPServer",
  ]) {
    const api = createBackendApi({
      callAPI: vi.fn().mockResolvedValue({
        configPath: "/repo/.agent/mcp_server/config.json",
      }),
    });

    await expect(api[action]()).rejects.toThrow("serverName");
  }
});

it("wraps MCP tool lifecycle RPC methods with guarded canonical payloads", async () => {
  const setResponse = {
    serverName: "my-search",
    toolName: "remote_search",
    state: "disabled",
  };
  const listResponse = [{ serverName: "my-search", toolName: "remote_search", state: "disabled" }];
  const exportResponse = [
    { serverName: "my-search", toolName: "remote_search", state: "disabled" },
    { serverName: "my-worker", toolName: "remote_worker", state: "suspended" },
  ];
  const callAPI = vi
    .fn()
    .mockResolvedValueOnce(setResponse)
    .mockResolvedValueOnce(listResponse)
    .mockResolvedValueOnce(exportResponse);
  const api = createBackendApi({ callAPI });

  await expect(
    api.setMCPToolLifecycle({
      workspace_root: " /repo ",
      server_name: " my-search ",
      manifest_name: " search_v1 ",
      tool_name: " remote_search ",
      state: "disabled",
      reason: " manual review ",
      replacement_tool: " remote_search_v2 ",
    }),
  ).resolves.toEqual(setResponse);
  await expect(
    api.listMCPToolLifecycle({
      workspaceRoot: " /repo ",
      serverName: " my-search ",
    }),
  ).resolves.toEqual(listResponse);
  await expect(
    api.exportMCPToolLifecycle({
      workspace_root: " /repo ",
    }),
  ).resolves.toEqual(exportResponse);

  expect(callAPI).toHaveBeenNthCalledWith(1, RPC_METHODS.MCP_TOOL_LIFECYCLE_SET, {
    workspaceRoot: "/repo",
    serverName: "my-search",
    manifestName: "search_v1",
    toolName: "remote_search",
    state: "disabled",
    reason: "manual review",
    replacementTool: "remote_search_v2",
  });
  expect(callAPI).toHaveBeenNthCalledWith(2, RPC_METHODS.MCP_TOOL_LIFECYCLE_LIST, {
    workspaceRoot: "/repo",
    serverName: "my-search",
  });
  expect(callAPI).toHaveBeenNthCalledWith(3, RPC_METHODS.MCP_TOOL_LIFECYCLE_EXPORT, {
    workspaceRoot: "/repo",
  });
  expect(typeof setMCPToolLifecycle).toBe("function");
  expect(typeof listMCPToolLifecycle).toBe("function");
  expect(typeof exportMCPToolLifecycle).toBe("function");
});

it("fails fast for invalid MCP tool lifecycle facade inputs", () => {
  const callAPI = vi.fn().mockResolvedValue({ ok: true });
  const api = createBackendApi({ callAPI });

  expectInvalidInputDoesNotCall(callAPI, () => api.setMCPToolLifecycle([]), "params must be an object");
  expectInvalidInputDoesNotCall(
    callAPI,
    () =>
      api.setMCPToolLifecycle({
        toolName: "remote_search",
        state: "disabled",
      }),
    "serverName is required",
  );
  expectInvalidInputDoesNotCall(
    callAPI,
    () =>
      api.setMCPToolLifecycle({
        serverName: "my-search",
        state: "disabled",
      }),
    "toolName is required",
  );
  expectInvalidInputDoesNotCall(
    callAPI,
    () =>
      api.setMCPToolLifecycle({
        serverName: "my-search",
        toolName: "remote_search",
      }),
    "state is required",
  );
  expectInvalidInputDoesNotCall(
    callAPI,
    () =>
      api.setMCPToolLifecycle({
        serverName: "my-search",
        toolName: "remote_search",
        state: "unknown",
      }),
    "state must be enabled, disabled, suspended, or removed",
  );
  expectInvalidInputDoesNotCall(
    callAPI,
    () =>
      api.setMCPToolLifecycle({
        serverName: "my-search",
        toolName: { name: "remote_search" },
        state: "disabled",
      }),
    "toolName must be a string",
  );
  expectInvalidInputDoesNotCall(
    callAPI,
    () =>
      api.listMCPToolLifecycle({
        serverName: "my-search",
        extra: true,
      }),
    "unsupported payload field extra",
  );
  expectInvalidInputDoesNotCall(
    callAPI,
    () =>
      api.exportMCPToolLifecycle({
        serverName: "my-search",
      }),
    "unsupported payload field serverName",
  );
});
