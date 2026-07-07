import { normalizeMessageText } from './markdownMessageModel.js';

function svgDataUrl(svg) {
  const value = (svg || '').toString();
  if (!value) return '';
  return `data:image/svg+xml;charset=utf-8,${encodeURIComponent(value)}`;
}

function normalizeSvgAttributeValue(value) {
  return (
    Array.from((value || '').toString().trim())
    .filter((char) => {
      const charCode = char.charCodeAt(0);
      return charCode > 0x1f && charCode !== 0x7f && !/\s/.test(char);
    })
    .join('')
    .toLowerCase()
  );
}

function isDangerousSvgAttributeValue(value) {
  const normalized = normalizeSvgAttributeValue(value);
  if (
    normalized.startsWith('javascript:') ||
    normalized.startsWith('vbscript:') ||
    normalized.startsWith('data:text/html') ||
    normalized.startsWith('data:image/svg+xml')
  ) {
    return true;
  }
  for (const match of normalized.matchAll(/url\(([^)]*)\)/g)) {
    const target = (match[1] || '').replace(/^['"]|['"]$/g, '');
    if (!target.startsWith('#')) return true;
  }
  return (
    normalized.includes('expression(')
  );
}

function parseSvgViewBoxSize(viewBox) {
  const parts = (viewBox || '').toString().trim().split(/[\s,]+/);
  if (parts.length !== 4) return null;
  const width = Number(parts[2]);
  const height = Number(parts[3]);
  if (!Number.isFinite(width) || !Number.isFinite(height) || width <= 0 || height <= 0) {
    return null;
  }
  return { width: parts[2], height: parts[3] };
}

function isPercentageSvgDimension(value) {
  return /%$/.test((value || '').toString().trim());
}

function ensureSvgImageDimensions(svgElement) {
  const width = svgElement.getAttribute('width');
  const height = svgElement.getAttribute('height');
  const needsWidth = isPercentageSvgDimension(width);
  const needsHeight = isPercentageSvgDimension(height) || (needsWidth && !height);
  if (!needsWidth && !needsHeight) return;

  const viewBoxSize = parseSvgViewBoxSize(svgElement.getAttribute('viewBox'));
  if (!viewBoxSize) {
    throw new Error('Mermaid SVG \u7f3a\u5c11\u53ef\u7528\u4e8e\u56fe\u7247\u5e03\u5c40\u7684 viewBox');
  }
  if (needsWidth) svgElement.setAttribute('width', viewBoxSize.width);
  if (needsHeight) svgElement.setAttribute('height', viewBoxSize.height);
}

function sanitizeMermaidSvg(svg) {
  const value = (svg || '').toString();
  if (!value) return '';
  if (typeof DOMParser === 'undefined' || typeof XMLSerializer === 'undefined') {
    throw new Error('\u5f53\u524d\u73af\u5883\u4e0d\u652f\u6301 SVG \u6e05\u7406');
  }

  const documentNode = new DOMParser().parseFromString(value, 'image/svg+xml');
  if (documentNode.querySelector('parsererror')) {
    throw new Error('Mermaid SVG \u89e3\u6790\u5931\u8d25');
  }

  ensureSvgImageDimensions(documentNode.documentElement);

  documentNode.querySelectorAll('script, foreignObject, iframe, object, embed, image').forEach((node) => {
    node.remove();
  });

  documentNode.querySelectorAll('*').forEach((node) => {
    Array.from(node.attributes).forEach((attribute) => {
      const name = attribute.name.toLowerCase();
      if (
        name.startsWith('on') ||
        isDangerousSvgAttributeValue(attribute.value)
      ) {
        node.removeAttribute(attribute.name);
      }
    });
  });

  return new XMLSerializer().serializeToString(documentNode.documentElement);
}

function isMermaidLanguage(language) {
  const value = (language || '').toString().trim().toLowerCase();
  return value === 'mermaid' || value === 'mmd';
}

function isMermaidSource(source) {
  const firstLine = normalizeMessageText(source).trim().split('\n')[0]?.trim().toLowerCase() || '';
  return /^(flowchart|graph|sequencediagram|classdiagram|statediagram|statediagram-v2|erdiagram|journey|gantt|pie|mindmap|timeline|gitgraph|quadrantchart|requirementdiagram)\b/.test(firstLine);
}

export { isMermaidLanguage, isMermaidSource, sanitizeMermaidSvg, svgDataUrl };
