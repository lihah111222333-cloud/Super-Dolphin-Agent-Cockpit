import { describe, expect, it } from 'vitest';
import { computed, ref } from '../lib/vue.esm-browser.prod.js';
import { useFileRefPreview } from './composables/useFileRefPreview.js';

describe('useFileRefPreview citation linkage', () => {
  it('opens image preview for image citations using timeline attachments', () => {
    const focusedDiffPath = ref('');
    const focusedDiffLine = ref(0);
    const fallbackDiffText = ref('');
    const fallbackMediaPreview = ref(null);
    const fallbackMarkdownPreview = ref(null);
    const pendingFileRefFocus = ref(null);

    const preview = useFileRefPreview({ threadStore: {} }, {
      selectedThreadId: ref('thread-1'),
      activeTimeline: computed(() => [{
        id: 'assistant-1',
        kind: 'assistant',
        attachments: [{
          kind: 'image',
          name: 'preview.png',
          path: '/tmp/preview.png',
          previewUrl: 'data:image/png;base64,abc',
        }],
      }]),
      activeThreadDiffText: computed(() => ''),
      focusedDiffPath,
      focusedDiffLine,
      fallbackDiffText,
      fallbackMediaPreview,
      fallbackMarkdownPreview,
      pendingFileRefFocus,
    });

    preview.onTimelineCitationClick({ kind: 'image', assetPointer: 'asset://image-1', raw: 'Screenshot' });

    expect(fallbackMediaPreview.value).toMatchObject({
      src: 'data:image/png;base64,abc',
      fullSrc: 'data:image/png;base64,abc',
      path: '/tmp/preview.png',
    });
    expect(focusedDiffPath.value).toBe('/tmp/preview.png');
    expect(focusedDiffLine.value).toBe(0);
  });

  it('falls back to direct imageSrc payload when no attachment lookup exists', () => {
    const focusedDiffPath = ref('');
    const focusedDiffLine = ref(0);
    const fallbackDiffText = ref('');
    const fallbackMediaPreview = ref(null);
    const fallbackMarkdownPreview = ref(null);
    const pendingFileRefFocus = ref(null);

    const preview = useFileRefPreview({ threadStore: {} }, {
      selectedThreadId: ref('thread-1'),
      activeTimeline: computed(() => []),
      activeThreadDiffText: computed(() => ''),
      focusedDiffPath,
      focusedDiffLine,
      fallbackDiffText,
      fallbackMediaPreview,
      fallbackMarkdownPreview,
      pendingFileRefFocus,
    });

    preview.onTimelineCitationClick({
      kind: 'image',
      assetPointer: '',
      imageSrc: 'https://example.com/shot.png',
      path: 'https://example.com/shot.png',
      raw: 'Preview image',
    });

    expect(fallbackMediaPreview.value).toMatchObject({
      src: 'https://example.com/shot.png',
      fullSrc: 'https://example.com/shot.png',
      path: 'https://example.com/shot.png',
    });
    expect(focusedDiffPath.value).toBe('https://example.com/shot.png');
    expect(focusedDiffLine.value).toBe(0);
  });
});
