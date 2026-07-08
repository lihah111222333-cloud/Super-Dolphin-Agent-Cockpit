import React from 'react';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { render as rtlRender } from '@testing-library/react';

function createTestQueryClient() {
  return new QueryClient({
    defaultOptions: {
      queries: { retry: false },
      mutations: { retry: false },
    },
  });
}

export function renderWithQueryClient(ui, options) {
  const queryClient = createTestQueryClient();
  const view = rtlRender(<QueryClientProvider client={queryClient}>{ui}</QueryClientProvider>, options);
  return {
    ...view,
    rerender(nextUi) {
      return view.rerender(<QueryClientProvider client={queryClient}>{nextUi}</QueryClientProvider>);
    },
  };
}
