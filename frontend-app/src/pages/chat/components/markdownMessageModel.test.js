import { describe, expect, it } from 'vitest';
import { imagePreviewSource } from './markdownMessageModel.js';

describe('markdownMessageModel', () => {
  it('does not mint local image preview URLs from raw absolute paths', () => {
    const localPath = 'C:\\Users\\mima0000\\Pictures\\Screenshots\\screen.png';

    expect(imagePreviewSource(localPath)).toBe('');
  });

  it('keeps backend-issued local image preview URLs renderable', () => {
    expect(imagePreviewSource('/local-image?id=asset_123')).toBe('/local-image?id=asset_123');
  });
});
