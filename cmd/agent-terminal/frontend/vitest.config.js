import { defineConfig, mergeConfig } from 'vitest/config';
import viteConfig from './vite.config.js';

export default mergeConfig(
    viteConfig,
    defineConfig({
        test: {
            include: ['src/**/*.test.{js,jsx,ts,tsx}'],
            environment: 'jsdom',
            globalSetup: ['./scripts/vitest-global-setup.js'],
            testTimeout: 60000,
        },
    })
);
