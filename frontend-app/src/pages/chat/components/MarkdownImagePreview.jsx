import { useEffect, useState } from 'react';
import { ImageLightbox } from './ImageLightbox.jsx';

const DEFAULT_IMAGE_LABEL = '\u56fe\u7247\u9884\u89c8';
const EXPAND_IMAGE_PREFIX = '\u653e\u5927\u56fe\u7247';
const EXPAND_IMAGE_HINT = '\u70b9\u51fb\u653e\u5927';
const IMAGE_LOAD_FAILED_TEXT = '\u56fe\u7247\u65e0\u6cd5\u52a0\u8f7d';

function MarkdownImagePreview({ src, label }) {
  const [failed, setFailed] = useState(false);
  const [expanded, setExpanded] = useState(false);
  const displayLabel = (label || '').toString().trim() || DEFAULT_IMAGE_LABEL;
  useEffect(() => {
    if (!expanded) return undefined;
    const onKeyDown = (event) => {
      if (event.key === 'Escape') setExpanded(false);
    };
    window.addEventListener('keydown', onKeyDown);
    return () => window.removeEventListener('keydown', onKeyDown);
  }, [expanded]);

  if (failed) {
    return (
      <span className="message-image-fallback" role="note" title={src}>
        <span>{IMAGE_LOAD_FAILED_TEXT}</span>
        <code>{displayLabel}</code>
      </span>
    );
  }

  const lightbox = expanded ? (
    <ImageLightbox label={displayLabel} onClose={() => setExpanded(false)}>
      <img src={src} alt={displayLabel} />
    </ImageLightbox>
  ) : null;

  return (
    <>
      <button
        type="button"
        className="message-image-preview"
        aria-label={`${EXPAND_IMAGE_PREFIX} ${displayLabel}`}
        onClick={() => setExpanded(true)}
      >
        <img
          src={src}
          alt={displayLabel}
          loading="lazy"
          decoding="async"
          onError={() => setFailed(true)}
        />
        <span>{EXPAND_IMAGE_HINT}</span>
      </button>
      {lightbox}
    </>
  );
}

export { MarkdownImagePreview };
