import { beforeEach, describe, expect, it, vi } from "vitest";
import { waitFor } from "@testing-library/react";
import {
  captureBridgeLogs,
  resetWailsRuntimeMocks,
  runtimeModule,
} from "./wailsBridgeTestSupport.js";

describe("wails bridge file picker helpers", () => {
  beforeEach(resetWailsRuntimeMocks);

  it("passes file filters through the ui/selectFiles RPC path", async () => {
    const byID = vi.fn((methodID, method, payload) => {
      if (methodID !== 1391035622 || method !== "ui/selectFiles") {
        throw new Error("filtered picker must use parameterized RPC path");
      }
      if (payload.filters?.[0]?.pattern !== "*.pdf;*.txt;*.text") {
        throw new Error("missing datasource filter pattern");
      }
      return Promise.resolve({ paths: ["C:\\data\\manual.pdf"] });
    });
    vi.doMock(runtimeModule, () => ({
      Call: { ByID: byID },
      Events: { On: vi.fn() },
    }));
    const { selectFiles } = await import("./wailsBridge.js");

    await expect(
      selectFiles({
        filters: [
          { displayName: "PDF/TXT/TEXT", pattern: "*.pdf;*.txt;*.text" },
        ],
      }),
    ).resolves.toEqual(["C:\\data\\manual.pdf"]);
    expect(byID).toHaveBeenCalledWith(
      1391035622,
      "ui/selectFiles",
      expect.objectContaining({
        filters: [
          { displayName: "PDF/TXT/TEXT", pattern: "*.pdf;*.txt;*.text" },
        ],
      }),
    );
  });

  it("uses a dedicated datasource import picker response with a token", async () => {
    const byID = vi.fn((methodID, method, payload) => {
      if (
        methodID !== 1391035622 ||
        method !== "ui/selectDatasourceImportFile"
      ) {
        throw new Error(
          "datasource import picker must use its dedicated RPC path",
        );
      }
      if (payload.filters?.[0]?.pattern !== "*.pdf;*.txt;*.text") {
        throw new Error("missing datasource filter pattern");
      }
      return Promise.resolve({
        sourcePath: "C:\\\\data\\\\manual.pdf",
        pickerToken: "picker-token",
      });
    });
    vi.doMock(runtimeModule, () => ({
      Call: { ByID: byID },
      Events: { On: vi.fn() },
    }));
    const { selectDatasourceImportFile } = await import("./wailsBridge.js");

    await expect(
      selectDatasourceImportFile({
        filters: [
          { displayName: "PDF/TXT/TEXT", pattern: "*.pdf;*.txt;*.text" },
        ],
      }),
    ).resolves.toEqual({
      sourcePath: "C:\\\\data\\\\manual.pdf",
      pickerToken: "picker-token",
    });
    expect(byID).toHaveBeenCalledWith(
      1391035622,
      "ui/selectDatasourceImportFile",
      expect.objectContaining({
        filters: [
          { displayName: "PDF/TXT/TEXT", pattern: "*.pdf;*.txt;*.text" },
        ],
      }),
    );
  });

  it("parses native file helper responses only from explicit response shapes", async () => {
    const byID = vi.fn((_methodID, method) => {
      if (method === "ui/selectProjectDirs")
        return Promise.resolve({ paths: ["/repo/a"] });
      if (method === "ui/saveTextFile")
        return Promise.resolve({ path: "/tmp/out.txt" });
      if (method === "ui/readDroppedTextFiles") {
        return Promise.resolve({
          files: [
            {
              path: "/tmp/a.txt",
              name: "a.txt",
              text: "hello",
              sizeBytes: 5,
            },
          ],
        });
      }
      throw new Error(`unexpected method ${method}`);
    });
    vi.doMock(runtimeModule, () => ({
      Call: { ByID: byID },
      Events: { On: vi.fn() },
    }));
    const { selectProjectDirs, saveTextFile, readDroppedTextFiles } =
      await import("./wailsBridge.js");

    await expect(selectProjectDirs()).resolves.toEqual(["/repo/a"]);
    await expect(
      saveTextFile({ defaultFilename: "out.txt", content: "hello" }),
    ).resolves.toBe("/tmp/out.txt");
    await expect(
      readDroppedTextFiles(["/tmp/a.txt"], "drop-1"),
    ).resolves.toEqual([
      {
        path: "/tmp/a.txt",
        name: "a.txt",
        text: "hello",
        sizeBytes: 5,
      },
    ]);
  });

  it("rejects malformed native file helper responses instead of defaulting to empty values", async () => {
    const byID = vi.fn((_methodID, method) => {
      if (method === "ui/selectProjectDirs") return Promise.resolve({});
      if (method === "ui/saveTextFile") return Promise.resolve({});
      if (method === "ui/readDroppedTextFiles")
        return Promise.resolve({
          files: [{ path: "/tmp/a.txt", name: "a.txt", text: "hello" }],
        });
      throw new Error(`unexpected method ${method}`);
    });
    vi.doMock(runtimeModule, () => ({
      Call: { ByID: byID },
      Events: { On: vi.fn() },
    }));
    const { selectProjectDirs, saveTextFile, readDroppedTextFiles } =
      await import("./wailsBridge.js");

    await expect(selectProjectDirs()).rejects.toThrow(
      "ui/selectProjectDirs response paths must be an array",
    );
    await expect(
      saveTextFile({ defaultFilename: "out.txt", content: "hello" }),
    ).rejects.toThrow("ui/saveTextFile response path must be a string");
    await expect(
      readDroppedTextFiles(["/tmp/a.txt"], "drop-1"),
    ).rejects.toThrow(
      "ui/readDroppedTextFiles response file sizeBytes must be a non-negative number",
    );
  });

  it("rejects missing clipboard image paths instead of defaulting to an empty payload", async () => {
    const byID = vi.fn().mockResolvedValue(undefined);
    vi.doMock(runtimeModule, () => ({
      Call: { ByID: byID },
      Events: { On: vi.fn() },
    }));
    const { saveClipboardImage } = await import("./wailsBridge.js");

    await expect(saveClipboardImage("base64-image")).rejects.toThrow(
      "ui/saveClipboardImage response path must be a string",
    );
  });

  it("rejects malformed selectFiles native responses without falling back to the RPC path", async () => {
    const byID = vi.fn().mockResolvedValue({});
    vi.doMock(runtimeModule, () => ({
      Call: { ByID: byID },
      Events: { On: vi.fn() },
    }));
    const { selectFiles } = await import("./wailsBridge.js");

    await expect(selectFiles()).rejects.toThrow(
      "ui/selectFiles response paths must be an array",
    );
    expect(byID).toHaveBeenCalledTimes(1);
  });

  it("rejects malformed datasource import picker responses without defaulting a token", async () => {
    const byID = vi
      .fn()
      .mockResolvedValue({ sourcePath: "C:\\\\data\\\\manual.pdf" });
    vi.doMock(runtimeModule, () => ({
      Call: { ByID: byID },
      Events: { On: vi.fn() },
    }));
    const { selectDatasourceImportFile } = await import("./wailsBridge.js");

    await expect(selectDatasourceImportFile()).rejects.toThrow(
      "ui/selectDatasourceImportFile response pickerToken must be a non-empty string",
    );
  });
});

