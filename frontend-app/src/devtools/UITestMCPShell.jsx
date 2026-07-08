export function UITestMCPShell() {
  return (
    <div className="sa-window sidebar-collapsed" data-theme="dark" data-testid="frontend-app">
      <main className="sa-main" data-testid="ui-test-mcp-shell">
        <section className="chat-page">
          <textarea data-testid="composer-input" aria-label="Message input" />
          <button type="button" data-testid="composer-submit" aria-label="Send message">
            Send
          </button>
        </section>
      </main>
    </div>
  );
}
