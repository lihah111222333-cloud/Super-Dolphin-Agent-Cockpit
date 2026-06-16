import React, { useEffect, useId, useState } from 'react';
import { ImageLightbox } from './ImageLightbox.jsx';
import { normalizeMessageText } from './markdownMessageModel.js';
import { sanitizeMermaidSvg, svgDataUrl } from './markdownMermaidModel.js';

const MERMAID_LABEL = 'Mermaid \u56fe\u8868';
const EXPAND_LABEL = '\u653e\u5927 Mermaid \u56fe\u8868';
const EXPAND_HINT = '\u70b9\u51fb\u653e\u5927';

let mermaidModulePromise = null;

function loadMermaidModule() {
  if (!mermaidModulePromise) {
    mermaidModulePromise = import('mermaid').then((module) => {
      const mermaid = module.default || module;
      return Promise.resolve(mermaid.initialize({
        startOnLoad: false,
        securityLevel: 'strict',
        theme: 'base',
        themeVariables: {
          fontFamily: 'ui-sans-serif, system-ui, -apple-system, BlinkMacSystemFont, "Segoe UI", sans-serif',
        },
      })).then(() => mermaid);
    });
  }
  return mermaidModulePromise;
}

function mermaidInitialState(diagram) {
  return diagram
    ? { status: 'loading', svg: '', error: '' }
    : { status: 'error', svg: '', error: 'Mermaid \u56fe\u8868\u5185\u5bb9\u4e3a\u7a7a' };
}

function MermaidDiagram({ source }) {
  const reactId = useId();
  const diagram = normalizeMessageText(source).trim();
  const [state, setState] = useState(() => mermaidInitialState(diagram));
  const [expanded, setExpanded] = useState(false);

  useEffect(() => {
    let cancelled = false;
    if (!diagram) return undefined;
    loadMermaidModule()
      .then((mermaid) => mermaid.render(`mermaid-${reactId.replace(/[^a-zA-Z0-9_-]/g, '')}`, diagram))
      .then((result) => {
        const svg = sanitizeMermaidSvg(result?.svg);
        if (!cancelled) setState({ status: 'ready', svg, error: '' });
      })
      .catch((error) => {
        if (!cancelled) setState({ status: 'error', svg: '', error: error?.message || String(error) });
      });
    return () => { cancelled = true; };
  }, [diagram, reactId]);

  useEffect(() => {
    if (!expanded) return undefined;
    const onKeyDown = (event) => {
      if (event.key === 'Escape') setExpanded(false);
    };
    window.addEventListener('keydown', onKeyDown);
    return () => window.removeEventListener('keydown', onKeyDown);
  }, [expanded]);

  if (state.status === 'ready' && state.svg) {
    const href = svgDataUrl(state.svg);
    return (
      <figure className="mermaid-diagram" aria-label={MERMAID_LABEL}>
        <button
          type="button"
          className="mermaid-diagram-preview"
          aria-label={EXPAND_LABEL}
          onClick={() => setExpanded(true)}
        >
          <img src={href} alt={MERMAID_LABEL} loading="lazy" decoding="async" />
          <span>{EXPAND_HINT}</span>
        </button>
        {expanded ? (
          <ImageLightbox label={MERMAID_LABEL} onClose={() => setExpanded(false)}>
            <div className="mermaid-lightbox-svg">
              <img src={href} alt={MERMAID_LABEL} />
            </div>
          </ImageLightbox>
        ) : null}
      </figure>
    );
  }

  return (
    <figure className={`mermaid-diagram mermaid-diagram--${state.status}`} aria-label={MERMAID_LABEL}>
      <figcaption>{state.status === 'loading' ? '\u6b63\u5728\u6e32\u67d3 Mermaid \u56fe\u8868...' : `Mermaid \u6e32\u67d3\u5931\u8d25\uff1a${state.error}`}</figcaption>
      <pre><code>{diagram}</code></pre>
    </figure>
  );
}

export { MermaidDiagram };
