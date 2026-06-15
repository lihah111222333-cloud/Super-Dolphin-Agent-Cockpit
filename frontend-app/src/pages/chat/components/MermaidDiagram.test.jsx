import React from 'react';
import { fireEvent, render, screen, within } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import mermaid from 'mermaid';
import { MermaidDiagram } from './MermaidDiagram.jsx';
import { isMermaidLanguage, isMermaidSource, sanitizeMermaidSvg } from './markdownMermaidModel.js';

vi.mock('mermaid', () => ({
  default: {
    initialize: vi.fn(),
    render: vi.fn((_id, source) => Promise.resolve({
      svg: `<svg role="img" aria-label="mock mermaid"><text>${source}</text></svg>`,
    })),
  },
}));

const MERMAID_LABEL = 'Mermaid \u56fe\u8868';
const EXPAND_LABEL = '\u653e\u5927 Mermaid \u56fe\u8868';

function decodedSvgDataUrl(image) {
  const src = image.getAttribute('src') || '';
  const [, payload = ''] = src.split(',', 2);
  return decodeURIComponent(payload);
}

describe('MermaidDiagram', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    mermaid.render.mockImplementation((_id, source) => Promise.resolve({
      svg: `<svg role="img" aria-label="mock mermaid"><text>${source}</text></svg>`,
    }));
  });

  it('renders diagrams and opens the shared image lightbox', async () => {
    render(<MermaidDiagram source={'flowchart TD\n  A --> B'} />);

    const image = await screen.findByRole('img', { name: MERMAID_LABEL });
    expect(decodedSvgDataUrl(image)).toContain('flowchart TD');

    fireEvent.click(screen.getByRole('button', { name: EXPAND_LABEL }));

    const dialog = screen.getByRole('dialog', { name: `\u56fe\u7247\u9884\u89c8\uff1a${MERMAID_LABEL}` });
    expect(within(dialog).getByRole('img', { name: MERMAID_LABEL })).toBeInTheDocument();
  });

  it('sanitizes unsafe SVG output before building the preview image', async () => {
    mermaid.render.mockResolvedValueOnce({
      svg: [
        '<svg role="img" aria-label="unsafe mermaid" onload="alert(1)">',
        '<script>alert(1)</script>',
        '<foreignObject><div>unsafe html</div></foreignObject>',
        '<a href="javascript:alert(1)"><text>unsafe link</text></a>',
        '<text>safe mermaid</text>',
        '</svg>',
      ].join(''),
    });

    render(<MermaidDiagram source={'flowchart TD\n  A --> B'} />);

    const image = await screen.findByRole('img', { name: MERMAID_LABEL });
    const svg = decodedSvgDataUrl(image);
    expect(svg).toContain('safe mermaid');
    expect(svg).not.toContain('<script');
    expect(svg).not.toContain('foreignObject');
    expect(svg).not.toContain('onload');
    expect(svg).not.toContain('javascript:alert');
  });

  it('keeps Mermaid language and source detection in the pure model', () => {
    expect(isMermaidLanguage('mmd')).toBe(true);
    expect(isMermaidLanguage('js')).toBe(false);
    expect(isMermaidSource('flowchart TD\n  A --> B')).toBe(true);
    expect(isMermaidSource('console.log("not a graph")')).toBe(false);
  });

  it('removes unsafe SVG nodes and attributes in the pure sanitizer', () => {
    const svg = sanitizeMermaidSvg([
      '<svg role="img" aria-label="unsafe mermaid" onload="alert(1)">',
      '<script>alert(1)</script>',
      '<foreignObject><div>unsafe html</div></foreignObject>',
      '<a href="javascript:alert(1)"><text>unsafe link</text></a>',
      '<text>safe mermaid</text>',
      '</svg>',
    ].join(''));

    expect(svg).toContain('safe mermaid');
    expect(svg).not.toContain('<script');
    expect(svg).not.toContain('foreignObject');
    expect(svg).not.toContain('onload');
    expect(svg).not.toContain('javascript:alert');
  });
});
