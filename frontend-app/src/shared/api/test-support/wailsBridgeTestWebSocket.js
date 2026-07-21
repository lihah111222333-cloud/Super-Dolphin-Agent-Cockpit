function createJSONRPCMessage(method, params) {
  return { data: JSON.stringify({ jsonrpc: "2.0", method, params }) };
}

export function createTestWebSocketClass(sockets) {
  return class TestWebSocket {
    static CONNECTING = 0;
    static OPEN = 1;

    constructor(url) {
      this.url = url;
      this.readyState = TestWebSocket.CONNECTING;
      this.sent = [];
      sockets.push(this);
    }

    send(data) {
      this.sent.push(data);
    }
    open() {
      this.readyState = TestWebSocket.OPEN;
      this.onopen?.();
    }
    close(event = { code: 1006, reason: "network lost" }) {
      this.readyState = 3;
      this.onclose?.(event);
    }
    emit(method, params) {
      this.onmessage?.(createJSONRPCMessage(method, params));
    }
    receive(message) {
      this.onmessage?.({ data: JSON.stringify(message) });
    }
  };
}
