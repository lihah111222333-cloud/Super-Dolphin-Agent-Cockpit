# Vite + React + TypeScript Template Updated for 2026

This starter combines React, TypeScript, Vite, Vitest, ESLint, and Prettier in a setup intended for Northwestern CS394 projects and similar coursework. The template now targets the 2026 major-version baseline verified in this repository on March 24, 2026.

![394-2026-Screenshot](./resources/2026-screenshot.png)

## Toolchain Baseline

- Node.js `22+` recommended
- npm `10+`
- React `19.2.x`
- TypeScript `5.9.x`
- ESLint `9.39.x`
- Vite `8.0.x`
- Vitest `4.1.x`

You can find more about these in the following links: [Vite](https://vitejs.dev), [React](https://reactjs.org/), [Typescript](https://www.typescriptlang.org/), [Eslint](https://eslint.org/), [Prettier](https://prettier.io/), [Vitest](https://vitest.dev/), [React Testing Library](https://testing-library.com/docs/react-testing-library/intro/)

## What's New in 2025/2026

- **React 19** with the latest React DOM and TypeScript types
- **Vite 8** for faster builds and dev server
- **Vitest 4** with visual UI mode and V8 coverage reporting
- **React Testing Library** with `@testing-library/jest-dom` for readable assertions and `@testing-library/user-event` for realistic user interaction simulation
- **TypeScript 5.9** with strict mode enabled
- **ESLint 9** using the new flat config format
- All dependencies updated to their latest stable versions
- **starter Copilot instructions** in `copilot-instructions.md` for code style and project structure guidelines

## Installation

Clone the repo and install dependencies:

```bash
npm install
```

If you use `nvm`, run:

```bash
nvm use
```

To create a fresh copy from GitHub:

```bash
npx degit toddwseattle/pretty-vitest-react-ts-template project-name
```

## Scripts

- `npm run dev` starts the Vite dev server.
- `npm run build` runs TypeScript and creates a production build.
- `npm run type-check` runs the TypeScript compiler without emitting files.
- `npm run lint` runs Prettier and ESLint across the repo.
- `npm test` runs Vitest without the browser UI.
- `npm test -- --run` runs the Vitest suite once without watch mode.
- `npm run test:ui` starts the Vitest UI.
  - The UI server is pinned to `127.0.0.1:51204`.

Install packages: `npm install`

Start the dev server: `npm run dev`

## Testing

This template uses [Vitest](https://vitest.dev/) with [React Testing Library](https://testing-library.com/docs/react-testing-library/intro/) for testing React components.

**Note**: npm audit currently reports GHSA-rf6f-7fwh-wjgh via flatted; awaiting upstream fix in @vitest/ui / flat-cache. This is a dev only dependency do notinclud npm audit of dev dependencies in your ci workflow until this is resolved.

### Running Tests

```bash
# Run tests in watch mode
npm test

# Run tests with the visual UI
npm run test:ui

# Run tests with coverage report
npm run test:coverage
```

### Writing Tests with React Testing Library

Tests live alongside your source files (e.g., `src/app.test.tsx` tests `src/App.tsx`). Here's a quick guide:

**Rendering a component:**

```tsx
import { render, screen } from '@testing-library/react';
import App from './App';

test('renders a heading', () => {
  render(<App />);
  expect(screen.getByText('Hello World')).toBeInTheDocument();
});
```

**Simulating user interactions with `userEvent`:**

```tsx
import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';

test('button click increments counter', async () => {
  const user = userEvent.setup();
  render(<Counter />);
  await user.click(screen.getByRole('button'));
  expect(screen.getByText('count is: 1')).toBeInTheDocument();
});
```

**Common queries (in order of priority):**

1. `getByRole` - query by ARIA role (preferred for accessibility)
2. `getByLabelText` - query by form label
3. `getByPlaceholderText` - query by input placeholder
4. `getByText` - query by visible text content
5. `getByAltText` - query by image alt text

### Testing Best Practices

- **Query by role first.** Use `getByRole` when possible. It encourages accessible markup and tests your component the way users interact with it.
- **Use `userEvent` over `fireEvent`.** `userEvent` simulates real browser behavior (focus, hover, keyboard events) while `fireEvent` dispatches a single DOM event. This catches more bugs.
- **Avoid testing implementation details.** Don't test state variables or internal methods. Test what the user sees and does.
- **Write descriptive test names.** A test name should explain what the component does, not how it does it. E.g., "shows error message when form is submitted empty" rather than "sets error state to true."
- **One assertion per behavior.** Each test should verify one behavior. Multiple related assertions in a test are fine, but avoid testing unrelated behaviors together.

### Test Setup

The test environment is configured in `vite.config.ts`:

- **Environment:** jsdom (simulates browser DOM)
- **Globals:** enabled (no need to import `describe`, `test`, `expect` manually)
- **Setup file:** `src/test/setup.ts` loads `@testing-library/jest-dom` matchers like `toBeInTheDocument()`
- **Coverage:** V8 provider with 70% thresholds for statements, branches, functions, and lines

## VS Code Setup

For the smoothest editing experience:

1. Install the ESLint extension.
2. Install the Prettier extension.
3. Enable `formatOnSave`.
4. Open a `.tsx` file and confirm both ESLint and Prettier are active in the editor.

## Copilot Instructions

There is a basic set of copilot instructions in `copilot-instructions.md` that you can refer to for code style and project structure guidelines. Feel free to modify/change/expand these instructions as needed for your project. The instructions link with the husky hooks to make sure code is linted and formatted consistently.

## Pre-commit Hook

The repo includes Husky setup and keeps the historical `pre-commit` lint flow in `package.json`. Running `npm run lint` before committing is still the safest way to ensure formatting and lint fixes are already applied.

## Acknowledgments

This template builds on earlier starter work from [theSwordBreaker](https://github.com/TheSwordBreaker/vite-reactts-eslint-prettier) and the React/Vitest teaching materials used in Northwestern CS394.
