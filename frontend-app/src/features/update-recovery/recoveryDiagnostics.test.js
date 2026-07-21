import { afterEach, describe, expect, it, vi } from "vitest";

import { exportRecoveryDiagnostics } from "./recoveryDiagnostics.js";

describe("exportRecoveryDiagnostics", () => {
  afterEach(() => {
    vi.restoreAllMocks();
    vi.unstubAllGlobals();
  });

  it("always revokes the diagnostics object URL when download click throws", () => {
    const createObjectURL = vi.fn().mockReturnValue("blob:recovery");
    const revokeObjectURL = vi.fn();
    vi.stubGlobal("URL", { ...URL, createObjectURL, revokeObjectURL });
    vi.spyOn(HTMLAnchorElement.prototype, "click").mockImplementation(() => {
      throw new Error("click failed");
    });

    expect(() =>
      exportRecoveryDiagnostics({ code: "UPDATE_SIGNATURE_INVALID" }),
    ).toThrow("click failed");
    expect(createObjectURL).toHaveBeenCalledTimes(1);
    expect(revokeObjectURL).toHaveBeenCalledWith("blob:recovery");
  });
});
