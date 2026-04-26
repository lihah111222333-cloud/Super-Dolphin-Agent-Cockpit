import { defineConfig } from 'vite';

export default defineConfig({
  test: {
    include: ['*.test.js'],
    environment: 'node',
    globalSetup: ['../scripts/vitest-global-setup.js'],
  },
});
