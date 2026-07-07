import { describe, expect, it } from 'vitest';
import {
  SILENT_ASYNC_FAILURE_ROOTS,
  silentAsyncFailureViolationsFromSources,
  silentAsyncFailureViolationsInSource,
} from './no-silent-async-failure.mjs';

describe('silent async failure guard', () => {
  it('scans frontend source only', () => {
    expect(SILENT_ASYNC_FAILURE_ROOTS).toEqual(['src']);
  });

  it('flags empty promise catches and empty catch blocks', () => {
    const source = `
      void refreshWorkflowSurface().catch(() => {});
      onClick={() => { void onRetry().catch((error) => {}); }}
      <button onClick={() => { void onRetry().catch(() => {}); }}>重试</button>
      try { await load(); } catch {}
      try { await save(); } catch (error) {}
    `;

    expect(silentAsyncFailureViolationsInSource('src/pages/workflows/WorkflowPage.jsx', source)).toEqual([
      {
        file: 'src/pages/workflows/WorkflowPage.jsx',
        line: 2,
        kind: 'empty-promise-catch',
        snippet: 'void refreshWorkflowSurface().catch(() => {});',
      },
      {
        file: 'src/pages/workflows/WorkflowPage.jsx',
        line: 3,
        kind: 'empty-promise-catch',
        snippet: 'onClick={() => { void onRetry().catch((error) => {}); }}',
      },
      {
        file: 'src/pages/workflows/WorkflowPage.jsx',
        line: 4,
        kind: 'empty-promise-catch',
        snippet: '<button onClick={() => { void onRetry().catch(() => {}); }}>重试</button>',
      },
      {
        file: 'src/pages/workflows/WorkflowPage.jsx',
        line: 5,
        kind: 'empty-catch-block',
        snippet: 'try { await load(); } catch {}',
      },
      {
        file: 'src/pages/workflows/WorkflowPage.jsx',
        line: 6,
        kind: 'empty-catch-block',
        snippet: 'try { await save(); } catch (error) {}',
      },
    ]);
  });

  it('flags catch handlers that only discard the caught error', () => {
    const source = `
      void runAction().catch((error) => { void error; });
      void runAction().catch((err) => void err);
      try { await save(); } catch (error) { void error; }
    `;

    expect(silentAsyncFailureViolationsInSource('src/pages/chat/chatUiActions.js', source)).toEqual([
      {
        file: 'src/pages/chat/chatUiActions.js',
        line: 2,
        kind: 'discarded-promise-catch-error',
        snippet: 'void runAction().catch((error) => { void error; });',
      },
      {
        file: 'src/pages/chat/chatUiActions.js',
        line: 3,
        kind: 'discarded-promise-catch-error',
        snippet: 'void runAction().catch((err) => void err);',
      },
      {
        file: 'src/pages/chat/chatUiActions.js',
        line: 4,
        kind: 'discarded-catch-error',
        snippet: 'try { await save(); } catch (error) { void error; }',
      },
    ]);
  });

  it('allows visible error handling', () => {
    const source = `
      void refreshWorkflowSurface().catch((error) => setNotice(error.message));
      try { await load(); } catch (error) { setNotice(error.message); }
      task.catch();
    `;

    expect(silentAsyncFailureViolationsInSource('src/pages/workflows/WorkflowPage.jsx', source)).toEqual([]);
  });

  it('ignores comments and strings', () => {
    const source = `
      // void refreshWorkflowSurface().catch(() => {});
      const sample = "try { await load(); } catch {}";
      const template = \`void task.catch(() => {})\`;
    `;

    expect(silentAsyncFailureViolationsInSource('src/pages/workflows/WorkflowPage.jsx', source)).toEqual([]);
  });

  it('allows documented parse and platform fallbacks', () => {
    const source = `
      try { JSON.parse(text); } catch {
        // Keep the original text when it is not valid JSON.
      }
      task.catch(() => {
        // Startup retry is best effort.
      });
    `;

    expect(silentAsyncFailureViolationsInSource('src/pages/workflows/WorkflowPage.jsx', source)).toEqual([]);
  });

  it('collects violations from source maps', () => {
    const sources = new Map([
      ['src/pages/workflows/WorkflowPage.jsx', 'void onRetry().catch(() => {});'],
      ['src/pages/shared/pageComponents.jsx', 'void onRetry().catch((error) => setRetryError(error.message));'],
    ]);

    expect(silentAsyncFailureViolationsFromSources(sources)).toEqual([
      {
        file: 'src/pages/workflows/WorkflowPage.jsx',
        line: 1,
        kind: 'empty-promise-catch',
        snippet: 'void onRetry().catch(() => {});',
      },
    ]);
  });
});
