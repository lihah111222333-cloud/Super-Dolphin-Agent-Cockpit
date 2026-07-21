import { expect, it } from "vitest";
import { collectFrontendPayloadKeysFromSource } from "./rpc-contract-audit.mjs";

describe("rpc contract audit", { timeout: 30000 }, () => {
  it("ignores payload calls inside nested functions and instance fields", () => {
    const source = `
      const api = {
        start: (params) => callBackend(RPC_METHODS.THREAD_START, threadStartPayload(params)),
      }
      function threadStartPayload(params) {
        const unused = { ...params }
        const nested = () => takePayloadField(unused, 'provider')
        class Decoy {
          read = takePayloadField(unused, 'provider')
        }
        void nested
        void Decoy
        return takePayloadFields(unused, ['cwd'])
      }
    `;

    expect(collectFrontendPayloadKeysFromSource(source).get("THREAD_START")).toEqual(["cwd"]);
  });

  it("fails fast when a required builder has no top-level declaration", () => {
    const source = [
      "function wrapper() {",
      "  function threadStartPayload(params) {",
      "    const unused = { ...params }",
      "    return takePayloadFields(unused, ['cwd', 'provider'])",
      "  }",
      "}",
      "const api = { start: (params) => callBackend(RPC_METHODS.THREAD_START, threadStartPayload(params)) }",
    ].join("\n");

    expect(() => collectFrontendPayloadKeysFromSource(source)).toThrow(
      "threadStartPayload must have exactly one top-level FunctionDeclaration; found 0",
    );
  });

  it("fails fast when a required builder has multiple top-level declarations", () => {
    const source = [
      "function threadStartPayload(params) {",
      "  const unused = { ...params }",
      "  return takePayloadFields(unused, ['cwd'])",
      "}",
      "function threadStartPayload(params) {",
      "  const unused = { ...params }",
      "  return takePayloadFields(unused, ['provider'])",
      "}",
      "const api = { start: (params) => callBackend(RPC_METHODS.THREAD_START, threadStartPayload(params)) }",
    ].join("\n");

    expect(() => collectFrontendPayloadKeysFromSource(source)).toThrow(
      "Identifier 'threadStartPayload' has already been declared",
    );
  });

  it("fails closed when one method has competing discovered builders", () => {
    const source = [
      "const api = {",
      "  one: (params) => callBackend(RPC_METHODS.THREAD_START, firstPayload(params)),",
      "  two: (params) => callBackend(RPC_METHODS.THREAD_START, secondPayload(params)),",
      "}",
      "function firstPayload(params) { const unused = { ...params }; return takePayloadFields(unused, ['cwd']) }",
      "function secondPayload(params) { const unused = { ...params }; return takePayloadFields(unused, ['provider']) }",
    ].join("\n");

    expect(() => collectFrontendPayloadKeysFromSource(source)).toThrow(
      "RPC_METHODS.THREAD_START has ambiguous payload builders: firstPayload, secondPayload",
    );
  });

  it("fails closed when a required method also has a non-builder payload call", () => {
    const source = `
      const api = {
        valid: (params) => callBackend(RPC_METHODS.THREAD_START, threadStartPayload(params)),
        invalid: () => callBackend(RPC_METHODS.THREAD_START, { cwd: '/repo' }),
      }
      function threadStartPayload(params) {
        const unused = { ...params }
        return takePayloadFields(unused, ['cwd'])
      }
    `;

    expect(() =>
      collectFrontendPayloadKeysFromSource(
        source,
        new Map([["THREAD_START", "thread/start"]]),
        new Set(["thread/start"]),
      ),
    ).toThrow(
      "RPC_METHODS.THREAD_START must pass an IdentifierBuilder(...) payload to callBackend",
    );
  });

  it("fails closed when a required method has no discovered builder", () => {
    const source = `
      const api = {
        start: (params) => callBackend(RPC_METHODS.THREAD_START, threadStartPayload(params)),
      }
      function threadStartPayload(params) {
        const unused = { ...params }
        return takePayloadFields(unused, ['cwd'])
      }
    `;

    expect(() =>
      collectFrontendPayloadKeysFromSource(
        source,
        new Map([
          ["THREAD_START", "thread/start"],
          ["TURN_START", "turn/start"],
        ]),
        new Set(["thread/start", "turn/start"]),
      ),
    ).toThrow("required RPC payload builders were not discovered: turn/start");
  });
});
