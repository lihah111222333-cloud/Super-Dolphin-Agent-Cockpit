import postcss from 'postcss';
import {
  LAYER_TOKENS_FILE,
  cssFiles,
  mainSource,
  indexSource,
  mainCssImports,
  cssSources,
  EXPECTED_Z_INDEX_TOKENS,
  EXPECTED_Z_INDEX_FILES,
  OVERLAY_THEME_SELECTOR_MIGRATIONS,
  splitSelectors,
  activeZIndexDeclarations,
  selectorOccurrences,
  indexHostViolations,
  forbiddenHostStackingDeclarations,
} from './styles.test.fixture.js';
import { describe, expect, it } from 'vitest';
describe('css import order', () => {
  it('keeps the test stylesheet list aligned with the app cascade order', () => {
    expect(mainCssImports).toEqual(cssFiles);
  });

  it('documents the cascade groups in the main entrypoint', () => {
    expect(mainSource).toContain('Base layers load first');
    expect(mainSource).toContain('Route and feature styles stay in navigation order');
    expect(mainSource).toContain('Late polish layers intentionally override');
  });
});

describe('layer token and overlay host contract', () => {
  it('requires the dedicated layer token source without masking the rest of this suite', () => {
    expect(cssSources.get(LAYER_TOKENS_FILE)).not.toBe('');
  });

  it('imports the layer token source exactly once before every other production stylesheet', () => {
    expect(mainCssImports.filter((file) => file === LAYER_TOKENS_FILE)).toEqual([LAYER_TOKENS_FILE]);
    expect(mainCssImports[0]).toBe(LAYER_TOKENS_FILE);
  });

  it('keeps all 43 active z-index declarations in 12 files on exact known token references', () => {
    const declarations = activeZIndexDeclarations();
    const files = [...new Set(declarations.map((declaration) => declaration.file))].sort();
    const invalid = declarations.filter((declaration) => {
      const match = /^var\((--z-[a-z-]+)\)$/.exec(declaration.value);
      return !match || !EXPECTED_Z_INDEX_TOKENS.has(match[1]);
    });

    expect(declarations).toHaveLength(43);
    expect(files).toEqual(EXPECTED_Z_INDEX_FILES);
    expect(invalid).toEqual([]);
  });

  it('shares one light-default palette rule between the document root and app shell', () => {
    const expectedSelectors = new Set([
      ':root',
      ':root[data-theme="light"]',
      '.sa-window[data-theme="light"]',
    ]);
    const stylesRoot = postcss.parse(cssSources.get('src/styles.css'), { from: 'src/styles.css' });
    const paletteRules = stylesRoot.nodes.filter((node) => (
      node.type === 'rule'
      && splitSelectors(node.selector).some((selector) => expectedSelectors.has(selector))
    ));

    expect(paletteRules).toHaveLength(1);
    expect(new Set(splitSelectors(paletteRules[0].selector))).toEqual(expectedSelectors);

    const declarations = {};
    paletteRules[0].walkDecls((declaration) => {
      declarations[declaration.prop] = declaration.value;
    });
    expect(declarations['--surface']).toBe('#ffffff');
    expect(declarations['--text-pri']).toBe('#1b1c18');
  });

  it('classifies missing, duplicate, nested, and misordered overlay hosts', () => {
    expect(indexHostViolations('<body><div id="root"></div><div id="overlay-root"></div><script></script></body>')).toEqual([]);
    expect(indexHostViolations('<body><div id="root"></div><script></script></body>')).toContain('overlay-root-count');
    expect(indexHostViolations('<body><div id="root"></div><div id="overlay-root"></div><div id="overlay-root"></div><script></script></body>')).toContain('overlay-root-count');
    expect(indexHostViolations('<body><div id="root"><div id="overlay-root"></div></div><script></script></body>')).toContain('host-sibling');
    expect(indexHostViolations('<body><div id="root"></div><script></script><div id="overlay-root"></div></body>')).toContain('host-order');
  });

  it('requires one root and one overlay-root as body siblings before the module script', () => {
    expect(indexHostViolations(indexSource)).toEqual([]);
  });

  it('keeps html, body, and overlay-root free of accidental stacking contexts', () => {
    expect(forbiddenHostStackingDeclarations()).toEqual([]);
  });

  it('moves every light overlay selector from the app shell to the overlay host', () => {
    const remainingOldSelectors = [];
    const missingHostSelectors = [];

    for (const [oldSelector, hostSelector] of OVERLAY_THEME_SELECTOR_MIGRATIONS) {
      if (selectorOccurrences(oldSelector) !== 0) remainingOldSelectors.push(oldSelector);
      if (selectorOccurrences(hostSelector) !== 1) missingHostSelectors.push(hostSelector);
    }

    expect({ remainingOldSelectors, missingHostSelectors }).toEqual({
      remainingOldSelectors: [],
      missingHostSelectors: [],
    });
  });
});
