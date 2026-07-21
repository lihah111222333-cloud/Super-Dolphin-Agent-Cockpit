import { describe, expect, it } from "vitest";
import { normalizeRuntimeEventEnvelope } from "./wailsBridge.js";
import { readFileSync } from "node:fs";
import { join } from "node:path";
import { cwd } from "node:process";

describe("wails bridge runtime loading", () => {
  it("keeps the public Wails runtime out of Vite import analysis", () => {
    const facadeSource = readFileSync(
      join(cwd(), "src/shared/api/wailsBridge.js"),
      "utf8",
    );
    const loaderSource = readFileSync(
      join(cwd(), "src/shared/api/wails/wailsRuntimeLoader.js"),
      "utf8",
    );

    expect(facadeSource).toContain("export { loadWailsRuntime } from './wails/wailsRuntimeLoader.js';");
    expect(loaderSource).toContain("import(/* @vite-ignore */ '/wails/runtime.js')");
    expect(loaderSource).not.toContain('modulePath');
    expect(loaderSource).not.toContain("Function('modulePath'");
  });
});

describe("wails bridge runtime event JSON parsing", () => {
  it("preserves large integer-looking text inside JSON strings", () => {
    expect(
      normalizeRuntimeEventEnvelope({
        name: "runtime",
        data: '{"payload":{"message":"keep : 1234567890123456 inside string"}}',
      }).payload.message,
    ).toBe("keep : 1234567890123456 inside string");
  });

  it("converts unsafe runtime event object integers to strings", () => {
    expect(
      normalizeRuntimeEventEnvelope({
        name: "runtime",
        data: '{"payload":{"requestId":9007199254740993}}',
      }).payload.requestId,
    ).toBe("9007199254740993");
  });

  it("converts unsafe runtime event array integers to strings", () => {
    expect(
      normalizeRuntimeEventEnvelope({
        name: "runtime",
        data: '{"payload":{"ids":[9007199254740993]}}',
      }).payload.ids,
    ).toEqual(["9007199254740993"]);
  });
});
