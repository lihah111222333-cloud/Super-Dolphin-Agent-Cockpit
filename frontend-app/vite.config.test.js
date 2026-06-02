import process from 'node:process';
import { describe, expect, it } from 'vitest';
import config from './vite.config.js';

describe('frontend vite dev proxy', () => {
  it('proxies generated image assets to the Go asset server', () => {
    const backendAddr = process.env.SUPER_DOLPHIN_HTTP_ADDR || '127.0.0.1:4512';

    expect(config.server.proxy['/generated-image']).toEqual({
      target: `http://${backendAddr}`,
    });
  });
});
