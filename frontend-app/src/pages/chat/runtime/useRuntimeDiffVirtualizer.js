import * as tanstackVirtual from "@tanstack/react-virtual";

/**
 * @param {import('@tanstack/react-virtual').PartialKeys<import('@tanstack/react-virtual').ReactVirtualizerOptions<HTMLDivElement, HTMLDivElement>, 'observeElementRect' | 'observeElementOffset' | 'scrollToFn'>} options
 * @returns {import('@tanstack/react-virtual').Virtualizer<HTMLDivElement, HTMLDivElement>}
 */
export function useRuntimeDiffVirtualizer(options) {
  "use no memo";
  return tanstackVirtual["useVirtualizer"](options);
}
