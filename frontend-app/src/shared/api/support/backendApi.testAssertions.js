import { expect } from "vitest";

export function expectInvalidInputDoesNotCall(callAPI, action, message) {
  const callCount = callAPI.mock.calls.length;
  expect(action).toThrow(message);
  expect(callAPI).toHaveBeenCalledTimes(callCount);
}