describe("wails bridge non-RPC binding logs", () => {
  beforeEach(resetWailsRuntimeMocks);

  it("keeps bridge.call.failed for non-RPC bridge binding failures", async () => {
    const error = new Error("native binding unavailable");
    vi.doMock(runtimeModule, () => ({
      Call: { ByID: vi.fn().mockRejectedValue(error) },
      Events: { On: vi.fn() },
    }));
    const { getBuildInfo, registerBridgeLogStore } =
      await import("./wailsBridge.js");
    const logs = captureBridgeLogs(registerBridgeLogStore);

    await expect(getBuildInfo()).rejects.toThrow("native binding unavailable");

    const errorEvents = logs
      .filter((entry) => entry.level === "error")
      .map((entry) => entry.event);
    expect(errorEvents).toEqual(["bridge.call.failed"]);
  });
});

describe("wails bridge event callbacks", () => {
  beforeEach(resetWailsRuntimeMocks);

  it("returns a ready promise and retries when the first runtime event binding is unavailable", async () => {
    let importCount = 0;
    const on = vi.fn(() => () => {});
    vi.doMock(runtimeModule, () => {
      importCount += 1;
      if (importCount === 1) {
        throw new Error("runtime not loaded yet");
      }
      return {
        Call: { ByID: vi.fn() },
        Events: { On: on },
      };
    });
    const { onBridgeEvent, registerBridgeLogStore } =
      await import("./wailsBridge.js");
    const logs = captureBridgeLogs(registerBridgeLogStore);

    const first = onBridgeEvent(vi.fn());

    expect(typeof first).toBe("object");
    expect(first).toEqual({
      ready: expect.any(Promise),
      unsubscribe: expect.any(Function),
    });
    await expect(first.ready).resolves.toBe(false);
    expect(
      logs.find((entry) => entry.event === "bridge.subscribe.unavailable"),
    ).toEqual(expect.objectContaining({ level: "warn" }));

    const second = onBridgeEvent(vi.fn());

    expect(typeof second).toBe("object");
    expect(second).toEqual({
      ready: expect.any(Promise),
      unsubscribe: expect.any(Function),
    });
    await expect(second.ready).resolves.toBe(true);
    expect(on).toHaveBeenCalledWith("bridge-event", expect.any(Function));
    second.unsubscribe();
  });

  it("rethrows bridge callback errors when escalation is requested", async () => {
    let eventCallback = null;
    const on = vi.fn((_eventName, callback) => {
      eventCallback = callback;
      return () => {};
    });
    vi.doMock(runtimeModule, () => ({
      Call: { ByID: vi.fn() },
      Events: { On: on },
    }));
    const { onBridgeEvent, registerBridgeLogStore } =
      await import("./wailsBridge.js");
    const logs = captureBridgeLogs(registerBridgeLogStore);

    onBridgeEvent(
      () => {
        throw new Error("dag status event run identity is required");
      },
      { escalateCallbackError: true },
    );

    await waitFor(() =>
      expect(on).toHaveBeenCalledWith("bridge-event", expect.any(Function)),
    );
    expect(() =>
      eventCallback({
        name: "bridge-event",
        data: {
          method: "task/node/statuschanged",
          payload: {
            dag_key: "flow-a",
            node_key: "step",
            new_status: "running",
          },
        },
      }),
    ).toThrow("dag status event run identity is required");
    expect(
      logs.find((entry) => entry.event === "bridge.callback.failed"),
    ).toEqual(
      expect.objectContaining({
        level: "error",
        fields: expect.objectContaining({
          error: expect.objectContaining({
            message: "dag status event run identity is required",
          }),
        }),
      }),
    );
  });

  it("emits an explicit parse failure event for malformed bridge event JSON", async () => {
    let eventCallback = null;
    const on = vi.fn((_eventName, callback) => {
      eventCallback = callback;
      return () => {};
    });
    vi.doMock(runtimeModule, () => ({
      Call: { ByID: vi.fn() },
      Events: { On: on },
    }));
    const { onBridgeEvent, registerBridgeLogStore } =
      await import("./wailsBridge.js");
    const logs = captureBridgeLogs(registerBridgeLogStore);
    const callback = vi.fn();

    onBridgeEvent(callback);

    await waitFor(() =>
      expect(on).toHaveBeenCalledWith("bridge-event", expect.any(Function)),
    );
    eventCallback({
      name: "bridge-event",
      data: '{"method":',
    });

    expect(callback).toHaveBeenCalledWith(
      expect.objectContaining({
        method: "bridge.event.parse_failed",
        payload: expect.objectContaining({
          eventName: "bridge-event",
          rawLen: 10,
        }),
      }),
    );
    expect(callback.mock.calls[0][0].payload).not.toHaveProperty("rawPreview");
    expect(
      logs.find((entry) => entry.event === "bridge.event.parse_failed"),
    ).toEqual(expect.objectContaining({ level: "error" }));
  });
});
