const DEFAULT_FRAME_LIMITS = Object.freeze({
  maxFrameBytes: 1024 * 1024,
  maxHeaderBytes: 8192,
  maxLineBytes: 1024 * 1024,
});

export class MCPFrameError extends Error {
  constructor(message, { mode = 'ndjson', consumed = 0 } = {}) {
    super(message);
    this.name = 'MCPFrameError';
    this.mode = mode;
    this.consumed = consumed;
  }
}

export function encodeMCPFrame(message, mode = 'ndjson') {
  const payload = JSON.stringify(message);
  if (mode === 'ndjson') return Buffer.from(`${payload}\n`, 'utf8');
  if (mode === 'content-length') {
    const length = Buffer.byteLength(payload, 'utf8');
    return Buffer.from(`Content-Length: ${length}\r\n\r\n${payload}`, 'utf8');
  }
  throw new Error(`unknown MCP frame mode: ${mode}`);
}

export function parseMCPFrame(buffer, limits = DEFAULT_FRAME_LIMITS) {
  const input = Buffer.isBuffer(buffer) ? buffer : Buffer.from(buffer);
  if (input.length === 0) return null;

  if (startsWithContentLength(input)) {
    return parseContentLengthFrame(input, frameLimits(limits));
  }
  return parseNDJSONFrame(input, frameLimits(limits));
}

export function createMCPFrameReader({ onMessage, onError, limits = DEFAULT_FRAME_LIMITS }) {
  if (typeof onMessage !== 'function') throw new Error('onMessage is required');
  if (typeof onError !== 'function') throw new Error('onError is required');

  let buffer = Buffer.alloc(0);
  let processing = Promise.resolve();

  async function drain() {
    while (buffer.length > 0) {
      let frame;
      try {
        frame = parseMCPFrame(buffer, limits);
      }
      catch (error) {
        if (error instanceof MCPFrameError) {
          const consumed = error.consumed > 0 ? error.consumed : buffer.length;
          buffer = buffer.subarray(Math.min(consumed, buffer.length));
          await onError(error, error.mode);
          continue;
        }
        throw error;
      }

      if (frame == null) return;
      buffer = buffer.subarray(frame.consumed);
      await onMessage(frame.message, frame.mode);
    }
  }

  return {
    push(chunk) {
      processing = processing.then(async () => {
        buffer = Buffer.concat([buffer, Buffer.isBuffer(chunk) ? chunk : Buffer.from(chunk)]);
        await drain();
      });
      return processing;
    },
    async end() {
      await processing;
      if (buffer.length === 0) return;
      const mode = startsWithContentLength(buffer) ? 'content-length' : 'ndjson';
      const error = new MCPFrameError('incomplete MCP frame at EOF', { mode, consumed: buffer.length });
      buffer = Buffer.alloc(0);
      await onError(error, mode);
    },
  };
}

function parseContentLengthFrame(input, limits) {
  const headerEnd = input.indexOf('\r\n\r\n');
  if (headerEnd === -1) {
    if (input.length > limits.maxHeaderBytes) {
      throw new MCPFrameError('MCP Content-Length header exceeds maxHeaderBytes', {
        mode: 'content-length',
        consumed: input.length,
      });
    }
    return null;
  }

  if (headerEnd > limits.maxHeaderBytes) {
    throw new MCPFrameError('MCP Content-Length header exceeds maxHeaderBytes', {
      mode: 'content-length',
      consumed: headerEnd + 4,
    });
  }

  const headerText = input.subarray(0, headerEnd).toString('ascii');
  const contentLength = parseContentLengthHeader(headerText);
  if (contentLength == null) {
    throw new MCPFrameError('missing or invalid MCP Content-Length header', {
      mode: 'content-length',
      consumed: headerEnd + 4,
    });
  }
  if (contentLength > limits.maxFrameBytes) {
    throw new MCPFrameError('MCP frame exceeds maxFrameBytes', {
      mode: 'content-length',
      consumed: headerEnd + 4,
    });
  }

  const frameStart = headerEnd + 4;
  const frameEnd = frameStart + contentLength;
  if (input.length < frameEnd) return null;

  const payload = input.subarray(frameStart, frameEnd).toString('utf8');
  try {
    return {
      message: JSON.parse(payload),
      consumed: frameEnd,
      mode: 'content-length',
    };
  }
  catch (error) {
    throw new MCPFrameError(`invalid JSON-RPC frame payload: ${error.message}`, {
      mode: 'content-length',
      consumed: frameEnd,
    });
  }
}

function parseNDJSONFrame(input, limits) {
  const newlineIndex = input.indexOf('\n');
  if (newlineIndex === -1) {
    if (input.length > limits.maxLineBytes) {
      throw new MCPFrameError('MCP NDJSON line exceeds maxLineBytes', {
        mode: 'ndjson',
        consumed: input.length,
      });
    }
    return null;
  }

  if (newlineIndex > limits.maxLineBytes) {
    throw new MCPFrameError('MCP NDJSON line exceeds maxLineBytes', {
      mode: 'ndjson',
      consumed: newlineIndex + 1,
    });
  }

  const rawLine = input.subarray(0, newlineIndex).toString('utf8').replace(/\r$/, '');
  try {
    return {
      message: JSON.parse(rawLine),
      consumed: newlineIndex + 1,
      mode: 'ndjson',
    };
  }
  catch (error) {
    throw new MCPFrameError(`invalid NDJSON JSON-RPC payload: ${error.message}`, {
      mode: 'ndjson',
      consumed: newlineIndex + 1,
    });
  }
}

function parseContentLengthHeader(headerText) {
  for (const line of headerText.split('\r\n')) {
    const match = /^content-length:\s*(\d+)$/i.exec(line.trim());
    if (!match) continue;
    const value = Number(match[1]);
    if (!Number.isSafeInteger(value) || value < 0) return null;
    return value;
  }
  return null;
}

function startsWithContentLength(input) {
  const prefix = input.subarray(0, Math.min(input.length, 'Content-Length'.length)).toString('ascii');
  return /^Content-Length/i.test(prefix);
}

function frameLimits(limits) {
  return {
    maxFrameBytes: requirePositiveInteger(limits.maxFrameBytes, 'maxFrameBytes'),
    maxHeaderBytes: requirePositiveInteger(limits.maxHeaderBytes, 'maxHeaderBytes'),
    maxLineBytes: requirePositiveInteger(limits.maxLineBytes, 'maxLineBytes'),
  };
}

function requirePositiveInteger(value, label) {
  if (!Number.isSafeInteger(value) || value <= 0) {
    throw new Error(`UI test MCP ${label} must be a positive integer`);
  }
  return value;
}
