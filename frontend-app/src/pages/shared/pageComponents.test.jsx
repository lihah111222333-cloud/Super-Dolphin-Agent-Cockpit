import React from 'react';
import { render, screen } from '@testing-library/react';
import { Circle } from 'lucide-react';
import { describe, expect, it, vi } from 'vitest';
import { PageHeader, Panel, RetryableSyncError } from './pageComponents.jsx';

describe('pageComponents', () => {
  it('renders shared page primitives', () => {
    const onRetry = vi.fn();
    render(
      <>
        <PageHeader icon={Circle} title="Dashboard" subtitle="Status" />
        <Panel title="Details">Ready</Panel>
        <RetryableSyncError message="Sync failed" onRetry={onRetry} />
      </>,
    );

    expect(screen.getByRole('heading', { name: /Dashboard/ })).toBeInTheDocument();
    expect(screen.getByText('Ready')).toBeInTheDocument();
    expect(screen.getByRole('alert')).toHaveTextContent('Sync failed');
  });
});
