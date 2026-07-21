import { afterEach, describe, expect, it, vi } from "vitest";
import { beginTextClipboardWrite, copyTextToClipboard } from "./wailsBridge.js";

describe("wails bridge clipboard helpers", () => {
  afterEach(() => {
    vi.unstubAllGlobals();
    delete window.__WAILS_SHIM_DEBUG__;
    delete document.execCommand;
  });

  it("starts a clipboard write synchronously and commits text after async data is ready", async () => {
    let copiedText = "";
    class TestClipboardItem {
      constructor(items) {
        this.items = items;
      }

      getType(type) {
        return this.items[type];
      }
    }
    class TestBlob {
      constructor(parts, options = {}) {
        this.parts = parts;
        this.type = options.type || "";
      }
    }
    const write = vi.fn(async ([item]) => {
      const blob = await item.getType("text/plain");
      copiedText = blob.parts.join("");
    });
    Object.defineProperty(navigator, "clipboard", {
      configurable: true,
      value: { write },
    });
    vi.stubGlobal("ClipboardItem", TestClipboardItem);
    vi.stubGlobal("Blob", TestBlob);

    const prepared = beginTextClipboardWrite();

    expect(write).toHaveBeenCalledTimes(1);
    await expect(prepared.commit("thread info")).resolves.toBe(true);
    expect(copiedText).toBe("thread info");
  });

  it("surfaces prepared clipboard write failures when committing text", async () => {
    class TestClipboardItem {
      constructor(items) {
        this.items = items;
      }

      getType(type) {
        return this.items[type];
      }
    }
    class TestBlob {
      constructor(parts, options = {}) {
        this.parts = parts;
        this.type = options.type || "";
      }
    }
    const write = vi.fn(async ([item]) => {
      await item.getType("text/plain");
      throw new Error("clipboard write rejected");
    });
    Object.defineProperty(navigator, "clipboard", {
      configurable: true,
      value: { write },
    });
    vi.stubGlobal("ClipboardItem", TestClipboardItem);
    vi.stubGlobal("Blob", TestBlob);

    const prepared = beginTextClipboardWrite();

    expect(write).toHaveBeenCalledTimes(1);
    await expect(prepared.commit("thread info")).rejects.toThrow(
      "clipboard write rejected",
    );
  });

  it("falls back to a focused textarea copy when async clipboard is unavailable", async () => {
    window.__WAILS_SHIM_DEBUG__ = true;
    Object.defineProperty(navigator, "clipboard", {
      configurable: true,
      value: undefined,
    });
    document.execCommand = vi.fn(() => true);

    await expect(copyTextToClipboard("fallback text")).resolves.toBe(true);

    expect(document.execCommand).toHaveBeenCalledWith("copy");
    expect(document.querySelector("textarea")).toBeNull();
  });

  it("throws concrete clipboard failures instead of returning false", async () => {
    window.__WAILS_SHIM_DEBUG__ = true;
    Object.defineProperty(navigator, "clipboard", {
      configurable: true,
      value: {
        writeText: vi
          .fn()
          .mockRejectedValue(new Error("The request is not allowed")),
      },
    });
    document.execCommand = vi.fn(() => false);

    await expect(copyTextToClipboard("failfast text")).rejects.toThrow(
      "clipboard copy failed: browser clipboard.writeText failed: The request is not allowed; document.execCommand fallback failed: document.execCommand('copy') returned false",
    );
  });
});
