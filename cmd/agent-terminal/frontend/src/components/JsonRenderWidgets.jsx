import React, { useEffect, useRef, useState, useContext, useMemo, useCallback } from 'react';
import { renderAssistantMarkdown } from '../utils/assistant-markdown.js';
import { resolveRenderedMarkdownAction } from '../utils/assistant-markdown-click.js';
import echarts from '../lib/echarts-custom.js';
import { MarkdownActionContext } from './json-render-markdown-action-key.jsx';
import { JsonRenderer } from './JsonRenderer.jsx';

function renderChildren(children) {
  if (!Array.isArray(children)) return [];
  return children.map((child, i) => {
    if (typeof child === 'string') return child;
    if (child && typeof child === 'object' && child.type) {
      return <JsonRenderer key={i} spec={child} />;
    }
    return null;
  });
}

function parseSize(raw, fallback) {
  if (raw == null || raw === '') return fallback;
  const str = String(raw).trim();
  if (!str) return fallback;
  if (/^\d+(\.\d+)?$/.test(str)) return `${str}px`;
  return str;
}

export function JrCard({ spec = {} }) {
  const header = (spec.title || spec.description) ? (
    <div className="jr-card-header">
      {spec.title && <h3 className="jr-card-title">{spec.title}</h3>}
      {spec.description && <p className="jr-card-desc">{spec.description}</p>}
    </div>
  ) : null;

  const body = (
    <div className="jr-card-body">
      {renderChildren(spec.children || [])}
    </div>
  );

  return (
    <div className="jr-root jr-card">
      {header}
      {body}
    </div>
  );
}

export function JrMetric({ spec = {} }) {
  return (
    <div className="jr-root jr-metric">
      <span className="jr-metric-label">{spec.label || ''}</span>
      <span className="jr-metric-value">{String(spec.value ?? '')}</span>
    </div>
  );
}

export function JrStack({ spec = {} }) {
  const dir = spec.direction === 'row' ? 'jr-stack-row' : 'jr-stack-col';
  const gap = spec.gap ? `${spec.gap}px` : '8px';

  return (
    <div
      className={`jr-root jr-stack ${dir}`}
      style={{ gap }}
    >
      {renderChildren(spec.children || [])}
    </div>
  );
}

export function JrHeading({ spec = {} }) {
  const level = Math.min(Math.max(Number(spec.level) || 2, 1), 4);
  const Tag = `h${level}`;

  return <Tag className="jr-root jr-heading">{spec.text || ''}</Tag>;
}

