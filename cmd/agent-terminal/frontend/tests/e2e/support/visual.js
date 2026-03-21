// @ts-nocheck
import { expect } from '@playwright/test';

export const VISUAL_VIEWPORT = Object.freeze({ width: 1600, height: 1000 });

export async function prepareVisualSnapshot(page) {
  await page.setViewportSize(VISUAL_VIEWPORT);
  await page.emulateMedia({ reducedMotion: 'reduce' });
}

export async function expectVisualSnapshot(locator, name, options = {}) {
  await expect(locator).toHaveScreenshot(name, {
    animations: 'disabled',
    caret: 'hide',
    scale: 'css',
    ...options,
  });
}
