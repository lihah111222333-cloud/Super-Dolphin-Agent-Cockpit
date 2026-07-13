import { describe, expect, it } from 'vitest';
import { previewUrlFromResponse } from './workflowPreviewUrl.js';

describe('workflow final output preview URL defense', () => {
  it.each([
    'https://127.0.0.1:4511/shared-file-preview?id=sf_123',
    'http://example.com:4511/shared-file-preview?id=sf_123',
    'http://127.0.0.1:4511/not-preview?id=sf_123',
    'http://127.0.0.1:4511/shared-file-preview?id=',
    'http://127.0.0.1:4511/shared-file-preview?id=one&id=two',
    'http://user@127.0.0.1:4511/shared-file-preview?id=sf_123',
  ])('refuses unsafe renderer media URL %s', (url) => {
    expect(() => previewUrlFromResponse({ url, contentType: 'video/mp4' })).toThrow('preview URL');
  });

  it.each([
    'http://127.0.0.1:4511/shared-file-preview?id=sf_ipv4',
    'http://[::1]:4512/shared-file-preview?id=sf_ipv6',
  ])('accepts backend loopback preview URL %s', (url) => {
    expect(previewUrlFromResponse({ url, contentType: 'video/mp4' })).toEqual({
      contentType: 'video/mp4',
      url,
    });
  });
});
