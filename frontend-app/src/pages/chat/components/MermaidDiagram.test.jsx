import React from 'react';
import { fireEvent, render, screen, within } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import mermaid from 'mermaid';
import { MermaidDiagram } from './MermaidDiagram.jsx';
import { isMermaidLanguage, isMermaidSource, sanitizeMermaidSvg } from './markdownMermaidModel.js';

const mermaidMockState = vi.hoisted(() => ({ config: null }));

vi.mock('mermaid', () => ({
  default: {
    initialize: vi.fn((config) => {
      mermaidMockState.config = config;
    }),
    render: vi.fn((_id, source) => Promise.resolve({
      svg: `<svg role="img" aria-label="mock mermaid"><text>${source}</text></svg>`,
    })),
  },
}));

const MERMAID_LABEL = 'Mermaid \u56fe\u8868';
const EXPAND_LABEL = '\u653e\u5927 Mermaid \u56fe\u8868';

function escapeSvgText(value) {
  return (value || '').toString()
    .replace(/&/g, '&amp;')
    .replace(/</g, '&lt;')
    .replace(/>/g, '&gt;');
}

function mockMermaidSvg(source) {
  if (source.includes('<br') && mermaidMockState.config?.htmlLabels !== false) {
    return [
      '<svg role="img" aria-label="mock mermaid">',
      '<foreignObject><div xmlns="http://www.w3.org/1999/xhtml">',
      '<span class="nodeLabel">frontend-app<br>React + Vite</span>',
      '</div></foreignObject>',
      '</svg>',
    ].join('');
  }
  return `<svg role="img" aria-label="mock mermaid"><text>${escapeSvgText(source)}</text></svg>`;
}

function decodedSvgDataUrl(image) {
  const src = image.getAttribute('src') || '';
  const [, payload = ''] = src.split(',', 2);
  return decodeURIComponent(payload);
}

describe('MermaidDiagram', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    mermaid.render.mockImplementation((_id, source) => Promise.resolve({
      svg: mockMermaidSvg(source),
    }));
  });

  it('renders diagrams and opens the shared image lightbox', async () => {
    render(<MermaidDiagram source={'flowchart TD\n  A --> B'} />);

    const image = await screen.findByRole('img', { name: MERMAID_LABEL });
    expect(decodedSvgDataUrl(image)).toContain('flowchart TD');
    expect(mermaid.initialize).toHaveBeenCalledWith(expect.objectContaining({
      htmlLabels: false,
      securityLevel: 'strict',
    }));

    fireEvent.click(screen.getByRole('button', { name: EXPAND_LABEL }));

    const dialog = screen.getByRole('dialog', { name: `\u56fe\u7247\u9884\u89c8\uff1a${MERMAID_LABEL}` });
    expect(within(dialog).getByRole('img', { name: MERMAID_LABEL })).toBeInTheDocument();
  });

  it('renders flowchart labels that use HTML line breaks', async () => {
    render(
      <MermaidDiagram
        source={'flowchart TD\n    USER[\u7528\u6237 / \u684c\u9762\u7aef] --> UI[frontend-app<br/>React + Vite \u65b0 UI]'}
      />,
    );

    expect(await screen.findByRole('img', { name: MERMAID_LABEL })).toBeInTheDocument();
    expect(screen.queryByText(/Mermaid \u6e32\u67d3\u5931\u8d25/)).not.toBeInTheDocument();
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

  it('adds concrete dimensions to Mermaid SVGs that only expose a percentage width', () => {
    const svg = sanitizeMermaidSvg([
      '<svg role="img" aria-label="wide mermaid" width="100%" viewBox="0 0 5010.72412109375 1489.6917724609375">',
      '<rect width="100" height="80"></rect>',
      '<text>visible diagram</text>',
      '</svg>',
    ].join(''));

    expect(svg).toContain('width="5010.72412109375"');
    expect(svg).toContain('height="1489.6917724609375"');
    expect(svg).toContain('viewBox="0 0 5010.72412109375 1489.6917724609375"');
  });
});
