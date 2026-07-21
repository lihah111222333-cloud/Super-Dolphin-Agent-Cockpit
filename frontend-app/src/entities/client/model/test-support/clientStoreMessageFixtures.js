export function createThreadMessageFixtures() {
  const ids = new Map();
  let nextID = 1;
  const allowed = new Set([
    "id",
    "agentId",
    "role",
    "eventType",
    "method",
    "content",
    "createdAt",
    "metadata",
  ]);
  const requiredValue = (input, key, fallback) =>
    input[key] === undefined ? fallback : input[key];
  const assertStringFields = (result) => {
    for (const key of [
      "agentId",
      "role",
      "eventType",
      "method",
      "content",
      "createdAt",
    ]) {
      if (typeof result[key] !== "string")
        throw new TypeError(`thread/messages fixture ${key} must be a string`);
    }
  };
  const message = (input = {}) => {
    for (const key of Object.keys(input)) {
      if (!allowed.has(key))
        throw new TypeError(`unsupported thread/messages fixture key: ${key}`);
    }
    const label =
      input.id === undefined ? `message-${nextID}` : String(input.id);
    const id =
      typeof input.id === "number" ? input.id : (ids.get(label) ?? nextID++);
    if (typeof input.id !== "number") ids.set(label, id);
    if (!Number.isSafeInteger(id) || id < 0)
      throw new TypeError(
        "thread/messages fixture id must be a non-negative safe integer",
      );
    const result = {
      id,
      agentId: requiredValue(input, "agentId", ""),
      role: requiredValue(input, "role", ""),
      eventType: requiredValue(input, "eventType", ""),
      method: requiredValue(input, "method", ""),
      content: requiredValue(input, "content", ""),
      createdAt: requiredValue(input, "createdAt", "2026-01-01T00:00:00.000Z"),
    };
    assertStringFields(result);
    if (input.metadata !== undefined) {
      if (
        !input.metadata ||
        typeof input.metadata !== "object" ||
        Array.isArray(input.metadata)
      )
        throw new TypeError(
          "thread/messages fixture metadata must be an object",
        );
      result.metadata = input.metadata;
    }
    return result;
  };
  const page = (input = {}) => {
    const messages = requiredValue(input, "messages", []);
    const total = requiredValue(input, "total", messages.length);
    const hasMore = requiredValue(input, "hasMore", false);
    const nextBefore = requiredValue(input, "nextBefore", "");
    if (!Array.isArray(messages))
      throw new TypeError("thread/messages fixture messages must be an array");
    if (!Number.isSafeInteger(total) || total < 0)
      throw new TypeError(
        "thread/messages fixture total must be a non-negative safe integer",
      );
    if (typeof hasMore !== "boolean")
      throw new TypeError("thread/messages fixture hasMore must be a boolean");
    if (typeof nextBefore !== "string")
      throw new TypeError(
        "thread/messages fixture nextBefore must be a string",
      );
    return { messages: messages.map(message), total, hasMore, nextBefore };
  };
  return { message, page };
}
