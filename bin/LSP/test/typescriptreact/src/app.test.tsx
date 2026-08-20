import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { describe, expect, test } from 'vitest';

import App from './App';

describe('App component', () => {
  test('renders the heading and links', () => {
    render(<App />);
    expect(screen.getByText(/Vite \+ React \+ Typescript/)).toBeInTheDocument();
    expect(screen.getByText('Learn React')).toBeInTheDocument();
    expect(screen.getByText('Vite Docs')).toBeInTheDocument();
    expect(screen.getByText('Vitest Docs')).toBeInTheDocument();
  });

  test('counter should be 0 at the start', () => {
    render(<App />);
    expect(screen.getByText('count is: 0')).toBeInTheDocument();
  });

  test('counter should increment by one when clicked', async () => {
    const user = userEvent.setup();
    render(<App />);
    const counter = screen.getByRole('button');
    await user.click(counter);
    expect(screen.getByText('count is: 1')).toBeInTheDocument();
  });

  test('counter should increment multiple times', async () => {
    const user = userEvent.setup();
    render(<App />);
    const counter = screen.getByRole('button');
    await user.click(counter);
    await user.click(counter);
    await user.click(counter);
    expect(screen.getByText('count is: 3')).toBeInTheDocument();
  });

  test('renders the logo image', () => {
    render(<App />);
    const logo = screen.getByAltText('logo');
    expect(logo).toBeInTheDocument();
  });
});