export function JrTable({ spec = {} }) {
  const cols = spec.columns || [];
  const rows = spec.rows || [];

  return (
    <div className="jr-root jr-table-wrap">
      <table className="jr-table">
        <thead>
          <tr>
            {cols.map((c, i) => (
              <th key={i} className="jr-table-th">
                {typeof c === 'string' ? c : (c.label || c.key || '')}
              </th>
            ))}
          </tr>
        </thead>
        <tbody>
          {rows.map((row, ri) => (
            <tr key={ri}>
              {cols.map((c, ci) => {
                const key = typeof c === 'string' ? c : (c.key || c.label || '');
                const val = Array.isArray(row) ? row[ci] : (row[key] ?? '');
                return (
                  <td key={ci} className="jr-table-td">
                    {String(val)}
                  </td>
                );
              })}
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}

export function JrAlert({ spec = {} }) {
  const severity = spec.severity || 'info';
  const icons = { info: 'ℹ️', warning: '⚠️', error: '❌', success: '✅' };

  return (
    <div className={`jr-root jr-alert jr-alert-${severity}`}>
      <span className="jr-alert-icon">{icons[severity] || 'ℹ️'}</span>
      <div className="jr-alert-content">
        {spec.title && <strong className="jr-alert-title">{spec.title}</strong>}
        <span className="jr-alert-msg">{spec.message || ''}</span>
      </div>
    </div>
  );
}

export function JrBadge({ spec = {} }) {
  const variant = spec.variant || 'default';
  return <span className={`jr-root jr-badge jr-badge-${variant}`}>{spec.text || ''}</span>;
}

export function JrCodeBlock({ spec = {} }) {
  return (
    <div className="jr-root" style={{ position: 'relative' }}>
      {spec.language && <span className="jr-codeblock-lang">{spec.language}</span>}
      <pre className="jr-codeblock">{spec.code || ''}</pre>
    </div>
  );
}

export function JrList({ spec = {} }) {
  const ordered = spec.ordered === true;
  const Tag = ordered ? 'ol' : 'ul';
  const items = (spec.items || []).map((item, i) => (
    <li key={i} className="jr-list-item">
      {String(item)}
    </li>
  ));

  return <Tag className="jr-root jr-list">{items}</Tag>;
}

export function JrProgress({ spec = {} }) {
  const pct = Math.min(100, Math.max(0, Number(spec.value) || 0));

  return (
    <div className="jr-root jr-progress">
      <div className="jr-progress-header">
        {spec.label && <span className="jr-progress-label">{spec.label}</span>}
        <span className="jr-progress-pct">{pct}%</span>
      </div>
      <div className="jr-progress-track">
        <div className="jr-progress-fill" style={{ width: `${pct}%` }} />
      </div>
    </div>
  );
}

export function JrSeparator({ spec = {} }) {
  if (spec.label) {
    return (
      <div className="jr-root jr-separator-labeled">
        <hr className="jr-separator-line" />
        <span className="jr-separator-text">{spec.label}</span>
        <hr className="jr-separator-line" />
      </div>
    );
  }
  return <hr className="jr-root jr-separator" />;
}

export function JrText({ spec = {} }) {
  return (
    <p className="jr-root" style={{ margin: 0, fontSize: '12px', lineHeight: '1.5' }}>
      {spec.text || ''}
    </p>
  );
}

export function JrMarkdown({ spec = {}, onFileRefClick, onCitationClick }) {
  const markdownActionHandlers = useContext(MarkdownActionContext);

  const onMarkdownClick = useCallback((event) => {
    const action = resolveRenderedMarkdownAction(event);
    if (!action) return;
    if (typeof event?.preventDefault === 'function') event.preventDefault();
    if (typeof event?.stopPropagation === 'function') event.stopPropagation();
    if (action.type === 'file-ref') {
      markdownActionHandlers?.onFileRefClick?.(action.payload);
      onFileRefClick?.(action.payload);
      return;
    }
    if (action.type === 'citation') {
      markdownActionHandlers?.onCitationClick?.(action.payload);
      onCitationClick?.(action.payload);
    }
  }, [markdownActionHandlers, onFileRefClick, onCitationClick]);

  const html = useMemo(() => renderAssistantMarkdown((spec.text || '').toString()), [spec.text]);

  return (
    <div
      className="jr-root jr-markdown chat-item-markdown agent-markdown-root"
      dangerouslySetInnerHTML={{ __html: html }}
      onClick={onMarkdownClick}
    />
  );
}

export function JrChart({ spec = {} }) {
  const containerRef = useRef(null);
  const chartInstanceRef = useRef(null);

  const initChart = useCallback(() => {
    if (!containerRef.current) return;
    if (chartInstanceRef.current) {
      chartInstanceRef.current.dispose();
      chartInstanceRef.current = null;
    }
    const theme = (typeof spec?.theme === 'string' && spec.theme.trim())
      ? spec.theme.trim()
      : 'dark';
    chartInstanceRef.current = echarts.init(containerRef.current, theme, { renderer: 'canvas' });
    const option = spec?.option || spec || {};
    chartInstanceRef.current.setOption(option, { notMerge: true });
    requestAnimationFrame(() => {
      chartInstanceRef.current?.resize();
    });
  }, [spec]);

  useEffect(() => {
    initChart();
    const onWindowResize = () => {
      chartInstanceRef.current?.resize();
    };

    if (typeof window !== 'undefined') {
      window.addEventListener('resize', onWindowResize);
    }

    let resizeObserver = null;
    if (containerRef.current && typeof ResizeObserver !== 'undefined') {
      resizeObserver = new ResizeObserver(() => {
        if (!containerRef.current) return;
        if (!chartInstanceRef.current) {
          initChart();
        } else {
          chartInstanceRef.current.resize();
        }
      });
      resizeObserver.observe(containerRef.current);
    }

    return () => {
      if (typeof window !== 'undefined') {
        window.removeEventListener('resize', onWindowResize);
      }
      if (resizeObserver) {
        resizeObserver.disconnect();
      }
      if (chartInstanceRef.current) {
        chartInstanceRef.current.dispose();
        chartInstanceRef.current = null;
      }
    };
  }, [initChart]);

  return (
    <div
      ref={containerRef}
      className="jr-root jr-chart"
      style={{
        width: parseSize(spec?.width, '100%'),
        height: parseSize(spec?.height, '300px'),
      }}
    />
  );
}

export function JrStat({ spec = {} }) {
  const trend = spec.trend || '';
  let trendClass = '';
  let trendIcon = '';
  if (trend === 'up') {
    trendClass = 'jr-stat-up';
    trendIcon = 'up';
  } else if (trend === 'down') {
    trendClass = 'jr-stat-down';
    trendIcon = 'down';
  }

  return (
    <div className="jr-root jr-stat">
      <span className="jr-stat-label">{spec.label || ''}</span>
      <span className="jr-stat-value">{String(spec.value ?? '')}</span>
      {spec.change != null && (
        <span className={`jr-stat-change ${trendClass}`}>
          {trendIcon}
          {String(spec.change)}
        </span>
      )}
    </div>
  );
}

export function JrTabs({ spec = {} }) {
  const tabs = spec.tabs || [];
  const defaultActive = spec.defaultTab || tabs[0]?.key || tabs[0]?.label || '';
  const [activeTab, setActiveTab] = useState(defaultActive);

  useEffect(() => {
    if (tabs.length > 0 && !tabs.some(t => (t.key || t.label) === activeTab)) {
      setActiveTab(tabs[0]?.key || tabs[0]?.label || '');
    }
  }, [tabs]);

  const children = spec.children || [];
  const selectedIndex = tabs.findIndex(tab => (tab.key || tab.label) === activeTab);
  const activeChild = selectedIndex >= 0 ? children[selectedIndex] : null;

  return (
    <div className="jr-root jr-tabs">
      <div className="jr-tabs-header">
        {tabs.map((tab) => {
          const key = tab.key || tab.label || '';
          const isActive = key === activeTab;
          return (
            <button
              key={key}
              className={`jr-tab-btn ${isActive ? 'active' : ''}`}
              onClick={() => setActiveTab(key)}
            >
              {tab.label || key}
            </button>
          );
        })}
      </div>
      <div className="jr-tabs-body">
        {activeChild && (
          typeof activeChild === 'string' ? activeChild : <JsonRenderer spec={activeChild} />
        )}
      </div>
    </div>
  );
}

export function JrAccordion({ spec = {} }) {
  const [isOpen, setIsOpen] = useState(spec.open === true);

  return (
    <div className={`jr-root jr-accordion ${isOpen ? 'jr-accordion-open' : ''}`}>
      <button
        className="jr-accordion-trigger"
        onClick={() => setIsOpen((prev) => !prev)}
      >
        <span className="jr-accordion-arrow">{isOpen ? '▾' : '▸'}</span>
        <span>{spec.title || ''}</span>
      </button>
      {isOpen && (
        <div className="jr-accordion-body">
          {renderChildren(spec.children || [])}
        </div>
      )}
    </div>
  );
}

export function JrTimeline({ spec = {} }) {
  const items = spec.items || [];

  return (
    <div className="jr-root jr-timeline">
      {items.map((item, i) => {
        const status = item.status || 'pending';
        const dotClass = status === 'done' ? 'jr-dot-done'
          : status === 'active' ? 'jr-dot-active' : 'jr-dot-pending';
        const isLast = i === items.length - 1;
        return (
          <div key={i} className="jr-timeline-item">
            <div className="jr-timeline-dot-col">
              <div className={`jr-timeline-dot ${dotClass}`} />
              {!isLast && <div className="jr-timeline-line" />}
            </div>
            <div className="jr-timeline-content">
              <div className="jr-timeline-head">
                <strong>{item.title || ''}</strong>
                {item.time && <span className="jr-timeline-time">{item.time}</span>}
              </div>
              {item.description && (
                <p className="jr-timeline-desc">{item.description}</p>
              )}
            </div>
          </div>
        );
      })}
    </div>
  );
}

export function JrButton({ spec = {} }) {
  const variant = spec.variant || 'default';
  return (
    <button
      className={`jr-root jr-button jr-button-${variant}`}
      disabled={!!spec.disabled}
    >
      {spec.label || ''}
    </button>
  );
}

export function JrImage({ spec = {} }) {
  return (
    <figure className="jr-root jr-image">
      <img
        className="jr-image-img"
        src={spec.src || ''}
        alt={spec.alt || ''}
        style={spec.width ? { maxWidth: spec.width } : {}}
      />
      {spec.caption && <figcaption className="jr-image-caption">{spec.caption}</figcaption>}
    </figure>
  );
}

const EXPLICIT_LINK_PROTOCOL_RE = /^[a-zA-Z][a-zA-Z\d+.-]*:/;
const SAFE_LINK_PROTOCOLS = new Set(['http:', 'https:', 'mailto:']);

function resolveSafeLinkHref(rawHref) {
  const href = (rawHref || '').toString().trim();
  if (!href) return '#';
  if (!EXPLICIT_LINK_PROTOCOL_RE.test(href)) return href;
  try {
    const protocol = new URL(href).protocol.toLowerCase();
    return SAFE_LINK_PROTOCOLS.has(protocol) ? href : '#';
  } catch (error) {
    return '#';
  }
}

export function JrLink({ spec = {} }) {
  return (
    <a
      className="jr-root jr-link"
      href={resolveSafeLinkHref(spec.href)}
      target="_blank"
      rel="noopener noreferrer"
    >
      {spec.text || spec.href || ''}
    </a>
  );
}

export const WIDGET_REGISTRY = {
  Card: { component: JrCard },
  Metric: { component: JrMetric },
  Stat: { component: JrStat },
  Stack: { component: JrStack },
  Heading: { component: JrHeading },
  Table: { component: JrTable },
  Tabs: { component: JrTabs },
  Accordion: { component: JrAccordion },
  Timeline: { component: JrTimeline },
  Alert: { component: JrAlert },
  Badge: { component: JrBadge },
  CodeBlock: { component: JrCodeBlock },
  List: { component: JrList },
  Progress: { component: JrProgress },
  Separator: { component: JrSeparator },
  Text: { component: JrText },
  Markdown: { component: JrMarkdown },
  Chart: { component: JrChart },
  Button: { component: JrButton },
  Image: { component: JrImage },
  Link: { component: JrLink },
};
