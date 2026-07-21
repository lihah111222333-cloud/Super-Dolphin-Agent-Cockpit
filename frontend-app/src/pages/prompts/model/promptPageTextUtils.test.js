import { describe, expect, it } from "vitest";
import {
  parseJsonObjectForEditor,
  serializeJsonForEditor,
} from "./promptPageTextUtils.js";

describe("prompt page JSON editor utilities", () => {
  it("serializes JSON values without swallowing serialization failures", () => {
    expect(serializeJsonForEditor({ language: "zh" })).toBe(
      '{\n  "language": "zh"\n}',
    );

    const cyclic = {};
    cyclic.self = cyclic;
    expect(() => serializeJsonForEditor(cyclic)).toThrow();
    expect(() => serializeJsonForEditor(Symbol("invalid"))).toThrow(
      /必须是可序列化的 JSON 值/,
    );
  });

  it("returns explicit editor validation errors for invalid JSON objects", () => {
    expect(
      parseJsonObjectForEditor('{"tags":["review"]}', "自动匹配条件"),
    ).toEqual({ value: { tags: ["review"] }, error: "" });
    expect(parseJsonObjectForEditor("[]", "自动匹配条件")).toEqual({
      value: undefined,
      error: "自动匹配条件必须是 JSON 对象",
    });
    expect(parseJsonObjectForEditor("{bad", "自动匹配条件").error).toMatch(
      /自动匹配条件不是合法 JSON/,
    );
  });
});
