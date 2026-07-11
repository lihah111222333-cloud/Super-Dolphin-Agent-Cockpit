import { describe, expect, it } from 'vitest';
import { imagePreviewSource } from './markdownMessageModel.js';

describe('markdownMessageModel', () => {
  it('does not mint local image preview URLs from raw absolute paths', () => {
    const localPath = 'C:\\Users\\alice\\Pictures\\Screenshots\\screen.png';

    expect(imagePreviewSource(localPath)).toBe('');
  });

  it('keeps backend-issued local image preview URLs renderable', () => {
    expect(imagePreviewSource('/local-image?id=asset_123')).toBe('/local-image?id=asset_123');
  });

  it('validates generated-image routes before rendering them', () => {
    const generatedPath = '/repo/.codex/generated_images/a.png';

    expect(imagePreviewSource('/generated-image?path=/tmp/secret.png')).toBe('');
    expect(imagePreviewSource(`/generated-image?path=${encodeURIComponent(generatedPath)}`)).toBe(`/generated-image?path=${encodeURIComponent(generatedPath)}`);
    expect(imagePreviewSource(generatedPath)).toBe(`/generated-image?path=${encodeURIComponent(generatedPath)}`);
  });

  it('rejects generated image paths that traverse out of the generated_images directory', () => {
    expect(imagePreviewSource('/repo/.codex/generated_images/../secret.png')).toBe('');
    expect(imagePreviewSource('/generated-image?path=/repo/.codex/generated_images/../secret.png')).toBe('');
  });
});
