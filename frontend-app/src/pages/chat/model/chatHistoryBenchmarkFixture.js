function requirePositiveInteger(value, field) {
  if (!Number.isSafeInteger(value) || value <= 0) {
    throw new TypeError(`${field} must be a positive integer`);
  }
}

function requireFixtureInput({ turns, toolsPerTurn, archived, seed }) {
  requirePositiveInteger(turns, 'turns');
  requirePositiveInteger(toolsPerTurn, 'toolsPerTurn');
  if (typeof archived !== 'boolean') throw new TypeError('archived must be a boolean');
  if (!Number.isSafeInteger(seed) || seed < 0) {
    throw new TypeError('seed must be a non-negative integer');
  }
}

function seededToken(seed, turnIndex, messageIndex) {
  let value = (seed ^ Math.imul(turnIndex + 1, 0x45d9f3b) ^ Math.imul(messageIndex + 1, 0x119de1f3)) >>> 0;
  value = Math.imul(value ^ (value >>> 16), 0x45d9f3b) >>> 0;
  value = Math.imul(value ^ (value >>> 16), 0x45d9f3b) >>> 0;
  return ((value ^ (value >>> 16)) >>> 0).toString(16).padStart(8, '0');
}

function createFixtureMessage(fixture, role, messageIndex, toolIndex) {
  const { archived, seed, turnIndex } = fixture;
  const token = seededToken(seed, turnIndex, messageIndex);
  const message = {
    id: `fixture-${seed}-${turnIndex}-${messageIndex}-${token}`,
    role,
    content: `synthetic-message-body-${token}`,
    archived,
    turnIndex,
  };
  if (role === 'tool') message.toolName = `fixture_tool_${toolIndex}`;
  return Object.freeze(message);
}

function buildChatHistoryFixture(input) {
  requireFixtureInput(input);
  const { archived, seed, toolsPerTurn, turns } = input;
  const history = [];
  for (let turnIndex = 0; turnIndex < turns; turnIndex += 1) {
    const fixture = { archived, seed, turnIndex };
    history.push(createFixtureMessage(fixture, 'user', 0));
    for (let toolIndex = 0; toolIndex < toolsPerTurn; toolIndex += 1) {
      history.push(createFixtureMessage(fixture, 'tool', toolIndex + 1, toolIndex));
    }
    history.push(createFixtureMessage(fixture, 'assistant', toolsPerTurn + 1));
  }
  return history;
}

export { buildChatHistoryFixture };
