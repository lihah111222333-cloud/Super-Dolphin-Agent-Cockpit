import React from 'react';
import { afterEach, describe, expect, it } from 'vitest';
import { cleanup, render, screen } from '@testing-library/react';
import { JrLink } from './components/JsonRenderWidgets.jsx';

afterEach(() => {
  cleanup();
});

describe('Json render widgets React runtime', () => {
  it('blocks script protocol links from assistant-provided specs', () => {
    render(<JrLink spec={{ href: 'javascript:alert(1)', text: 'bad' }} />);

    expect(screen.getByRole('link').getAttribute('href')).toBe('#');
  });

  it('keeps safe absolute links intact', () => {
    render(<JrLink spec={{ href: 'https://example.com/docs', text: 'docs' }} />);

    expect(screen.getByRole('link').getAttribute('href')).toBe('https://example.com/docs');
  });
});
