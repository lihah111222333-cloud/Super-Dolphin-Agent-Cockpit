import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { existsSync, readFileSync } from "node:fs";
import { join } from "node:path";
import { cwd } from "node:process";
import {
  SHARED_FILE_PREVIEW_FIELD_CONSUMERS,
  SHARED_FILE_PREVIEW_MAX_BYTES,
  nativeSharedFilePreviewResponse,
} from "./wails/wailsBridgeNativeFiles.js";
import {
  resetWailsRuntimeMocks,
  runtimeModule,
  sharedFilePreviewProducerFields,
} from "./wailsBridgeTestSupport.js";

describe("wails bridge shared file helpers", () => {
  beforeEach(resetWailsRuntimeMocks);

  afterEach(() => {
    vi.unstubAllGlobals();
  });

  it("opens shared files through the native sharedFile open RPC with a trimmed path", async () => {
    const byID = vi.fn().mockResolvedValue({ opened: true });
    vi.doMock(runtimeModule, () => ({
      Call: { ByID: byID },
      Events: { On: vi.fn() },
    }));
    const { openSharedFile } = await import("./wailsBridge.js");

    await expect(
      openSharedFile({ path: " dag/video/final.mp4 " }),
    ).resolves.toEqual({ opened: true });

    expect(byID).toHaveBeenCalledWith(
      expect.any(Number),
      "ui/sharedFile/open",
      expect.objectContaining({
        path: "dag/video/final.mp4",
      }),
    );
    await expect(openSharedFile({ path: " " })).rejects.toThrow(
      "openSharedFile path is required",
    );
  });

  it("requests tokenized shared file previews through the native sharedFile RPC", async () => {
    const byID = vi.fn().mockResolvedValue({
      url: "http://127.0.0.1:4511/shared-file-preview?id=sf_123",
      path: "dag/video/final.mp4",
      contentType: "video/mp4",
      sizeBytes: 24,
    });
    vi.doMock(runtimeModule, () => ({
      Call: { ByID: byID },
      Events: { On: vi.fn() },
    }));
    const { previewSharedFile } = await import("./wailsBridge.js");

    await expect(
      previewSharedFile({ path: " dag/video/final.mp4 " }),
    ).resolves.toEqual({
      url: "http://127.0.0.1:4511/shared-file-preview?id=sf_123",
      path: "dag/video/final.mp4",
      contentType: "video/mp4",
      sizeBytes: 24,
    });

    byID.mockResolvedValueOnce({
      url: "http://127.0.0.1:4511/shared-file-preview?id=sf_other",
      path: "dag/video/other.mp4",
      contentType: "video/mp4",
      sizeBytes: 24,
    });
    await expect(
      previewSharedFile({ path: "dag/video/final.mp4" }),
    ).rejects.toThrow("response path must match requested path");

    expect(byID).toHaveBeenCalledWith(
      expect.any(Number),
      "ui/sharedFile/open",
      expect.objectContaining({
        path: "dag/video/final.mp4",
        preview: true,
      }),
    );
    await expect(previewSharedFile({ path: "" })).rejects.toThrow(
      "previewSharedFile path is required",
    );
  });

  it("keeps the native preview consumer registry aligned with the Go producer", () => {
    expect(Object.keys(SHARED_FILE_PREVIEW_FIELD_CONSUMERS).sort()).toEqual(
      sharedFilePreviewProducerFields(),
    );
    expect(SHARED_FILE_PREVIEW_FIELD_CONSUMERS).toMatchObject({
      contentType: { direction: "bridge-to-workflow-ui" },
      path: { direction: "bridge-terminal-validation" },
      sizeBytes: { direction: "bridge-terminal-validation" },
      url: { direction: "bridge-to-workflow-ui" },
    });
    for (const contract of Object.values(SHARED_FILE_PREVIEW_FIELD_CONSUMERS)) {
      expect(contract.reason).toEqual(expect.any(String));
      expect(contract.reason.trim()).not.toBe("");
      expect(contract.owner).toEqual(expect.any(String));
      expect(existsSync(join(cwd(), "..", contract.owner))).toBe(true);
    }
    const producerSource = readFileSync(
      join(cwd(), "..", "internal", "ui", "wails", "sharedfile_open.go"),
      "utf8",
    );
    const maxBytes = producerSource.match(
      /const sharedFilePreviewMaxBytes int64 = (\d+) \* 1024 \* 1024/,
    );
    if (!maxBytes)
      throw new Error(
        "sharedFilePreviewMaxBytes producer constant is required",
      );
    expect(SHARED_FILE_PREVIEW_MAX_BYTES).toBe(
      Number(maxBytes[1]) * 1024 * 1024,
    );
  });

  it("requires every exact native preview field one at a time", () => {
    const valid = {
      url: "http://127.0.0.1:4511/shared-file-preview?id=sf_123",
      path: "dag/video/final.mp4",
      contentType: "video/mp4",
      sizeBytes: 24,
    };
    for (const field of Object.keys(valid)) {
      const candidate = { ...valid };
      delete candidate[field];
      expect(
        () => nativeSharedFilePreviewResponse("ui/sharedFile/open", candidate),
        field,
      ).toThrow(field);
    }
    expect(() =>
      nativeSharedFilePreviewResponse("ui/sharedFile/open", {
        ...valid,
        stale: true,
      }),
    ).toThrow("unknown field stale");
    expect(
      nativeSharedFilePreviewResponse("ui/sharedFile/open", valid),
    ).toEqual(valid);
    expect(() =>
      nativeSharedFilePreviewResponse("ui/sharedFile/open", {
        ...valid,
        sizeBytes: SHARED_FILE_PREVIEW_MAX_BYTES + 1,
      }),
    ).toThrow("sizeBytes");
  });

  it.each([
    "https://127.0.0.1:4511/shared-file-preview?id=sf_123",
    "http://example.com:4511/shared-file-preview?id=sf_123",
    "http://127.0.0.1:4511/not-preview?id=sf_123",
    "http://127.0.0.1:4511/shared-file-preview?id=",
    "http://127.0.0.1:4511/shared-file-preview?id=one&id=two",
    "http://user@127.0.0.1:4511/shared-file-preview?id=sf_123",
    "http://127.0.0.1:0/shared-file-preview?id=sf_123",
    "http://2130706433:4511/shared-file-preview?id=sf_123",
    "http://127.0.0.1:4511/a/../shared-file-preview?id=sf_123",
    "http://127.0.0.1:4511/shared-file-preview?id=sf_123&&",
  ])("rejects unsafe native preview URL %s", (url) => {
    expect(() =>
      nativeSharedFilePreviewResponse("ui/sharedFile/open", {
        url,
        path: "dag/video/final.mp4",
        contentType: "video/mp4",
        sizeBytes: 24,
      }),
    ).toThrow("response url");
  });

  it("accepts IPv4 and IPv6 loopback preview URLs with explicit runtime ports", () => {
    for (const url of [
      "http://127.0.0.1:4511/shared-file-preview?id=sf_ipv4",
      "http://[::1]:4512/shared-file-preview?id=sf_ipv6",
    ]) {
      expect(
        nativeSharedFilePreviewResponse("ui/sharedFile/open", {
          url,
          path: "dag/video/final.mp4",
          contentType: "video/mp4",
          sizeBytes: 24,
        }).url,
      ).toBe(url);
    }
  });

  it("rejects malformed native shared file responses", async () => {
    const byID = vi.fn((_methodID, method, payload) => {
      if (method !== "ui/sharedFile/open") {
        throw new Error(`unexpected method ${method}`);
      }
      return Promise.resolve(payload.preview ? { url: "" } : {});
    });
    vi.doMock(runtimeModule, () => ({
      Call: { ByID: byID },
      Events: { On: vi.fn() },
    }));
    const { openSharedFile, previewSharedFile } =
      await import("./wailsBridge.js");

    await expect(
      openSharedFile({ path: "dag/video/final.mp4" }),
    ).rejects.toThrow("ui/sharedFile/open response opened must be true");
    await expect(
      previewSharedFile({ path: "dag/video/final.mp4" }),
    ).rejects.toThrow(
      "ui/sharedFile/open response url must be a canonical loopback preview URL",
    );
  });
});
